package mobile

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mejango/croptop/internal/host"
	"github.com/mejango/croptop/internal/ipfs"
	"github.com/mejango/croptop/internal/publish"
	"github.com/mejango/croptop/internal/render"
	"github.com/mejango/croptop/internal/store"
)

type Proposal struct {
	ID            string `json:"id"`
	CID           string `json:"cid"`
	Parent        string `json:"parent"`
	Sequence      string `json:"sequence"`
	Host          string `json:"host"`
	Time          int64  `json:"time"`
	ExpiresAt     int64  `json:"expiresAt"`
	RecordPayload []byte `json:"recordPayload"`
	PushPayload   []byte `json:"pushPayload"`
}

type Operation struct {
	ID          string    `json:"id"`
	IPNS        string    `json:"ipns"`
	PostID      string    `json:"postID"`
	State       string    `json:"state"`
	Title       string    `json:"title"`
	Caption     string    `json:"caption"`
	CreatedAt   int64     `json:"createdAt"`
	MediaSHA256 string    `json:"mediaSHA256"`
	MediaType   string    `json:"mediaType"`
	Proposal    *Proposal `json:"proposal,omitempty"`
	URL         string    `json:"url"`
	Error       string    `json:"error"`
	Code        string    `json:"code"`
}

type operation struct {
	Operation
	InputDigest     string                `json:"inputDigest"`
	MediaPath       string                `json:"mediaPath,omitempty"`
	Prepared        *publish.PreparedPost `json:"prepared,omitempty"`
	UnsignedRecord  []byte                `json:"unsignedRecord,omitempty"`
	CommitAttempted bool                  `json:"commitAttempted,omitempty"`
}

func (s *Server) respondOperation(w http.ResponseWriter, op *operation) {
	s.mu.Lock()
	status := 200
	if op.State == "preparing" || op.State == "committing" {
		status = 202
	}
	// Marshal while locked: a worker may finish while the response is written.
	b, err := json.Marshal(op.Operation)
	s.mu.Unlock()
	if err != nil {
		apiError(w, 500, "storage", "Could not read this operation.")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(b)
}

func (s *Server) allowedLocked(name string) bool {
	return !s.closed && s.Enabled && s.auth.Connections[name]
}

func (s *Server) createHTTP(w http.ResponseWriter, r *http.Request, name string) {
	s.mu.Lock()
	enabled := s.allowedLocked(name)
	s.mu.Unlock()
	if !enabled {
		apiError(w, 403, "connection_disabled", "Enable phone publishing for this site first.")
		return
	}
	select {
	case s.uploads <- struct{}{}:
		defer func() { <-s.uploads }()
	default:
		apiError(w, 429, "busy", "Too many uploads are in progress. Keep this draft and try again shortly.")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, MaxImageBytes+(64<<10))
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		apiError(w, 400, "invalid_upload", "Choose one image within the upload limit.")
		return
	}
	defer r.MultipartForm.RemoveAll()
	if len(r.MultipartForm.File) != 1 || len(r.MultipartForm.File["image"]) != 1 {
		apiError(w, 400, "invalid_upload", "Choose exactly one still image.")
		return
	}
	for k, v := range r.MultipartForm.Value {
		if (k != "id" && k != "title" && k != "caption") || len(v) != 1 {
			apiError(w, 400, "invalid_request", "Invalid post fields.")
			return
		}
	}
	id, title, caption := r.FormValue("id"), r.FormValue("title"), r.FormValue("caption")
	if !uuidPattern.MatchString(id) {
		apiError(w, 400, "invalid_id", "Retain an uppercase UUID for this draft.")
		return
	}
	if len(title) > MaxTitleBytes || len(caption) > MaxCaptionBytes || !utf8.ValidString(title+caption) {
		apiError(w, 400, "invalid_text", "Use a shorter title or caption.")
		return
	}
	f, err := r.MultipartForm.File["image"][0].Open()
	if err != nil {
		apiError(w, 400, "invalid_upload", "Could not read this image.")
		return
	}
	defer f.Close()
	input, err := io.ReadAll(io.LimitReader(f, MaxImageBytes+1))
	if err != nil || len(input) == 0 || len(input) > MaxImageBytes {
		apiError(w, 413, "image_too_large", "Choose an image within the upload limit.")
		return
	}
	identity, _ := json.Marshal([]string{title, caption, digest(input)})
	inputDigest := digest(identity)
	key := operationKey(name, id)
	s.mu.Lock()
	if op := s.operations[key]; op != nil {
		if op.InputDigest != inputDigest {
			s.mu.Unlock()
			apiError(w, 409, "operation_conflict", "This operation ID already belongs to another draft. Keep its original image and text when retrying.")
			return
		}
		s.mu.Unlock()
		s.respondOperation(w, op)
		return
	}
	if !s.allowedLocked(name) {
		s.mu.Unlock()
		apiError(w, 403, "connection_disabled", "Phone publishing is disabled for this site.")
		return
	}
	count := 0
	for _, op := range s.operations {
		if op.IPNS == name && op.State != "published" && op.Code != "draft_expired" {
			count++
		}
	}
	if count >= 20 {
		s.mu.Unlock()
		apiError(w, 429, "draft_limit", "This site has too many pending drafts. Finish a pending post before uploading another.")
		return
	}
	op := &operation{Operation: Operation{ID: id, IPNS: name, PostID: id, Title: title, Caption: caption, CreatedAt: s.now().Unix(), State: "preparing"}, InputDigest: inputDigest}
	if err := atomicBytes(filepath.Join(s.operationDir(op), "upload"), input); err != nil {
		s.mu.Unlock()
		apiError(w, 500, "storage", "Could not save this upload. Keep the draft and retry.")
		return
	}
	if err := s.saveOperationLocked(op); err != nil {
		s.mu.Unlock()
		apiError(w, 500, "storage", "Could not save this operation. Keep the draft and retry.")
		return
	}
	s.operations[key] = op
	s.startPrepareLocked(op)
	s.mu.Unlock()
	s.respondOperation(w, op)
}

