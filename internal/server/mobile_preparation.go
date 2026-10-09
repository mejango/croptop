package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/mejango/croptop/internal/ctxlock"
)

const phonePreparationTimeout = 5 * time.Minute
const phonePreparationRetention = 15 * time.Minute
const phonePreparationLimit = 128

type phonePreparationRequest struct {
	ID            string `json:"id"`
	EnableHosting bool   `json:"enableHosting"`
	AllowPublish  bool   `json:"allowPublish"`
}

type phonePreparationStatus struct {
	ID         string                 `json:"id"`
	SiteID     string                 `json:"siteID"`
	State      string                 `json:"state"`
	Stage      string                 `json:"stage"`
	Message    string                 `json:"message"`
	StartedAt  int64                  `json:"startedAt"`
	Deadline   int64                  `json:"deadline"`
	Connection *phoneConnectionResult `json:"connection,omitempty"`
	Error      string                 `json:"error,omitempty"`
	Code       string                 `json:"code,omitempty"`
}

type phonePreparation struct {
	Status    phonePreparationStatus
	Request   phonePreparationRequest
	Cancel    context.CancelFunc
	ExpiresAt time.Time
	Tombstone bool
}

func phonePreparationKey(siteID, id string) string { return siteID + "/" + id }

func phoneStageMessage(stage string) string {
	switch stage {
	case "service":
		return "Checking the phone service…"
	case "checking":
		return "Checking your published site; unpublished desktop edits stay on this computer…"
	case "publishing":
		return "Preparing the desktop site for its required phone update…"
	case "uploading":
		return "Uploading your site to hosting. Keep this computer awake…"
	case "verifying_host":
		return "Checking the hosted publication…"
	case "verifying_phone":
		return "The site is hosted. Waiting for the phone service to verify it…"
	case "hosting":
		return "Uploading and verifying the hosted site. Keep this computer awake…"
	case "pairing":
		return "Creating your private connection link…"
	default:
		return "Waiting for Croptop's current operation to finish…"
	}
}

func (s *Server) startPhonePreparation(w http.ResponseWriter, r *http.Request) {
	if !mobilePhoneAllowed(w, r) {
		return
	}
	var in phonePreparationRequest
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || !uuidRe.MatchString(in.ID) || !uuidRe.MatchString(r.PathValue("id")) {
		writeErr(w, 400, errors.New("invalid phone preparation request"))
		return
	}
	in.ID = strings.ToUpper(in.ID)
	siteID := strings.ToUpper(r.PathValue("id"))
	if _, err := s.Store.Site(siteID); err != nil {
		writeErr(w, 404, errors.New("site not found"))
		return
	}
	pending := s.phoneConnections()
	pending.mu.Lock()
	defer pending.mu.Unlock()
	now := time.Now()
	pending.prunePreparations(now)
	key := phonePreparationKey(siteID, in.ID)
	if job := pending.preparations[key]; job != nil {
		if !job.Tombstone && job.Request != in {
			writeJSON(w, 409, map[string]string{"error": "A preparation ID cannot be reused with different permissions.", "code": "preparation_mismatch"})
			return
		}
		writeJSON(w, 202, job.Status)
		return
	}
	for _, job := range pending.preparations {
		if job.Status.ID == in.ID && job.Status.SiteID != siteID {
			writeJSON(w, 409, map[string]string{"error": "A preparation ID belongs to a different site.", "code": "preparation_mismatch"})
			return
		}
		if job.Status.SiteID == siteID && (job.Status.State == "preparing" || job.Status.State == "cancelling") {
			writeJSON(w, 409, map[string]string{"error": "This site already has a connection being prepared. Finish or cancel it first.", "code": "preparation_in_progress"})
			return
		}
	}
	if len(pending.preparations) >= phonePreparationLimit {
		writeErr(w, 429, errors.New("too many connection attempts; try again later"))
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), phonePreparationTimeout)
	job := &phonePreparation{Request: in, Cancel: cancel, ExpiresAt: now.Add(phonePreparationRetention), Status: phonePreparationStatus{ID: in.ID, SiteID: siteID, State: "preparing", Stage: "waiting", Message: phoneStageMessage("waiting"), StartedAt: now.Unix(), Deadline: now.Add(phonePreparationTimeout).Unix()}}
	pending.preparations[key] = job
	go s.runPhonePreparation(ctx, job)
	writeJSON(w, 202, job.Status)
}

func (p *phoneConnections) prunePreparations(now time.Time) {
	for id, job := range p.preparations {
		if now.After(job.ExpiresAt) && job.Status.State != "preparing" && job.Status.State != "cancelling" {
			if job.Cancel != nil {
				job.Cancel()
			}
			delete(p.preparations, id)
		}
	}
}

