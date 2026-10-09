package mobile

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

var uuidPattern = regexp.MustCompile(`^[0-9A-F]{8}-[0-9A-F]{4}-[0-9A-F]{4}-[0-9A-F]{4}-[0-9A-F]{12}$`)

func operationKey(ipns, id string) string { return digest([]byte(ipns)) + "/" + id }
func (s *Server) operationDir(op *operation) string {
	return filepath.Join(s.DataDir, "operations", operationKey(op.IPNS, op.ID))
}

// Atomic journal replacement includes fsync of both file and directory. Draft
// uploads are synced before intent becomes visible, so a 202 is recoverable.
func atomicJSON(path string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return atomicBytes(path, b)
}
func atomicBytes(path string, b []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".write-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err := f.Chmod(0600); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(f.Name(), path); err != nil {
		return err
	}
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
func (s *Server) saveAuthLocked() error {
	return atomicJSON(filepath.Join(s.DataDir, "auth.json"), s.auth)
}
func (s *Server) saveOperationLocked(op *operation) error {
	return atomicJSON(filepath.Join(s.operationDir(op), "operation.json"), op)
}

func (s *Server) loadOperations() error {
	paths, err := filepath.Glob(filepath.Join(s.DataDir, "operations", "*", "*", "operation.json"))
	if err != nil {
		return err
	}
	for _, path := range paths {
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var op operation
		if err := json.Unmarshal(b, &op); err != nil {
			return fmt.Errorf("mobile operation journal: %w", err)
		}
		if !uuidPattern.MatchString(op.ID) || !uuidPattern.MatchString(op.PostID) || path != filepath.Join(s.operationDir(&op), "operation.json") {
			return fmt.Errorf("invalid mobile operation journal identity")
		}
		if op.State == "preparing" {
			op.State = "failed"
			op.Code = "interrupted"
			op.Error = "Preparation was interrupted. Retry this draft to continue."
		}
		s.operations[operationKey(op.IPNS, op.ID)] = &op
	}
	return nil
}