func (s *Server) operationHTTP(w http.ResponseWriter, r *http.Request, name, path string) {
	parts := strings.Split(path, "/")
	if len(parts) > 2 || !uuidPattern.MatchString(parts[0]) {
		apiError(w, 404, "not_found", "Operation not found.")
		return
	}
	s.mu.Lock()
	op := s.operations[operationKey(name, parts[0])]
	s.mu.Unlock()
	if op == nil {
		apiError(w, 404, "not_found", "Operation not found for this site.")
		return
	}
	action := ""
	if len(parts) == 2 {
		action = parts[1]
	}
	if action == "image" && r.Method == "GET" {
		s.imageHTTP(w, r, op)
		return
	}
	if action == "" && r.Method == "GET" {
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		if err := s.reconcile(ctx, op); err != nil {
			apiError(w, 503, "status_unavailable", "Could not confirm this operation yet. Keep the draft and check again.")
			return
		}
		s.expireDraft(op)
		s.respondOperation(w, op)
		return
	}
	if action == "prepare" && r.Method == "POST" {
		var body struct{}
		if !decodeJSON(w, r, &body) {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		if err := s.reconcile(ctx, op); err != nil {
			apiError(w, 503, "status_unavailable", "Could not confirm the previous attempt. Keep this draft and try again.")
			return
		}
		s.expireDraft(op)
		s.mu.Lock()
		if op.State == "published" {
			s.mu.Unlock()
			s.respondOperation(w, op)
			return
		}
		if !s.allowedLocked(name) {
			s.mu.Unlock()
			apiError(w, 403, "connection_disabled", "Phone publishing is disabled for this site.")
			return
		}
		if op.Code == "draft_expired" {
			s.mu.Unlock()
			apiError(w, 410, "draft_expired", "This private upload expired. Choose the retained image to start a new draft.")
			return
		}
		if s.active[operationKey(name, op.ID)] == nil {
			op.State = "preparing"
			op.Error = ""
			op.Code = ""
			if err := s.saveOperationLocked(op); err != nil {
				s.mu.Unlock()
				apiError(w, 500, "storage", "Could not save this retry.")
				return
			}
			s.startPrepareLocked(op)
		}
		s.mu.Unlock()
		s.respondOperation(w, op)
		return
	}
	if action == "commit" && r.Method == "POST" {
		s.commitHTTP(w, r, op)
		return
	}
	apiError(w, 404, "not_found", "Not found.")
}

func (s *Server) imageHTTP(w http.ResponseWriter, r *http.Request, op *operation) {
	s.mu.Lock()
	path, typ := op.MediaPath, op.MediaType
	s.mu.Unlock()
	if path == "" {
		apiError(w, 404, "image_pending", "The preview is not ready yet.")
		return
	}
	f, err := os.Open(path)
	if err != nil {
		apiError(w, 404, "image_expired", "This private preview is no longer available.")
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		apiError(w, 500, "storage", "Could not read the preview.")
		return
	}
	w.Header().Set("Content-Type", typ)
	w.Header().Set("Content-Disposition", "inline; filename="+strconv.Quote(filepath.Base(path)))
	http.ServeContent(w, r, filepath.Base(path), info.ModTime(), f)
}

func (s *Server) startPrepareLocked(op *operation) {
	key := operationKey(op.IPNS, op.ID)
	if s.active[key] != nil {
		return
	}
	select {
	case s.slots <- struct{}{}:
	default:
		op.State = "failed"
		op.Code = "busy"
		op.Error = "The publisher is busy. Retry this saved draft shortly."
		_ = s.saveOperationLocked(op)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), jobTimeout)
	active := &job{cancel: cancel}
	s.active[key] = active
	s.wg.Add(1)
	go func() {
		defer s.finishJob(key, active)
		if err := s.prepare(ctx, op); err != nil {
			s.fail(op, errorCode(err), err.Error())
		}
	}()
}

