package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/farshadfighter/taktaplus/internal/auth"
	"github.com/farshadfighter/taktaplus/internal/backup"
)

// backupError maps backup service errors to responses.
func (s *srv) backupError(w http.ResponseWriter, r *http.Request, err error) {
	var ie backup.InputError
	switch {
	case errors.As(err, &ie):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, backup.ErrNotFound):
		writeError(w, http.StatusNotFound, "not found")
	case errors.Is(err, backup.ErrNotLicensed), errors.Is(err, backup.ErrRestoreRunning):
		writeError(w, http.StatusConflict, err.Error())
	default:
		s.deviceError(w, r, err)
	}
}

// reauth checks the password and TOTP code sent with a sensitive request.
func (s *srv) reauth(w http.ResponseWriter, r *http.Request, password, code string) bool {
	sess, _ := sessionFrom(r.Context())
	err := s.auth.Reauthenticate(r.Context(), sess.User.ID, password, code)
	switch {
	case err == nil:
		return true
	case errors.Is(err, auth.ErrInvalidCredentials), errors.Is(err, auth.ErrInvalidCode):
		writeError(w, http.StatusForbidden, "password or authentication code is not correct")
	case errors.Is(err, auth.ErrLocked):
		writeError(w, http.StatusForbidden, err.Error())
	default:
		internalError(s.log, w, r, err)
	}
	return false
}

