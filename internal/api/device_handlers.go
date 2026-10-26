package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/farshadfighter/taktaplus/internal/alerts"
	"github.com/farshadfighter/taktaplus/internal/inventory"
)

// serviceError answers validation failures with 400, missing objects with
// 404 and anything else with 500.
func (s *srv) serviceError(w http.ResponseWriter, r *http.Request, err error) {
	var ie inventory.InputError
	var ae alerts.InputError
	switch {
	case errors.As(err, &ie), errors.As(err, &ae):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, inventory.ErrNotFound), errors.Is(err, alerts.ErrNotFound):
		writeError(w, http.StatusNotFound, "not found")
	default:
		internalError(s.log, w, r, err)
	}
}

// deviceError reports a failure talking to a device: the device's answer is
// useful to the user, so it is passed on with 502.
func (s *srv) deviceError(w http.ResponseWriter, r *http.Request, err error) {
	var ie inventory.InputError
	switch {
	case errors.As(err, &ie), errors.Is(err, inventory.ErrNotFound):
		s.serviceError(w, r, err)
	case errors.Is(err, context.Canceled):
		writeError(w, http.StatusServiceUnavailable, "request cancelled")
	default:
		writeError(w, http.StatusBadGateway, err.Error())
	}
}

func optionalID(r *http.Request, name string) *int64 {
	v, err := strconv.ParseInt(r.URL.Query().Get(name), 10, 64)
	if err != nil || v <= 0 {
		return nil
	}
	return &v
}

