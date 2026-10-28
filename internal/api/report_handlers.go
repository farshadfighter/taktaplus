package api

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/farshadfighter/taktaplus/internal/logs"
	"github.com/farshadfighter/taktaplus/internal/reports"
)

func (s *srv) reportError(w http.ResponseWriter, r *http.Request, err error) {
	var ie reports.InputError
	switch {
	case errors.As(err, &ie):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, reports.ErrNotFound):
		writeError(w, http.StatusNotFound, "not found")
	case errors.Is(err, logs.ErrDisabled):
		writeError(w, http.StatusServiceUnavailable, err.Error())
	default:
		internalError(s.log, w, r, err)
	}
}

func (s *srv) listReportTemplates(w http.ResponseWriter, r *http.Request) {
	list, err := s.reports.Templates(r.Context())
	if err != nil {
		internalError(s.log, w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": list})
}

func (s *srv) saveReportTemplate(w http.ResponseWriter, r *http.Request) {
	var t reports.Template
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
	id, err := s.reports.SaveTemplate(r.Context(), t, sess.User.Username)
	if err != nil {
		s.reportError(w, r, err)
		return
	}
	s.audit(r, sess, "report_template.save", "report_template", strconv.FormatInt(id, 10), map[string]any{"name": t.Name, "widgets": t.Widgets})
	writeJSON(w, http.StatusOK, map[string]int64{"id": id})
}

func (s *srv) deleteReportTemplate(w http.ResponseWriter, r *http.Request) {
	id, ok := PathID(w, r, "id")
	if !ok {
		return
	}
	if err := s.reports.DeleteTemplate(r.Context(), id); err != nil {
		s.reportError(w, r, err)
		return
	}
	sess, _ := sessionFrom(r.Context())
	s.audit(r, sess, "report_template.delete", "report_template", strconv.FormatInt(id, 10), nil)
	w.WriteHeader(http.StatusNoContent)
}

func (s *srv) listReportSchedules(w http.ResponseWriter, r *http.Request) {
	list, err := s.reports.Schedules(r.Context())
	if err != nil {
		internalError(s.log, w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": list, "timezone": s.reports.Location().String()})
}

func (s *srv) saveReportSchedule(w http.ResponseWriter, r *http.Request) {
	var sc reports.Schedule
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
	id, err := s.reports.SaveSchedule(r.Context(), sc, sess.User.Username)
	if err != nil {
		s.reportError(w, r, err)
		return
	}
	s.audit(r, sess, "report_schedule.save", "report_schedule", strconv.FormatInt(id, 10),
		map[string]any{"template_id": sc.TemplateID, "frequency": sc.Frequency, "recipients": sc.Recipients, "enabled": sc.Enabled})
	writeJSON(w, http.StatusOK, map[string]int64{"id": id})
}

func (s *srv) deleteReportSchedule(w http.ResponseWriter, r *http.Request) {
	id, ok := PathID(w, r, "id")
	if !ok {
		return
	}
	if err := s.reports.DeleteSchedule(r.Context(), id); err != nil {
		s.reportError(w, r, err)
		return
	}
	sess, _ := sessionFrom(r.Context())
	s.audit(r, sess, "report_schedule.delete", "report_schedule", strconv.FormatInt(id, 10), nil)
	w.WriteHeader(http.StatusNoContent)
}

func (s *srv) reportChannels(w http.ResponseWriter, r *http.Request) {
	list, err := s.alerts.EmailChannels(r.Context())
	if err != nil {
		internalError(s.log, w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": list})
}

func (s *srv) listReportRuns(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	list, err := s.reports.Runs(r.Context(), limit)
	if err != nil {
		internalError(s.log, w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": list})
}

func (s *srv) startReport(w http.ResponseWriter, r *http.Request) {
	var in reports.RunInput
	if !decodeJSON(w, r, &in) {
		return
	}
	sess, _ := sessionFrom(r.Context())
	run, err := s.reports.Start(r.Context(), in, sess.User.Username)
	if err != nil {
		s.reportError(w, r, err)
		return
	}
	s.audit(r, sess, "report.run", "report_run", strconv.FormatInt(run.ID, 10), map[string]any{"template_id": in.TemplateID, "from": in.From, "to": in.To})
	writeJSON(w, http.StatusAccepted, run)
}

func (s *srv) retryReport(w http.ResponseWriter, r *http.Request) {
	id, ok := PathID(w, r, "id")
	if !ok {
		return
	}
	if err := s.reports.Retry(r.Context(), id); err != nil {
		s.reportError(w, r, err)
		return
	}
	sess, _ := sessionFrom(r.Context())
	s.audit(r, sess, "report.retry", "report_run", strconv.FormatInt(id, 10), nil)
	w.WriteHeader(http.StatusNoContent)
}

func (s *srv) deleteReport(w http.ResponseWriter, r *http.Request) {
	id, ok := PathID(w, r, "id")
	if !ok {
		return
	}
	if err := s.reports.DeleteRun(r.Context(), id); err != nil {
		s.reportError(w, r, err)
		return
	}
	sess, _ := sessionFrom(r.Context())
	s.audit(r, sess, "report.delete", "report_run", strconv.FormatInt(id, 10), nil)
	w.WriteHeader(http.StatusNoContent)
}

func (s *srv) downloadReport(w http.ResponseWriter, r *http.Request) {
	id, ok := PathID(w, r, "id")
	if !ok {
		return
	}
	run, err := s.reports.GetRun(r.Context(), id)
	if err != nil {
		s.reportError(w, r, err)
		return
	}
	path, f, ok := s.reports.FilePath(run, chi.URLParam(r, "format"))
	if !ok {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	file, err := os.Open(path)
	if err != nil {
		writeError(w, http.StatusNotFound, "the report file is no longer available")
		return
	}
	defer file.Close()
	ctype := "application/pdf"
	if f.Format == reports.FormatXLSX {
		ctype = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	}
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, f.Name))
	w.Header().Set("Content-Length", strconv.FormatInt(f.Size, 10))
	_, _ = file.WriteTo(w)
}
