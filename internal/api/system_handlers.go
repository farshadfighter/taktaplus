package api

import (
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/farshadfighter/taktaplus-lic/pkg/selfbackup"

	"github.com/farshadfighter/taktaplus/internal/system"
)

func (s *srv) systemError(w http.ResponseWriter, r *http.Request, err error) {
	var ie system.InputError
	switch {
	case errors.As(err, &ie):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, system.ErrBusy):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, system.ErrNotFound):
		writeError(w, http.StatusNotFound, "not found")
	default:
		internalError(s.log, w, r, err)
	}
}

func (s *srv) systemOverview(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	st, err := s.system.Settings(ctx)
	if err != nil {
		internalError(s.log, w, r, err)
		return
	}
	runs, err := s.system.Runs(ctx)
	if err != nil {
		internalError(s.log, w, r, err)
		return
	}
	ups, err := s.system.Upgrades(ctx)
	if err != nil {
		internalError(s.log, w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"info": s.system.Info(), "settings": st, "runs": runs, "upgrades": ups})
}

func (s *srv) saveSystemBackupSettings(w http.ResponseWriter, r *http.Request) {
	var in system.Settings
	if !decodeJSON(w, r, &in) {
		return
	}
	if err := s.system.SaveSettings(r.Context(), in); err != nil {
		s.systemError(w, r, err)
		return
	}
	sess, _ := sessionFrom(r.Context())
	s.audit(r, sess, "system_backup.settings", "system", "backup", map[string]any{"enabled": in.Enabled, "cron": in.Cron, "keep": in.Keep, "sftp": in.SFTPEnabled, "sftp_host": in.SFTP.Host})
	w.WriteHeader(http.StatusNoContent)
}

func (s *srv) setSystemBackupPassphrase(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Passphrase string `json:"passphrase"`
		Password   string `json:"password"`
		Code       string `json:"code"`
	}
	if !decodeJSON(w, r, &in) || !s.reauth(w, r, in.Password, in.Code) {
		return
	}
	if err := s.system.SetPassphrase(r.Context(), in.Passphrase); err != nil {
		s.systemError(w, r, err)
		return
	}
	sess, _ := sessionFrom(r.Context())
	s.audit(r, sess, "system_backup.passphrase", "system", "backup", nil)
	w.WriteHeader(http.StatusNoContent)
}

func (s *srv) testSystemSFTP(w http.ResponseWriter, r *http.Request) {
	var in selfbackup.SFTP
	if !decodeJSON(w, r, &in) {
		return
	}
	key, err := s.system.TestSFTP(r.Context(), in)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error(), "host_key": key})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "host_key": key})
}

func (s *srv) startSystemBackup(w http.ResponseWriter, r *http.Request) {
	sess, _ := sessionFrom(r.Context())
	id, err := s.system.Start(r.Context(), "manual", sess.User.Username)
	if err != nil {
		s.systemError(w, r, err)
		return
	}
	s.audit(r, sess, "system_backup.run", "system_backup", strconv.FormatInt(id, 10), nil)
	writeJSON(w, http.StatusAccepted, map[string]int64{"id": id})
}

func (s *srv) downloadSystemBackup(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("file")
	f, err := s.system.File(name)
	if err != nil {
		s.systemError(w, r, err)
		return
	}
	defer f.Close()
	sess, _ := sessionFrom(r.Context())
	s.audit(r, sess, "system_backup.download", "system", name, nil)
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="`+sanitizeFilename(name)+`"`)
	w.Header().Set("Cache-Control", "no-store")
	if st, err := f.Stat(); err == nil {
		w.Header().Set("Content-Length", strconv.FormatInt(st.Size(), 10))
	}
	_, _ = io.Copy(w, f)
}

func (s *srv) uploadUpgrade(w http.ResponseWriter, r *http.Request) {
	var meta struct {
		Password string `json:"password"`
		Code     string `json:"code"`
	}
	part, _, ok := filePart(w, r, &meta)
	if !ok {
		return
	}
	if !s.reauth(w, r, meta.Password, meta.Code) {
		return
	}
	sess, _ := sessionFrom(r.Context())
	u, _, err := s.system.Stage(r.Context(), part, sess.User.Username, true)
	if err != nil {
		s.systemError(w, r, err)
		return
	}
	s.audit(r, sess, "system.upgrade_upload", "system_upgrade", strconv.FormatInt(u.ID, 10), map[string]any{"version": u.Version, "from": u.FromVersion})
	writeJSON(w, http.StatusCreated, u)
}

func (s *srv) cancelUpgrade(w http.ResponseWriter, r *http.Request) {
	id, ok := PathID(w, r, "id")
	if !ok {
		return
	}
	if err := s.system.Cancel(r.Context(), id); err != nil {
		s.systemError(w, r, err)
		return
	}
	sess, _ := sessionFrom(r.Context())
	s.audit(r, sess, "system.upgrade_cancel", "system_upgrade", strconv.FormatInt(id, 10), nil)
	w.WriteHeader(http.StatusNoContent)
}
