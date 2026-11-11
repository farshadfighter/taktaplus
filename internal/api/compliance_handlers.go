package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/farshadfighter/taktaplus/internal/compliance"
	"github.com/farshadfighter/taktaplus/internal/inventory"
)

func (s *srv) complianceError(w http.ResponseWriter, r *http.Request, err error) {
	var ie compliance.InputError
	switch {
	case errors.As(err, &ie):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, compliance.ErrNotFound), errors.Is(err, inventory.ErrNotFound):
		writeError(w, http.StatusNotFound, "not found")
	default:
		s.changeError(w, r, err)
	}
}

func (s *srv) complianceOverview(w http.ResponseWriter, r *http.Request) {
	list, err := s.compliance.Results(r.Context())
	if err != nil {
		internalError(s.log, w, r, err)
		return
	}
	type row struct {
		*compliance.Result
		DriftTemplates int `json:"drift_templates"`
		Differences    int `json:"differences"`
		BrokenRules    int `json:"broken_rules"`
	}
	rows := make([]row, 0, len(list))
	sum, n, drifting := 0, 0, 0
	broken := map[int64]bool{}
	var last *string
	for _, res := range list {
		d, diffs, b := res.Counts()
		rows = append(rows, row{res, d, diffs, b})
		if res.Error != "" {
			continue
		}
		sum += res.Score
		n++
		if d > 0 {
			drifting++
		}
		for _, v := range res.Violations {
			if !v.Excepted {
				broken[v.RuleID] = true
			}
		}
		if t := res.EvaluatedAt.UTC().Format("2006-01-02T15:04:05Z"); last == nil || t > *last {
			last = &t
		}
	}
	avg := 0
	if n > 0 {
		avg = (sum + n/2) / n
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": rows, "summary": map[string]any{
		"average": avg, "drifting": drifting, "broken_rules": len(broken), "last_evaluated": last}})
}

func (s *srv) complianceDevice(w http.ResponseWriter, r *http.Request) {
	id, ok := PathID(w, r, "id")
	if !ok {
		return
	}
	res, err := s.compliance.Result(r.Context(), id)
	if err != nil {
		s.complianceError(w, r, err)
		return
	}
	ex, err := s.compliance.Exceptions(r.Context(), id)
	if err != nil {
		internalError(s.log, w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"result": res, "exceptions": ex})
}