func (s *Server) finishJob(key string, active *job) {
	active.cancel()
	s.mu.Lock()
	if s.active[key] == active {
		delete(s.active, key)
	}
	s.mu.Unlock()
	<-s.slots
	s.wg.Done()
}

func (s *Server) prepare(ctx context.Context, op *operation) error {
	if err := s.reconcile(ctx, op); err != nil {
		return fmt.Errorf("Could not confirm the previous attempt: %w", err)
	}
	s.mu.Lock()
	done := op.State == "published"
	allowed := s.allowedLocked(op.IPNS)
	mediaPath := op.MediaPath
	s.mu.Unlock()
	if done {
		return nil
	}
	if !allowed {
		return errDisabled
	}
	if _, err := s.inspect(ctx, op.IPNS); err != nil {
		return err
	}
	// A retry owns this operation exclusively. Retire the old proposal before
	// removing its private files so a crash cannot leave a signable descriptor
	// referring to a partially removed tree.
	s.mu.Lock()
	op.Proposal = nil
	op.Prepared = nil
	op.UnsignedRecord = nil
	err := s.saveOperationLocked(op)
	s.mu.Unlock()
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(s.operationDir(op))
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), "prepare-") {
			if err := os.RemoveAll(filepath.Join(s.operationDir(op), entry.Name())); err != nil {
				return err
			}
		}
	}
	if mediaPath == "" {
		// A 40-megapixel 16-bit image can consume hundreds of MiB while decoded.
		// Keep this expensive phase bounded separately from network preparation.
		select {
		case s.normalizers <- struct{}{}:
		case <-ctx.Done():
			return ctx.Err()
		}
		media, err := NormalizeImage(ctx, filepath.Join(s.operationDir(op), "upload"), filepath.Join(s.operationDir(op), "media"))
		<-s.normalizers
		if err != nil {
			return &normalizationError{err}
		}
		s.mu.Lock()
		op.MediaPath = media.Path
		op.MediaType = media.Type
		op.MediaSHA256 = media.SHA256
		err = s.saveOperationLocked(op)
		mediaPath = op.MediaPath
		s.mu.Unlock()
		if err != nil {
			return err
		}
	}
	work, err := os.MkdirTemp(s.operationDir(op), "prepare-*")
	if err != nil {
		return err
	}
	created := store.FromTime(time.Unix(op.CreatedAt, 0))
	prepared, err := s.Publisher.PreparePost(ctx, s.HostURL, op.IPNS, publish.NewPost{ID: op.PostID, Created: &created, Title: op.Title, Content: op.Caption, Files: []string{mediaPath}, HeroImage: filepath.Base(mediaPath)}, work)
	if err != nil {
		if errors.Is(err, publish.ErrPostExists) {
			return s.reconcile(ctx, op)
		}
		return err
	}
	if prepared.PostID != op.PostID || prepared.Site == nil || prepared.Site.IPNS != op.IPNS {
		return errors.New("The prepared publication has a different identity.")
	}
	if publish.HostOf(prepared.Site) != s.HostURL {
		return errors.New("The prepared publication has a different hosting destination.")
	}
	if err := render.CheckMobileCompatibility(prepared.Site, s.TemplateDigest); err != nil {
		return err
	}
	record, payload, err := ipfs.NewUnsignedRecord(op.IPNS, prepared.CID, prepared.Sequence)
	if err != nil {
		return err
	}
	u, _ := url.Parse(s.HostURL)
	stamp := s.now().Unix()
	proposal := &Proposal{CID: prepared.CID, Parent: prepared.Parent, Sequence: strconv.FormatUint(prepared.Sequence, 10), Host: strings.ToLower(u.Hostname()), Time: stamp, ExpiresAt: s.now().Add(proposalLifetime).Unix(), RecordPayload: payload, PushPayload: host.PushMessage(strings.ToLower(u.Hostname()), op.IPNS, prepared.CID, prepared.Sequence, stamp)}
	b, _ := json.Marshal(proposal)
	proposal.ID = digest(b)
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.allowedLocked(op.IPNS) || ctx.Err() != nil {
		return errDisabled
	}
	if op.State == "published" {
		return nil
	}
	op.Prepared = &prepared
	op.UnsignedRecord = record
	op.Proposal = proposal
	op.State = "needs_signature"
	op.Error = ""
	op.Code = ""
	return s.saveOperationLocked(op)
}

