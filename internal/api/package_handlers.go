package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strconv"

	"github.com/farshadfighter/taktaplus/internal/packages"
)

func (s *srv) packageError(w http.ResponseWriter, r *http.Request, err error) {
	var ie packages.InputError
	switch {
	case errors.As(err, &ie):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, packages.ErrNotFound):
		writeError(w, http.StatusNotFound, "not found")
	default:
		internalError(s.log, w, r, err)
	}
}

func (s *srv) listPackages(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	list, err := s.pkgs.List(r.Context(), packages.Filter{Platform: q.Get("platform"), Type: q.Get("type"), All: q.Get("all") == "1"})
	if err != nil {
		internalError(s.log, w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": list})
}

func (s *srv) updatePackage(w http.ResponseWriter, r *http.Request) {
	id, ok := PathID(w, r, "id")
	if !ok {
		return
	}
	var m packages.Meta
	if !decodeJSON(w, r, &m) {
		return
	}
	if err := s.pkgs.Update(r.Context(), id, m); err != nil {
		s.packageError(w, r, err)
		return
	}
	sess, _ := sessionFrom(r.Context())
	s.audit(r, sess, "package.update", "package", strconv.FormatInt(id, 10), map[string]any{"type": m.Type, "version": m.Version})
	w.WriteHeader(http.StatusNoContent)
}

func (s *srv) deletePackage(w http.ResponseWriter, r *http.Request) {
	id, ok := PathID(w, r, "id")
	if !ok {
		return
	}
	p, err := s.pkgs.Get(r.Context(), id)
	if err != nil {
		s.packageError(w, r, err)
		return
	}
	if err := s.pkgs.Delete(r.Context(), id); err != nil {
		s.packageError(w, r, err)
		return
	}
	sess, _ := sessionFrom(r.Context())
	s.audit(r, sess, "package.delete", "package", strconv.FormatInt(id, 10), map[string]any{"file": p.File, "sha256": p.SHA256})
	w.WriteHeader(http.StatusNoContent)
}

// filePart returns the first "file" part of a multipart upload, after an
// optional "meta" JSON part.
func filePart(w http.ResponseWriter, r *http.Request, meta any) (io.Reader, string, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, packages.MaxFileSize+(1<<20))
	mr, err := r.MultipartReader()
	if err != nil {
		writeError(w, http.StatusBadRequest, "expected a multipart upload")
		return nil, "", false
	}
	for {
		part, err := mr.NextPart()
		if err != nil {
			writeError(w, http.StatusBadRequest, "no file in the upload")
			return nil, "", false
		}
		switch part.FormName() {
		case "meta":
			if meta != nil {
				if err := json.NewDecoder(io.LimitReader(part, 64<<10)).Decode(meta); err != nil {
					writeError(w, http.StatusBadRequest, "invalid package description")
					return nil, "", false
				}
			}
		case "file":
			return part, part.FileName(), true
		}
	}
}

func (s *srv) uploadPackage(w http.ResponseWriter, r *http.Request) {
	var meta *packages.Meta
	var raw json.RawMessage
	part, name, ok := filePart(w, r, &raw)
	if !ok {
		return
	}
	if len(raw) > 0 && string(raw) != "null" {
		meta = &packages.Meta{}
		if err := json.Unmarshal(raw, meta); err != nil {
			writeError(w, http.StatusBadRequest, "invalid package description")
			return
		}
	}
	p, err := s.pkgs.Upload(r.Context(), part, name, meta)
	if err != nil {
		s.packageError(w, r, err)
		return
	}
	sess, _ := sessionFrom(r.Context())
	s.audit(r, sess, "package.upload", "package", strconv.FormatInt(p.ID, 10), map[string]any{"file": p.File, "sha256": p.SHA256})
	writeJSON(w, http.StatusCreated, p)
}

func (s *srv) importBundle(w http.ResponseWriter, r *http.Request) {
	part, _, ok := filePart(w, r, nil)
	if !ok {
		return
	}
	tmp, err := os.CreateTemp(s.tmpDir, "bundle-*.zip")
	if err != nil {
		internalError(s.log, w, r, err)
		return
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	size, err := io.Copy(tmp, part)
	if err != nil {
		writeError(w, http.StatusBadRequest, "upload failed: "+err.Error())
		return
	}
	sess, _ := sessionFrom(r.Context())
	run, err := s.pkgs.ImportBundle(r.Context(), tmp, size, sess.User.Username)
	if err != nil {
		s.packageError(w, r, err)
		return
	}
	s.audit(r, sess, "package.bundle", "package", "", map[string]any{"added": run.Added, "seen": run.Seen, "status": run.Status})
	writeJSON(w, http.StatusOK, run)
}

func (s *srv) listSources(w http.ResponseWriter, r *http.Request) {
	list, err := s.pkgs.ListSources(r.Context())
	if err != nil {
		internalError(s.log, w, r, err)
		return
	}
	runs, err := s.pkgs.Runs(r.Context())
	if err != nil {
		internalError(s.log, w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": list, "runs": runs})
}

func (s *srv) saveSource(w http.ResponseWriter, r *http.Request) {
	var src packages.Source
	if !decodeJSON(w, r, &src) {
		return
	}
	src.ID = 0
	if r.Method == http.MethodPut {
		id, ok := PathID(w, r, "id")
		if !ok {
			return
		}
		src.ID = id
	}
	id, err := s.pkgs.SaveSource(r.Context(), src)
	if err != nil {
		s.packageError(w, r, err)
		return
	}
	sess, _ := sessionFrom(r.Context())
	s.audit(r, sess, "package_source.save", "package_source", strconv.FormatInt(id, 10), map[string]any{"name": src.Name, "kind": src.Kind})
	writeJSON(w, http.StatusOK, map[string]any{"id": id})
}

func (s *srv) deleteSource(w http.ResponseWriter, r *http.Request) {
	id, ok := PathID(w, r, "id")
	if !ok {
		return
	}
	if err := s.pkgs.DeleteSource(r.Context(), id); err != nil {
		s.packageError(w, r, err)
		return
	}
	sess, _ := sessionFrom(r.Context())
	s.audit(r, sess, "package_source.delete", "package_source", strconv.FormatInt(id, 10), nil)
	w.WriteHeader(http.StatusNoContent)
}

// syncSource starts a sync in the background; the panel follows the runs.
func (s *srv) syncSource(w http.ResponseWriter, r *http.Request) {
	id, ok := PathID(w, r, "id")
	if !ok {
		return
	}
	sess, _ := sessionFrom(r.Context())
	go func() {
		if _, err := s.pkgs.Sync(context.WithoutCancel(r.Context()), id, sess.User.Username); err != nil {
			s.log.Warn("package sync", "source", id, "err", err)
		}
	}()
	s.audit(r, sess, "package_source.sync", "package_source", strconv.FormatInt(id, 10), nil)
	w.WriteHeader(http.StatusAccepted)
}