func (s *srv) complianceEvaluate(w http.ResponseWriter, r *http.Request) {
	if id := r.URL.Query().Get("device"); id != "" {
		n, err := strconv.ParseInt(id, 10, 64)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid device")
			return
		}
		res, err := s.compliance.Evaluate(r.Context(), n)
		if err != nil {
			s.complianceError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, res)
		return
	}
	if err := s.compliance.EvaluateAll(r.Context()); err != nil {
		internalError(s.log, w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *srv) listComplianceTemplates(w http.ResponseWriter, r *http.Request) {
	list, err := s.compliance.Templates(r.Context())
	if err != nil {
		internalError(s.log, w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": list})
}

func (s *srv) saveComplianceTemplate(w http.ResponseWriter, r *http.Request) {
	var t compliance.Template
	if !decodeJSON(w, r, &t) {
		return
	}
	t.ID = 0
	if r.Method == http.MethodPut {
		id, ok := PathID(w, r, "id")
		if !ok {
			return
		}
		t.ID = id
	}
	sess, _ := sessionFrom(r.Context())
	id, err := s.compliance.SaveTemplate(r.Context(), t, sess.User.Username)
	if err != nil {
		s.complianceError(w, r, err)
		return
	}
	s.audit(r, sess, "compliance_template.save", "compliance_template", strconv.FormatInt(id, 10), map[string]any{"name": t.Name, "auto_fix": t.AutoFix})
	go s.reevaluate()
	writeJSON(w, http.StatusOK, map[string]int64{"id": id})
}

// reevaluate refreshes every result after templates or rules change.
func (s *srv) reevaluate() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	if err := s.compliance.EvaluateAll(ctx); err != nil {
		s.log.Warn("compliance re-evaluation", "err", err)
	}
}

func (s *srv) deleteComplianceTemplate(w http.ResponseWriter, r *http.Request) {
	id, ok := PathID(w, r, "id")
	if !ok {
		return
	}
	if err := s.compliance.DeleteTemplate(r.Context(), id); err != nil {
		s.complianceError(w, r, err)
		return
	}
	sess, _ := sessionFrom(r.Context())
	s.audit(r, sess, "compliance_template.delete", "compliance_template", strconv.FormatInt(id, 10), nil)
	go s.reevaluate()
	w.WriteHeader(http.StatusNoContent)
}

func (s *srv) listComplianceRules(w http.ResponseWriter, r *http.Request) {
	list, err := s.compliance.Rules(r.Context())
	if err != nil {
		internalError(s.log, w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": list})
}

func (s *srv) saveComplianceRule(w http.ResponseWriter, r *http.Request) {
	var rule compliance.Rule
	if !decodeJSON(w, r, &rule) {
		return
	}
	rule.ID = 0
	if r.Method == http.MethodPut {
		id, ok := PathID(w, r, "id")
		if !ok {
			return
		}
		rule.ID = id
	}
	id, err := s.compliance.SaveRule(r.Context(), rule)
	if err != nil {
		s.complianceError(w, r, err)
		return
	}
	sess, _ := sessionFrom(r.Context())
	s.audit(r, sess, "compliance_rule.save", "compliance_rule", strconv.FormatInt(id, 10), map[string]any{"name": rule.Name, "enabled": rule.Enabled})
	go s.reevaluate()
	writeJSON(w, http.StatusOK, map[string]int64{"id": id})
}

func (s *srv) deleteComplianceRule(w http.ResponseWriter, r *http.Request) {
	id, ok := PathID(w, r, "id")
	if !ok {
		return
	}
	if err := s.compliance.DeleteRule(r.Context(), id); err != nil {
		s.complianceError(w, r, err)
		return
	}
	sess, _ := sessionFrom(r.Context())
	s.audit(r, sess, "compliance_rule.delete", "compliance_rule", strconv.FormatInt(id, 10), nil)
	go s.reevaluate()
	w.WriteHeader(http.StatusNoContent)
}

func (s *srv) addComplianceException(w http.ResponseWriter, r *http.Request) {
	id, ok := PathID(w, r, "id")
	if !ok {
		return
	}
	var in struct {
		Ref    string `json:"ref"`
		Reason string `json:"reason"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	sess, _ := sessionFrom(r.Context())
	if err := s.compliance.AddException(r.Context(), id, in.Ref, in.Reason, sess.User.Username); err != nil {
		s.complianceError(w, r, err)
		return
	}
	s.audit(r, sess, "compliance.exception", "device", strconv.FormatInt(id, 10), map[string]any{"ref": in.Ref, "reason": in.Reason})
	w.WriteHeader(http.StatusNoContent)
}

func (s *srv) deleteComplianceException(w http.ResponseWriter, r *http.Request) {
	id, ok := PathID(w, r, "id")
	if !ok {
		return
	}
	if err := s.compliance.DeleteException(r.Context(), id); err != nil {
		s.complianceError(w, r, err)
		return
	}
	sess, _ := sessionFrom(r.Context())
	s.audit(r, sess, "compliance.exception_delete", "compliance_exception", strconv.FormatInt(id, 10), nil)
	w.WriteHeader(http.StatusNoContent)
}

func (s *srv) fixDrift(w http.ResponseWriter, r *http.Request) {
	id, ok := PathID(w, r, "id")
	if !ok {
		return
	}
	var in struct {
		TemplateID int64 `json:"template_id"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	c, err := s.compliance.Fix(r.Context(), id, in.TemplateID, actorOf(r))
	if err != nil {
		s.complianceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, c)
}