func (s *srv) listDevices(w http.ResponseWriter, r *http.Request) {
	list, err := s.inv.List(r.Context(), inventory.Filter{GroupID: optionalID(r, "group"), Query: r.URL.Query().Get("q")})
	if err != nil {
		internalError(s.log, w, r, err)
		return
	}
	statuses, err := s.inv.Statuses(r.Context())
	if err != nil {
		internalError(s.log, w, r, err)
		return
	}
	type row struct {
		*inventory.Device
		Health *inventory.Status `json:"health"`
	}
	out := make([]row, 0, len(list))
	for _, d := range list {
		out = append(out, row{d, statuses[d.ID]})
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

func (s *srv) discoverDevice(w http.ResponseWriter, r *http.Request) {
	var in inventory.Input
	if !decodeJSON(w, r, &in) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	res, err := s.inv.Discover(ctx, in)
	if err != nil {
		s.deviceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *srv) createDevice(w http.ResponseWriter, r *http.Request) {
	var in inventory.Input
	if !decodeJSON(w, r, &in) {
		return
	}
	id, err := s.inv.Create(r.Context(), in)
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	sess, _ := sessionFrom(r.Context())
	s.audit(r, sess, "device.create", "device", strconv.FormatInt(id, 10), map[string]any{"name": in.Name, "host": in.Host, "kind": in.Kind})
	// Probe right away; a failure leaves the device saved with its error.
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	if err := s.mon.Refresh(ctx, id); err != nil {
		var ie inventory.InputError
		if errors.As(err, &ie) {
			// The serial is already registered under another device.
			_ = s.inv.Delete(context.WithoutCancel(r.Context()), id)
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	d, err := s.inv.Get(r.Context(), id)
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, d)
}

func (s *srv) getDevice(w http.ResponseWriter, r *http.Request) {
	id, ok := PathID(w, r, "id")
	if !ok {
		return
	}
	d, err := s.inv.Get(r.Context(), id)
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	st, err := s.inv.Status(r.Context(), id)
	if err != nil {
		internalError(s.log, w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"device": d, "status": st})
}

func (s *srv) updateDevice(w http.ResponseWriter, r *http.Request) {
	id, ok := PathID(w, r, "id")
	if !ok {
		return
	}
	var in inventory.Input
	if !decodeJSON(w, r, &in) {
		return
	}
	if err := s.inv.Update(r.Context(), id, in); err != nil {
		s.serviceError(w, r, err)
		return
	}
	sess, _ := sessionFrom(r.Context())
	s.audit(r, sess, "device.update", "device", strconv.FormatInt(id, 10), map[string]any{
		"name": in.Name, "host": in.Host, "credentials_changed": in.Credentials != nil})
	w.WriteHeader(http.StatusNoContent)
}

func (s *srv) deleteDevice(w http.ResponseWriter, r *http.Request) {
	id, ok := PathID(w, r, "id")
	if !ok {
		return
	}
	d, err := s.inv.Get(r.Context(), id)
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	if err := s.inv.Delete(r.Context(), id); err != nil {
		s.serviceError(w, r, err)
		return
	}
	sess, _ := sessionFrom(r.Context())
	s.audit(r, sess, "device.delete", "device", strconv.FormatInt(id, 10), map[string]any{"name": d.Name, "serial": d.Serial})
	w.WriteHeader(http.StatusNoContent)
}

func (s *srv) refreshDevice(w http.ResponseWriter, r *http.Request) {
	id, ok := PathID(w, r, "id")
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	if err := s.mon.Refresh(ctx, id); err != nil {
		s.deviceError(w, r, err)
		return
	}
	s.getDevice(w, r)
}

func (s *srv) deviceMetrics(w http.ResponseWriter, r *http.Request) {
	id, ok := PathID(w, r, "id")
	if !ok {
		return
	}
	hours, _ := strconv.Atoi(r.URL.Query().Get("hours"))
	if hours <= 0 || hours > 24*30 {
		hours = 24
	}
	list, err := s.inv.Metrics(r.Context(), id, time.Now().Add(-time.Duration(hours)*time.Hour))
	if err != nil {
		internalError(s.log, w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": list})
}

// --- groups ---

func (s *srv) listGroups(w http.ResponseWriter, r *http.Request) {
	list, err := s.inv.ListGroups(r.Context())
	if err != nil {
		internalError(s.log, w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": list})
}

func (s *srv) saveGroup(w http.ResponseWriter, r *http.Request) {
	var id int64
	if r.Method == http.MethodPut {
		var ok bool
		if id, ok = PathID(w, r, "id"); !ok {
			return
		}
	}
	var req struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	newID, err := s.inv.SaveGroup(r.Context(), id, req.Name, req.Description)
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	sess, _ := sessionFrom(r.Context())
	s.audit(r, sess, "group.save", "group", strconv.FormatInt(newID, 10), map[string]any{"name": req.Name})
	writeJSON(w, http.StatusOK, map[string]any{"id": newID})
}

func (s *srv) deleteGroup(w http.ResponseWriter, r *http.Request) {
	id, ok := PathID(w, r, "id")
	if !ok {
		return
	}
	if err := s.inv.DeleteGroup(r.Context(), id); err != nil {
		s.serviceError(w, r, err)
		return
	}
	sess, _ := sessionFrom(r.Context())
	s.audit(r, sess, "group.delete", "group", strconv.FormatInt(id, 10), nil)
	w.WriteHeader(http.StatusNoContent)
}

// --- alerts ---

func (s *srv) listAlerts(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	var deviceID int64
	if p := optionalID(r, "device"); p != nil {
		deviceID = *p
	}
	limit, _ := strconv.Atoi(q.Get("limit"))
	list, err := s.alerts.List(r.Context(), q.Get("state"), deviceID, limit)
	if err != nil {
		internalError(s.log, w, r, err)
		return
	}
	counts, err := s.alerts.OpenCounts(r.Context())
	if err != nil {
		internalError(s.log, w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": list, "open": counts})
}

func (s *srv) ackAlert(w http.ResponseWriter, r *http.Request) {
	id, ok := PathID(w, r, "id")
	if !ok {
		return
	}
	sess, _ := sessionFrom(r.Context())
	if err := s.alerts.Ack(r.Context(), id, sess.User.Username); err != nil {
		s.serviceError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *srv) listRules(w http.ResponseWriter, r *http.Request) {
	list, err := s.alerts.ListRules(r.Context())
	if err != nil {
		internalError(s.log, w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": list})
}

func (s *srv) saveRule(w http.ResponseWriter, r *http.Request) {
	var rule alerts.Rule
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
	id, err := s.alerts.SaveRule(r.Context(), rule)
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	sess, _ := sessionFrom(r.Context())
	s.audit(r, sess, "alert_rule.save", "alert_rule", strconv.FormatInt(id, 10), map[string]any{"name": rule.Name, "kind": rule.Kind})
	writeJSON(w, http.StatusOK, map[string]any{"id": id})
}

func (s *srv) deleteRule(w http.ResponseWriter, r *http.Request) {
	id, ok := PathID(w, r, "id")
	if !ok {
		return
	}
	if err := s.alerts.DeleteRule(r.Context(), id); err != nil {
		s.serviceError(w, r, err)
		return
	}
	sess, _ := sessionFrom(r.Context())
	s.audit(r, sess, "alert_rule.delete", "alert_rule", strconv.FormatInt(id, 10), nil)
	w.WriteHeader(http.StatusNoContent)
}

func (s *srv) listChannels(w http.ResponseWriter, r *http.Request) {
	list, err := s.alerts.ListChannels(r.Context())
	if err != nil {
		internalError(s.log, w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": list})
}

func (s *srv) saveChannel(w http.ResponseWriter, r *http.Request) {
	var c alerts.Channel
	if !decodeJSON(w, r, &c) {
		return
	}
	c.ID = 0
	if r.Method == http.MethodPut {
		id, ok := PathID(w, r, "id")
		if !ok {
			return
		}
		c.ID = id
	}
	id, err := s.alerts.SaveChannel(r.Context(), c)
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	sess, _ := sessionFrom(r.Context())
	s.audit(r, sess, "channel.save", "channel", strconv.FormatInt(id, 10), map[string]any{"name": c.Name, "kind": c.Kind})
	writeJSON(w, http.StatusOK, map[string]any{"id": id})
}

func (s *srv) deleteChannel(w http.ResponseWriter, r *http.Request) {
	id, ok := PathID(w, r, "id")
	if !ok {
		return
	}
	if err := s.alerts.DeleteChannel(r.Context(), id); err != nil {
		s.serviceError(w, r, err)
		return
	}
	sess, _ := sessionFrom(r.Context())
	s.audit(r, sess, "channel.delete", "channel", strconv.FormatInt(id, 10), nil)
	w.WriteHeader(http.StatusNoContent)
}

func (s *srv) testChannel(w http.ResponseWriter, r *http.Request) {
	id, ok := PathID(w, r, "id")
	if !ok {
		return
	}
	if err := s.alerts.TestChannel(r.Context(), id); err != nil {
		if errors.Is(err, alerts.ErrNotFound) {
			s.serviceError(w, r, err)
			return
		}
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