func (s *srv) listBackups(w http.ResponseWriter, r *http.Request) {
	var deviceID int64
	if p := optionalID(r, "device"); p != nil {
		deviceID = *p
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	list, err := s.backups.List(r.Context(), deviceID, limit)
	if err != nil {
		internalError(s.log, w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": list})
}

func (s *srv) runBackup(w http.ResponseWriter, r *http.Request) {
	id, ok := PathID(w, r, "id")
	if !ok {
		return
	}
	sess, _ := sessionFrom(r.Context())
	b, unchanged, err := s.backups.Run(r.Context(), id, backup.ReasonManual, sess.User.Username)
	if err != nil {
		s.backupError(w, r, err)
		return
	}
	s.audit(r, sess, "backup.run", "device", strconv.FormatInt(id, 10), map[string]any{"backup_id": b.ID, "unchanged": unchanged})
	writeJSON(w, http.StatusOK, map[string]any{"backup": b, "unchanged": unchanged})
}

func (s *srv) backupContent(w http.ResponseWriter, r *http.Request) {
	id, ok := PathID(w, r, "id")
	if !ok {
		return
	}
	cfg, b, err := s.backups.Content(r.Context(), id)
	if err != nil {
		s.backupError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"backup": b, "content": backup.Mask(string(cfg))})
}

func (s *srv) downloadBackup(w http.ResponseWriter, r *http.Request) {
	id, ok := PathID(w, r, "id")
	if !ok {
		return
	}
	cfg, b, err := s.backups.Content(r.Context(), id)
	if err != nil {
		s.backupError(w, r, err)
		return
	}
	sess, _ := sessionFrom(r.Context())
	s.audit(r, sess, "backup.download", "backup", strconv.FormatInt(id, 10), map[string]any{"device": b.DeviceName})
	name := b.DeviceName + "-" + b.At.UTC().Format("20060102-1504") + ".conf"
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="`+sanitizeFilename(name)+`"`)
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(cfg)
}

func sanitizeFilename(s string) string {
	out := make([]rune, 0, len(s))
	for _, c := range s {
		if c == '"' || c == '\\' || c == '/' || c < 0x20 {
			c = '_'
		}
		out = append(out, c)
	}
	return string(out)
}

func (s *srv) diffBackups(w http.ResponseWriter, r *http.Request) {
	from, to := optionalID(r, "from"), optionalID(r, "to")
	if from == nil || to == nil {
		writeError(w, http.StatusBadRequest, "from and to are required")
		return
	}
	changes, err := s.backups.DiffBackups(r.Context(), *from, *to)
	if err != nil {
		s.backupError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"changes": changes})
}

func (s *srv) diffCurrent(w http.ResponseWriter, r *http.Request) {
	id, ok := PathID(w, r, "id")
	if !ok {
		return
	}
	changes, err := s.backups.DiffCurrent(r.Context(), id)
	if err != nil {
		s.backupError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"changes": changes})
}

func (s *srv) updateBackup(w http.ResponseWriter, r *http.Request) {
	id, ok := PathID(w, r, "id")
	if !ok {
		return
	}
	var req struct {
		Locked bool   `json:"locked"`
		Note   string `json:"note"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := s.backups.SetLock(r.Context(), id, req.Locked, req.Note); err != nil {
		s.backupError(w, r, err)
		return
	}
	sess, _ := sessionFrom(r.Context())
	s.audit(r, sess, "backup.update", "backup", strconv.FormatInt(id, 10), map[string]any{"locked": req.Locked})
	w.WriteHeader(http.StatusNoContent)
}

func (s *srv) deleteBackup(w http.ResponseWriter, r *http.Request) {
	id, ok := PathID(w, r, "id")
	if !ok {
		return
	}
	if err := s.backups.Delete(r.Context(), id); err != nil {
		s.backupError(w, r, err)
		return
	}
	sess, _ := sessionFrom(r.Context())
	s.audit(r, sess, "backup.delete", "backup", strconv.FormatInt(id, 10), nil)
	w.WriteHeader(http.StatusNoContent)
}

func (s *srv) restoreBackup(w http.ResponseWriter, r *http.Request) {
	id, ok := PathID(w, r, "id")
	if !ok {
		return
	}
	var req struct {
		Password string `json:"password"`
		Code     string `json:"code"`
	}
	if !decodeJSON(w, r, &req) || !s.reauth(w, r, req.Password, req.Code) {
		return
	}
	sess, _ := sessionFrom(r.Context())
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	job, err := s.backups.Restore(ctx, id, sess.User.Username)
	if err != nil {
		s.backupError(w, r, err)
		return
	}
	s.audit(r, sess, "backup.restore", "device", strconv.FormatInt(job.DeviceID, 10), map[string]any{
		"backup_id": id, "job_id": job.ID, "status": job.Status})
	writeJSON(w, http.StatusAccepted, job)
}

func (s *srv) listRestores(w http.ResponseWriter, r *http.Request) {
	var deviceID int64
	if p := optionalID(r, "device"); p != nil {
		deviceID = *p
	}
	list, err := s.backups.ListRestores(r.Context(), deviceID)
	if err != nil {
		internalError(s.log, w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": list})
}

func (s *srv) getRestore(w http.ResponseWriter, r *http.Request) {
	id, ok := PathID(w, r, "id")
	if !ok {
		return
	}
	job, err := s.backups.GetRestore(r.Context(), id)
	if err != nil {
		s.backupError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, job)
}

func (s *srv) listPolicies(w http.ResponseWriter, r *http.Request) {
	list, err := s.backups.ListPolicies(r.Context())
	if err != nil {
		internalError(s.log, w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": list})
}

func (s *srv) savePolicy(w http.ResponseWriter, r *http.Request) {
	var p backup.Policy
	if !decodeJSON(w, r, &p) {
		return
	}
	p.ID = 0
	if r.Method == http.MethodPut {
		id, ok := PathID(w, r, "id")
		if !ok {
			return
		}
		p.ID = id
	}
	id, err := s.backups.SavePolicy(r.Context(), p)
	if err != nil {
		s.backupError(w, r, err)
		return
	}
	sess, _ := sessionFrom(r.Context())
	s.audit(r, sess, "backup_policy.save", "backup_policy", strconv.FormatInt(id, 10), map[string]any{"name": p.Name, "schedule": p.Schedule})
	writeJSON(w, http.StatusOK, map[string]any{"id": id})
}

func (s *srv) deletePolicy(w http.ResponseWriter, r *http.Request) {
	id, ok := PathID(w, r, "id")
	if !ok {
		return
	}
	if err := s.backups.DeletePolicy(r.Context(), id); err != nil {
		s.backupError(w, r, err)
		return
	}
	sess, _ := sessionFrom(r.Context())
	s.audit(r, sess, "backup_policy.delete", "backup_policy", strconv.FormatInt(id, 10), nil)
	w.WriteHeader(http.StatusNoContent)
}
