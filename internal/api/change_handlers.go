package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/farshadfighter/taktaplus/internal/changes"
	"github.com/farshadfighter/taktaplus/internal/scripts"
)

func (s *srv) changeError(w http.ResponseWriter, r *http.Request, err error) {
	var ie changes.InputError
	switch {
	case errors.As(err, &ie):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, changes.ErrNotFound), errors.Is(err, scripts.ErrNotFound):
		writeError(w, http.StatusNotFound, "not found")
	case errors.Is(err, changes.ErrState), errors.Is(err, changes.ErrOwnChange):
		writeError(w, http.StatusConflict, err.Error())
	default:
		internalError(s.log, w, r, err)
	}
}

func actorOf(r *http.Request) changes.Actor {
	sess, _ := sessionFrom(r.Context())
	return changes.Actor{UserID: sess.User.ID, Username: sess.User.Username, IP: clientIP(r)}
}

func (s *srv) listChanges(w http.ResponseWriter, r *http.Request) {
	list, err := s.changes.List(r.Context(), r.URL.Query().Get("kind"), r.URL.Query().Get("status"))
	if err != nil {
		internalError(s.log, w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": list})
}

func (s *srv) getChange(w http.ResponseWriter, r *http.Request) {
	id, ok := PathID(w, r, "id")
	if !ok {
		return
	}
	c, err := s.changes.Get(r.Context(), id)
	if err != nil {
		s.changeError(w, r, err)
		return
	}
	targets, err := s.changes.Targets(r.Context(), id)
	if err != nil {
		internalError(s.log, w, r, err)
		return
	}
	approvals, err := s.changes.Approvals(r.Context(), id)
	if err != nil {
		internalError(s.log, w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"change": c, "targets": targets, "approvals": approvals})
}

// createChange plans a change of any registered kind.
func (s *srv) createChange(w http.ResponseWriter, r *http.Request) {
	var in changes.CreateInput
	if !decodeJSON(w, r, &in) {
		return
	}
	if in.Kind == scripts.Kind {
		// Copy the library script into the change so approvers see the
		// exact text that will run.
		var sp scripts.Spec
		if err := json.Unmarshal(in.Spec, &sp); err != nil {
			writeError(w, http.StatusBadRequest, "invalid script change")
			return
		}
		if sp.ScriptID != 0 && sp.Body == "" {
			sc, err := s.scripts.Get(r.Context(), sp.ScriptID)
			if err != nil {
				s.changeError(w, r, err)
				return
			}
			sp.Body = sc.Body
			in.Spec, _ = json.Marshal(sp)
		}
	}
	c, err := s.changes.Create(r.Context(), in, actorOf(r))
	if err != nil {
		s.changeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, c)
}

func (s *srv) changeAction(do func(r *http.Request, id int64, body map[string]string) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := PathID(w, r, "id")
		if !ok {
			return
		}
		body := map[string]string{}
		if r.ContentLength != 0 && !decodeJSON(w, r, &body) {
			return
		}
		if err := do(r, id, body); err != nil {
			s.changeError(w, r, err)
			return
		}
		s.getChange(w, r)
	}
}

func (s *srv) submitChange() http.HandlerFunc {
	return s.changeAction(func(r *http.Request, id int64, _ map[string]string) error {
		return s.changes.Submit(r.Context(), id, actorOf(r))
	})
}

func (s *srv) decideChange(approve bool) http.HandlerFunc {
	return s.changeAction(func(r *http.Request, id int64, b map[string]string) error {
		return s.changes.Decide(r.Context(), id, actorOf(r), approve, b["comment"])
	})
}

func (s *srv) cancelChange() http.HandlerFunc {
	return s.changeAction(func(r *http.Request, id int64, _ map[string]string) error {
		return s.changes.Cancel(r.Context(), id, actorOf(r))
	})
}

func (s *srv) runChangeNow() http.HandlerFunc {
	return s.changeAction(func(r *http.Request, id int64, b map[string]string) error {
		return s.changes.RunNow(r.Context(), id, actorOf(r), b["reason"])
	})
}

// --- windows and approval policies ---

func (s *srv) listWindows(w http.ResponseWriter, r *http.Request) {
	windows, err := s.changes.ListWindows(r.Context())
	if err != nil {
		internalError(s.log, w, r, err)
		return
	}
	policies, err := s.changes.ListPolicies(r.Context())
	if err != nil {
		internalError(s.log, w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"windows": windows, "policies": policies})
}

func (s *srv) saveWindow(w http.ResponseWriter, r *http.Request) {
	var win changes.Window
	if !decodeJSON(w, r, &win) {
		return
	}
	win.ID = 0
	if r.Method == http.MethodPut {
		id, ok := PathID(w, r, "id")
		if !ok {
			return
		}
		win.ID = id
	}
	id, err := s.changes.SaveWindow(r.Context(), win)
	if err != nil {
		s.changeError(w, r, err)
		return
	}
	sess, _ := sessionFrom(r.Context())
	s.audit(r, sess, "window.save", "window", strconv.FormatInt(id, 10), map[string]any{"name": win.Name})
	writeJSON(w, http.StatusOK, map[string]any{"id": id})
}

func (s *srv) deleteWindow(w http.ResponseWriter, r *http.Request) {
	id, ok := PathID(w, r, "id")
	if !ok {
		return
	}
	if err := s.changes.DeleteWindow(r.Context(), id); err != nil {
		s.changeError(w, r, err)
		return
	}
	sess, _ := sessionFrom(r.Context())
	s.audit(r, sess, "window.delete", "window", strconv.FormatInt(id, 10), nil)
	w.WriteHeader(http.StatusNoContent)
}

func (s *srv) setApprovalPolicy(w http.ResponseWriter, r *http.Request) {
	var req struct {
		GroupID  *int64 `json:"group_id"`
		Required *int   `json:"required"` // null removes a group's policy
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	var err error
	if req.Required == nil {
		if req.GroupID == nil {
			writeError(w, http.StatusBadRequest, "the default policy cannot be removed")
			return
		}
		err = s.changes.DeletePolicy(r.Context(), *req.GroupID)
	} else {
		err = s.changes.SetPolicy(r.Context(), req.GroupID, *req.Required)
	}
	if err != nil {
		s.changeError(w, r, err)
		return
	}
	sess, _ := sessionFrom(r.Context())
	s.audit(r, sess, "approval_policy.set", "approval_policy", "", map[string]any{"group_id": req.GroupID, "required": req.Required})
	w.WriteHeader(http.StatusNoContent)
}

// --- scripts ---

func (s *srv) listScripts(w http.ResponseWriter, r *http.Request) {
	list, err := s.scripts.List(r.Context())
	if err != nil {
		internalError(s.log, w, r, err)
		return
	}
	vars, err := s.scripts.Variables(r.Context())
	if err != nil {
		internalError(s.log, w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": list, "variables": vars})
}

func (s *srv) saveScript(w http.ResponseWriter, r *http.Request) {
	var sc scripts.Script
	if !decodeJSON(w, r, &sc) {
		return
	}
	sc.ID = 0
	if r.Method == http.MethodPut {
		id, ok := PathID(w, r, "id")
		if !ok {
			return
		}
		sc.ID = id
	}
	sess, _ := sessionFrom(r.Context())
	id, err := s.scripts.Save(r.Context(), sc, sess.User.Username)
	if err != nil {
		s.changeError(w, r, err)
		return
	}
	s.audit(r, sess, "script.save", "script", strconv.FormatInt(id, 10), map[string]any{"name": sc.Name})
	saved, _ := s.scripts.Get(r.Context(), id)
	writeJSON(w, http.StatusOK, saved)
}

func (s *srv) deleteScript(w http.ResponseWriter, r *http.Request) {
	id, ok := PathID(w, r, "id")
	if !ok {
		return
	}
	if err := s.scripts.Delete(r.Context(), id); err != nil {
		s.changeError(w, r, err)
		return
	}
	sess, _ := sessionFrom(r.Context())
	s.audit(r, sess, "script.delete", "script", strconv.FormatInt(id, 10), nil)
	w.WriteHeader(http.StatusNoContent)
}

func (s *srv) previewScript(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Body     string            `json:"body"`
		DeviceID int64             `json:"device_id"`
		Vars     map[string]string `json:"vars"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	d, err := s.inv.Get(r.Context(), req.DeviceID)
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	text, err := s.scripts.Render(r.Context(), req.Body, d, req.Vars)
	if err != nil {
		s.changeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"text": text, "read_only": scripts.IsReadOnly(text)})
}

func (s *srv) runScriptReadOnly(w http.ResponseWriter, r *http.Request) {
	id, ok := PathID(w, r, "id")
	if !ok {
		return
	}
	var req struct {
		DeviceIDs []int64 `json:"device_ids"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	sess, _ := sessionFrom(r.Context())
	runs, err := s.scripts.RunReadOnly(r.Context(), id, req.DeviceIDs, sess.User.Username)
	if err != nil {
		s.changeError(w, r, err)
		return
	}
	s.audit(r, sess, "script.run_readonly", "script", strconv.FormatInt(id, 10), map[string]any{"devices": len(req.DeviceIDs)})
	writeJSON(w, http.StatusOK, map[string]any{"items": runs})
}

func (s *srv) scriptRuns(w http.ResponseWriter, r *http.Request) {
	id, ok := PathID(w, r, "id")
	if !ok {
		return
	}
	runs, err := s.scripts.Runs(r.Context(), id)
	if err != nil {
		internalError(s.log, w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": runs})
}

func (s *srv) setVariable(w http.ResponseWriter, r *http.Request) {
	var v scripts.Variable
	if !decodeJSON(w, r, &v) {
		return
	}
	if err := s.scripts.SetVariable(r.Context(), v); err != nil {
		s.changeError(w, r, err)
		return
	}
	sess, _ := sessionFrom(r.Context())
	s.audit(r, sess, "variable.set", "variable", v.Name, map[string]any{"scope": v.Scope, "scope_id": v.ScopeID})
	w.WriteHeader(http.StatusNoContent)
}

func (s *srv) deleteVariable(w http.ResponseWriter, r *http.Request) {
	id, ok := PathID(w, r, "id")
	if !ok {
		return
	}
	if err := s.scripts.DeleteVariable(r.Context(), id); err != nil {
		s.changeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// firmwarePath previews the upgrade path for a device.
func (s *srv) firmwarePath(w http.ResponseWriter, r *http.Request) {
	id, ok := PathID(w, r, "id")
	if !ok {
		return
	}
	d, err := s.inv.Get(r.Context(), id)
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	path, err := s.firmware.Path(r.Context(), d, r.URL.Query().Get("version"))
	if err != nil {
		s.changeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"current": d.Firmware, "path": path})
}