func (s *Server) runPhonePreparation(ctx context.Context, job *phonePreparation) {
	pending := s.phoneConnections()
	progress := func(stage string) {
		pending.mu.Lock()
		defer pending.mu.Unlock()
		if job.Status.State == "preparing" {
			job.Status.Stage, job.Status.Message = stage, phoneStageMessage(stage)
		}
	}
	connection, err := s.preparePhone(ctx, job.Status.SiteID, phonePreparationOptions{EnableHosting: job.Request.EnableHosting, AllowPublish: job.Request.AllowPublish}, progress)
	pending.mu.Lock()
	defer pending.mu.Unlock()
	if job.Cancel != nil {
		job.Cancel()
	}
	if job.Status.State == "cancelling" {
		if connection != nil {
			pending.removeConnection(connection.ID)
		}
		job.Status.State, job.Status.Message = "cancelled", "Connection setup stopped. Hosting changes or a publication already committed are not undone."
	} else if err != nil {
		job.Status.State, job.Status.Error = "failed", err.Error()
		var problem *phoneProblem
		if errors.As(err, &problem) {
			job.Status.Code = problem.Code
		}
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			job.Status.Code, job.Status.Error = "preparation_timeout", "Phone setup reached its five-minute limit. A publication already committed may remain hosted. Try again to check the published site."
		} else if job.Status.Code == "" && errors.Is(err, context.DeadlineExceeded) {
			job.Status.Code = "service_timeout"
		}
		job.Status.Message = "Phone setup did not finish."
	} else {
		job.Status.State, job.Status.Stage, job.Status.Message, job.Status.Connection = "ready", "pairing", "Scan the private QR code with your phone.", connection
	}
	job.ExpiresAt = time.Now().Add(phonePreparationRetention)
}

func (s *Server) phonePreparationStatus(w http.ResponseWriter, r *http.Request) {
	if !mobilePhoneAllowed(w, r) {
		return
	}
	siteID, id := strings.ToUpper(r.PathValue("id")), strings.ToUpper(r.PathValue("prepid"))
	if !uuidRe.MatchString(siteID) || !uuidRe.MatchString(id) {
		writeErr(w, 400, errors.New("invalid preparation ID"))
		return
	}
	pending := s.phoneConnections()
	pending.mu.Lock()
	defer pending.mu.Unlock()
	pending.prunePreparations(time.Now())
	job := pending.preparations[phonePreparationKey(siteID, id)]
	if job == nil {
		writeErr(w, 404, errors.New("connection preparation expired; start again"))
		return
	}
	writeJSON(w, 200, job.Status)
}

func (s *Server) cancelPhonePreparation(w http.ResponseWriter, r *http.Request) {
	if !mobilePhoneAllowed(w, r) {
		return
	}
	siteID, id := strings.ToUpper(r.PathValue("id")), strings.ToUpper(r.PathValue("prepid"))
	if !uuidRe.MatchString(siteID) || !uuidRe.MatchString(id) {
		writeErr(w, 400, errors.New("invalid preparation ID"))
		return
	}
	pending := s.phoneConnections()
	pending.mu.Lock()
	defer pending.mu.Unlock()
	now := time.Now()
	pending.prunePreparations(now)
	key := phonePreparationKey(siteID, id)
	job := pending.preparations[key]
	if job == nil {
		if len(pending.preparations) >= phonePreparationLimit {
			writeErr(w, 429, errors.New("too many connection attempts; try again later"))
			return
		}
		job = &phonePreparation{Tombstone: true, ExpiresAt: now.Add(phonePreparationRetention), Status: phonePreparationStatus{ID: id, SiteID: siteID, State: "cancelled", Stage: "waiting", StartedAt: now.Unix(), Deadline: now.Unix()}}
		pending.preparations[key] = job
	}
	if job.Cancel != nil {
		job.Cancel()
	}
	if job.Status.Connection != nil {
		pending.removeConnection(job.Status.Connection.ID)
		job.Status.Connection = nil
	}
	if job.Status.State == "preparing" {
		job.Status.State = "cancelling"
	}
	if job.Status.State != "cancelling" {
		job.Status.State = "cancelled"
	}
	job.Status.Message = "Stopping setup. Hosting changes, committed publications and any key already sent are not revoked."
	if job.Status.State == "cancelled" {
		job.Status.Message = "Connection setup stopped. Hosting changes, committed publications and any key already sent are not revoked."
	}
	writeJSON(w, 200, job.Status)
}

// TryLock keeps a cancelled request from waiting indefinitely behind an existing
// render/publish, or later acquiring the gate and doing work after dismissal.
func (s *Server) lockPhoneContext(ctx context.Context) error {
	return ctxlock.Lock(ctx, &s.mu)
}
