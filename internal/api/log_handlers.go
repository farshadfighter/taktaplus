package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/farshadfighter/taktaplus/internal/logs"
	"github.com/farshadfighter/taktaplus/internal/scripts"
)

func (s *srv) logError(w http.ResponseWriter, r *http.Request, err error) {
	var ie logs.InputError
	switch {
	case errors.As(err, &ie):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, logs.ErrDisabled):
		writeError(w, http.StatusServiceUnavailable, err.Error())
	default:
		internalError(s.log, w, r, err)
	}
}

// maxRange bounds dashboard and search periods.
const maxRange = 400 * 24 * time.Hour

// parseRange reads from, to (RFC 3339) and devices (comma-separated IDs);
// the default is the last 24 hours.
func parseRange(r *http.Request) (logs.Range, error) {
	q := r.URL.Query()
	now := time.Now().UTC()
	rg := logs.Range{From: now.Add(-24 * time.Hour), To: now}
	for name, dst := range map[string]*time.Time{"from": &rg.From, "to": &rg.To} {
		if v := q.Get(name); v != "" {
			t, err := time.Parse(time.RFC3339, v)
			if err != nil {
				return rg, fmt.Errorf("%s must be an RFC 3339 time", name)
			}
			*dst = t.UTC()
		}
	}
	if !rg.To.After(rg.From) || rg.To.Sub(rg.From) > maxRange {
		return rg, errors.New("the period must be positive and at most 400 days")
	}
	if v := q.Get("devices"); v != "" {
		for _, p := range strings.Split(v, ",") {
			id, err := strconv.ParseInt(strings.TrimSpace(p), 10, 64)
			if err != nil || id <= 0 {
				return rg, errors.New("devices must be device IDs")
			}
			rg.DeviceIDs = append(rg.DeviceIDs, id)
		}
	}
	return rg, nil
}

func (s *srv) logStatus(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	st, err := s.logs.Settings(ctx)
	if err != nil {
		internalError(s.log, w, r, err)
		return
	}
	out := map[string]any{"enabled": s.logs.Enabled(), "settings": st, "geoip": s.logs.GeoIP().Info(),
		"syslog_server": s.syslogServer}
	if s.logs.Enabled() {
		out["stats"] = s.logs.Collector().Stats()
		usage, err := s.logs.Usage(ctx)
		if err != nil {
			s.log.Warn("log store usage", "err", err)
			out["store_error"] = err.Error()
		}
		out["usage"] = usage
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *srv) saveLogSettings(w http.ResponseWriter, r *http.Request) {
	var st logs.Settings
	if !decodeJSON(w, r, &st) {
		return
	}
	if err := s.logs.SaveSettings(r.Context(), st); err != nil {
		s.logError(w, r, err)
		return
	}
	sess, _ := sessionFrom(r.Context())
	s.audit(r, sess, "logs.settings", "settings", "logs", map[string]any{"retention": st.Retention, "trusted_sources": st.TrustedSources})
	w.WriteHeader(http.StatusNoContent)
}

func (s *srv) uploadGeoIP(w http.ResponseWriter, r *http.Request) {
	part, name, ok := filePart(w, r, nil)
	if !ok {
		return
	}
	if err := s.logs.GeoIP().Install(part, "upload "+name, ""); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	sess, _ := sessionFrom(r.Context())
	s.audit(r, sess, "logs.geoip_upload", "settings", "geoip", map[string]any{"file": name})
	writeJSON(w, http.StatusOK, s.logs.GeoIP().Info())
}

func (s *srv) logCatalog(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"widgets": logs.Widgets(), "tabs": logs.Tabs, "views": logs.Views()})
}