var errDisabled = errors.New("Phone publishing is disabled for this site.")

type normalizationError struct{ error }

func (e *normalizationError) Unwrap() error { return e.error }

func errorCode(err error) string {
	var mediaErr *normalizationError
	switch {
	case errors.Is(err, errDisabled):
		return "connection_disabled"
	case errors.Is(err, publish.ErrParentChanged):
		return "parent_changed"
	case errors.Is(err, publish.ErrAuthorizationExpired):
		return "signature_expired"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, context.Canceled):
		return "interrupted"
	case errors.Is(err, ErrHEIFUnavailable):
		return "heif_unavailable"
	case errors.As(err, &mediaErr):
		return "image_invalid"
	default:
		return "prepare_failed"
	}
}

func (s *Server) fail(op *operation, code, message string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if op.State == "published" {
		return
	}
	op.State = "failed"
	op.Code = code
	op.Error = message
	_ = s.saveOperationLocked(op)
}

// Reconciliation searches the verified site's post list, not just its current
// head CID: a later author may have advanced the head after a lost commit reply.
func (s *Server) reconcile(ctx context.Context, op *operation) error {
	s.mu.Lock()
	done := op.State == "published"
	attempted := op.CommitAttempted
	state := op.State
	s.mu.Unlock()
	if done || (!attempted && state == "preparing") {
		return nil
	}
	posted, err := s.Publisher.InspectPost(ctx, s.HostURL, op.IPNS, op.PostID)
	if err != nil {
		return err
	}
	if posted == nil {
		s.mu.Lock()
		if op.State == "committing" && s.active[operationKey(op.IPNS, op.ID)] == nil {
			op.State = "failed"
			op.Code = "commit_unconfirmed"
			op.Error = "Publication was interrupted. Retry this retained draft to check and finish it."
			err = s.saveOperationLocked(op)
		}
		s.mu.Unlock()
		if err != nil {
			return err
		}
		return nil
	}
	if posted.URL == "" {
		return errors.New("Published post has no usable URL.")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	op.State = "published"
	op.URL = posted.URL
	op.Error = ""
	op.Code = ""
	return s.saveOperationLocked(op)
}

// Cleanup expires private staging after seven days, retaining operation receipts
// indefinitely. Run periodically with a bounded context. A failed reconciliation
// retains bytes and identity; uncertainty must never be interpreted as absence.
func (s *Server) Cleanup(ctx context.Context) error {
	if err := s.Init(); err != nil {
		return err
	}
	s.mu.Lock()
	var expired []*operation
	for key, op := range s.operations {
		if s.active[key] == nil && s.now().Unix()-op.CreatedAt >= int64(draftLifetime.Seconds()) {
			expired = append(expired, op)
		}
	}
	s.mu.Unlock()
	for _, op := range expired {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := s.reconcile(ctx, op); err != nil {
			return err
		}
		s.expireDraft(op)
		s.mu.Lock()
		if op.State == "published" && s.active[operationKey(op.IPNS, op.ID)] == nil {
			op.Prepared = nil
			op.UnsignedRecord = nil
			op.Proposal = nil
			op.MediaPath = ""
			if err := s.saveOperationLocked(op); err != nil {
				s.mu.Unlock()
				return err
			}
			entries, _ := os.ReadDir(s.operationDir(op))
			for _, entry := range entries {
				if entry.Name() != "operation.json" {
					_ = os.RemoveAll(filepath.Join(s.operationDir(op), entry.Name()))
				}
			}
		}
		s.mu.Unlock()
	}
	return nil
}

func (s *Server) expireDraft(op *operation) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if op.State == "published" || s.active[operationKey(op.IPNS, op.ID)] != nil || s.now().Unix()-op.CreatedAt < int64(draftLifetime.Seconds()) {
		return
	}
	op.State = "failed"
	op.Code = "draft_expired"
	op.Error = "This private upload expired after seven days. Your device may still have the original image."
	op.Proposal = nil
	op.Prepared = nil
	op.UnsignedRecord = nil
	if s.saveOperationLocked(op) != nil {
		return
	}
	// Only private bytes in this exact UUID directory are removed; keep the
	// durable receipt/identity so a late commit can still be reconciled.
	dir := s.operationDir(op)
	entries, _ := os.ReadDir(dir)
	for _, entry := range entries {
		if entry.Name() != "operation.json" {
			_ = os.RemoveAll(filepath.Join(dir, entry.Name()))
		}
	}
}