func (s *srv) logDashboard(w http.ResponseWriter, r *http.Request) {
	rg, err := parseRange(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	var ids []string
	if tab := r.URL.Query().Get("tab"); tab != "" {
		ids = logs.Tabs[tab]
		if ids == nil {
			writeError(w, http.StatusBadRequest, "unknown dashboard tab")
			return
		}
	} else if v := r.URL.Query().Get("widgets"); v != "" {
		ids = strings.Split(v, ",")
		for _, id := range ids {
			if !logs.KnownWidget(id) {
				writeError(w, http.StatusBadRequest, "unknown widget "+id)
				return
			}
		}
	}
	data, err := s.logs.Query(r.Context(), ids, rg)
	if err != nil {
		s.logError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"widgets": data, "from": rg.From, "to": rg.To})
}

func searchQuery(r *http.Request) (logs.SearchQuery, error) {
	rg, err := parseRange(r)
	if err != nil {
		return logs.SearchQuery{}, err
	}
	q := r.URL.Query()
	sq := logs.SearchQuery{Type: q.Get("type"), Range: rg}
	if v := q.Get("filters"); v != "" {
		if err := json.Unmarshal([]byte(v), &sq.Filters); err != nil || len(sq.Filters) > 20 {
			return sq, errors.New("filters must be a JSON list of at most 20 conditions")
		}
	}
	sq.Limit, _ = strconv.Atoi(q.Get("limit"))
	sq.Offset, _ = strconv.Atoi(q.Get("offset"))
	return sq, nil
}

func (s *srv) logSearch(w http.ResponseWriter, r *http.Request) {
	sq, err := searchQuery(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	res, err := s.logs.Search(r.Context(), sq)
	if err != nil {
		s.logError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *srv) logExport(w http.ResponseWriter, r *http.Request) {
	sq, err := searchQuery(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !s.logs.Enabled() {
		s.logError(w, r, logs.ErrDisabled)
		return
	}
	sess, _ := sessionFrom(r.Context())
	s.audit(r, sess, "logs.export", "logs", sq.Type, map[string]any{"from": sq.Range.From, "to": sq.Range.To, "filters": sq.Filters})
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="taktaplus-%s-%s.csv"`, sq.Type, time.Now().UTC().Format("20060102-1504")))
	if err := s.logs.ExportCSV(r.Context(), sq, w); err != nil {
		// Headers are gone; the truncated file is all the client gets.
		s.log.Error("log export failed", "err", err)
	}
}

func (s *srv) syslogConfig(w http.ResponseWriter, r *http.Request) {
	var in logs.DeviceSyslog
	if !decodeJSON(w, r, &in) {
		return
	}
	if err := in.Validate(); err != nil {
		s.logError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"fortigate": in.FortiGateCLI(), "fortiweb": in.FortiWebCLI()})
}

// syslogScript saves the device configuration as a library script, to run
// through the change workflow.
func (s *srv) syslogScript(w http.ResponseWriter, r *http.Request) {
	var in struct {
		logs.DeviceSyslog
		Platform string `json:"platform"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	if err := in.Validate(); err != nil {
		s.logError(w, r, err)
		return
	}
	sc := scripts.Script{Platform: in.Platform, Description: "ارسال لاگ به TaktaPlus (" + in.Protocol + " " + in.Server + ":" + strconv.Itoa(in.Port) + ")"}
	switch in.Platform {
	case "fortigate":
		sc.Name, sc.Body = "TaktaPlus — ارسال لاگ FortiGate", in.FortiGateCLI()
	case "fortiweb":
		sc.Name, sc.Body = "TaktaPlus — ارسال لاگ FortiWeb", in.FortiWebCLI()
	default:
		writeError(w, http.StatusBadRequest, "platform must be fortigate or fortiweb")
		return
	}
	list, err := s.scripts.List(r.Context())
	if err != nil {
		internalError(s.log, w, r, err)
		return
	}
	for _, old := range list {
		if old.Name == sc.Name {
			sc.ID = old.ID
		}
	}
	sess, _ := sessionFrom(r.Context())
	id, err := s.scripts.Save(r.Context(), sc, sess.User.Username)
	if err != nil {
		s.changeError(w, r, err)
		return
	}
	s.audit(r, sess, "script.save", "script", strconv.FormatInt(id, 10), map[string]any{"name": sc.Name})
	writeJSON(w, http.StatusOK, map[string]int64{"id": id})
}