func (s *Server) commitHTTP(w http.ResponseWriter, r *http.Request, op *operation) {
	var body struct {
		ProposalID      string `json:"proposalId"`
		RecordSignature []byte `json:"recordSignature"`
		PushSignature   []byte `json:"pushSignature"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	s.mu.Lock()
	if op.State == "published" || op.State == "committing" {
		s.mu.Unlock()
		s.respondOperation(w, op)
		return
	}
	if !s.allowedLocked(op.IPNS) {
		s.mu.Unlock()
		apiError(w, 403, "connection_disabled", "Phone publishing is disabled for this site.")
		return
	}
	if op.State != "needs_signature" || op.Proposal == nil || op.Prepared == nil || body.ProposalID != op.Proposal.ID {
		s.mu.Unlock()
		apiError(w, 409, "proposal_changed", "Prepare this retained draft again before signing.")
		return
	}
	if op.Proposal.ExpiresAt <= s.now().Unix() || op.Proposal.Time > s.now().Unix()+30 {
		s.mu.Unlock()
		apiError(w, 409, "signature_expired", "Prepare this retained draft again for a fresh signature.")
		return
	}
	if !ipfs.VerifyIPNS(op.IPNS, op.Proposal.RecordPayload, body.RecordSignature) || !ipfs.VerifyIPNS(op.IPNS, op.Proposal.PushPayload, body.PushSignature) {
		s.mu.Unlock()
		apiError(w, 401, "invalid_signature", "The publication signatures could not be verified.")
		return
	}
	record, err := ipfs.CompleteRecord(op.IPNS, op.UnsignedRecord, body.RecordSignature)
	if err != nil {
		s.mu.Unlock()
		apiError(w, 401, "invalid_signature", "The signed IPNS record could not be verified.")
		return
	}
	select {
	case s.slots <- struct{}{}:
	default:
		s.mu.Unlock()
		apiError(w, 429, "busy", "The publisher is busy. Retry this saved operation shortly.")
		return
	}
	op.State = "committing"
	op.CommitAttempted = true
	op.Error = ""
	op.Code = ""
	if err := s.saveOperationLocked(op); err != nil {
		op.State = "needs_signature"
		<-s.slots
		s.mu.Unlock()
		apiError(w, 500, "storage", "Could not save commit intent. Keep the draft and retry.")
		return
	}
	prepared := *op.Prepared
	auth := publish.PostAuthorization{Timestamp: op.Proposal.Time, Signature: body.PushSignature, Record: record}
	ctx, cancel := context.WithTimeout(context.Background(), jobTimeout)
	key := operationKey(op.IPNS, op.ID)
	active := &job{cancel: cancel}
	s.active[key] = active
	s.wg.Add(1)
	s.mu.Unlock()
	go func() {
		defer s.finishJob(key, active)
		gate := s.siteGate(op.IPNS)
		gate.Lock()
		defer gate.Unlock()
		s.mu.Lock()
		allowed := s.allowedLocked(op.IPNS)
		s.mu.Unlock()
		if !allowed {
			s.fail(op, "connection_disabled", errDisabled.Error())
			return
		}
		if _, err := s.inspect(ctx, op.IPNS); err != nil {
			s.fail(op, "site_not_ready", err.Error())
			return
		}
		posted, err := s.Publisher.CommitPreparedPost(ctx, prepared, auth)
		if err == nil && posted.URL == "" {
			err = errors.New("The host did not return a confirmed post link.")
		}
		if err != nil {
			// The host may have committed before the reply was lost. Never turn
			// that uncertainty into a new logical post.
			rctx, rcancel := context.WithTimeout(context.Background(), 30*time.Second)
			reconcileErr := s.reconcile(rctx, op)
			rcancel()
			code := errorCode(err)
			if code == "prepare_failed" || reconcileErr != nil {
				code = "commit_unconfirmed"
			}
			s.fail(op, code, "Could not confirm publication. Keep this draft and retry to check the published site.")
			return
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		op.State = "published"
		op.URL = posted.URL
		op.Code = ""
		op.Error = ""
		// A failed journal write leaves the durable commit intent in place;
		// restart/status reconciliation will recover the verified post.
		_ = s.saveOperationLocked(op)
	}()
	s.respondOperation(w, op)
}
