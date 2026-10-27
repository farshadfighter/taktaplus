package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/farshadfighter/taktaplus/internal/otp"
	"github.com/farshadfighter/taktaplus/internal/radiussrv"
	"github.com/farshadfighter/taktaplus/internal/sms"
	"github.com/farshadfighter/taktaplus/internal/smtpgw"
)

func (s *srv) otpError(w http.ResponseWriter, r *http.Request, err error) {
	var ie otp.InputError
	var ge smtpgw.InputError
	switch {
	case errors.As(err, &ie), errors.As(err, &ge), radiussrv.IsInput(err):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, otp.ErrCapacity), errors.Is(err, otp.ErrRateLimited), errors.Is(err, sms.ErrNotConfigured):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, otp.ErrNotFound):
		writeError(w, http.StatusNotFound, "not found")
	default:
		internalError(s.log, w, r, err)
	}
}

func (s *srv) otpOverview(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	used, err := s.otp.Usage(ctx)
	if err != nil {
		internalError(s.log, w, r, err)
		return
	}
	policy, _ := s.otp.Policy(ctx)
	smsCfg, _ := s.sms.Config(ctx)
	ldapCfg, _ := s.otp.LDAP(ctx)
	radiusCfg, _ := s.radius.Config(ctx)
	gwCfg, _ := s.smtpgw.Config(ctx)
	stats, _ := s.sms.Stats(ctx)
	writeJSON(w, http.StatusOK, map[string]any{
		"capacity": s.otp.Capacity(), "used": used, "policy": policy, "sms": smsCfg.Redacted(), "sms_configured": smsCfg.Configured(),
		"ldap": ldapCfg.Redacted(), "radius": radiusCfg, "gateway": gwCfg.Redacted(), "stats": stats,
		"listen": map[string]string{"radius": s.radiusAddr, "smtp": s.smtpAddr},
	})
}

func (s *srv) listOTPUsers(w http.ResponseWriter, r *http.Request) {
	list, err := s.otp.List(r.Context())
	if err != nil {
		internalError(s.log, w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": list})
}

func (s *srv) saveOTPUser(w http.ResponseWriter, r *http.Request) {
	var in otp.UserInput
	if !decodeJSON(w, r, &in) {
		return
	}
	sess, _ := sessionFrom(r.Context())
	if r.Method == http.MethodPut {
		id, ok := PathID(w, r, "id")
		if !ok {
			return
		}
		if err := s.otp.Update(r.Context(), id, in); err != nil {
			s.otpError(w, r, err)
			return
		}
		s.audit(r, sess, "otp_user.update", "otp_user", strconv.FormatInt(id, 10), map[string]any{"username": in.Username, "active": in.Active})
		w.WriteHeader(http.StatusNoContent)
		return
	}
	u, err := s.otp.Create(r.Context(), in)
	if err != nil {
		s.otpError(w, r, err)
		return
	}
	s.audit(r, sess, "otp_user.create", "otp_user", strconv.FormatInt(u.ID, 10), map[string]any{"username": u.Username, "source": u.Source})
	writeJSON(w, http.StatusCreated, u)
}

func (s *srv) otpUserAction(do func(ctx context.Context, id int64) error, action string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := PathID(w, r, "id")
		if !ok {
			return
		}
		if err := do(r.Context(), id); err != nil {
			s.otpError(w, r, err)
			return
		}
		sess, _ := sessionFrom(r.Context())
		s.audit(r, sess, action, "otp_user", strconv.FormatInt(id, 10), nil)
		w.WriteHeader(http.StatusNoContent)
	}
}

// saveSetting decodes a settings body and stores it with save.
func saveSetting[T any](s *srv, name string, save func(ctx context.Context, v T) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var v T
		if !decodeJSON(w, r, &v) {
			return
		}
		if err := save(r.Context(), v); err != nil {
			s.otpError(w, r, err)
			return
		}
		sess, _ := sessionFrom(r.Context())
		s.audit(r, sess, "setting."+name, "setting", name, nil)
		w.WriteHeader(http.StatusNoContent)
	}
}

func (s *srv) smsTest(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Mobile string `json:"mobile"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	sess, _ := sessionFrom(r.Context())
	if err := s.sms.Send(r.Context(), req.Mobile, "123456", "test", sess.User.Username, nil); err != nil {
		if errors.Is(err, sms.ErrNotConfigured) {
			s.otpError(w, r, err)
			return
		}
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *srv) smsCredit(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	c, err := s.sms.Credit(ctx)
	if err != nil {
		if errors.Is(err, sms.ErrNotConfigured) {
			s.otpError(w, r, err)
			return
		}
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"credit": c})
}

func (s *srv) smsMessages(w http.ResponseWriter, r *http.Request) {
	list, err := s.sms.Messages(r.Context(), 300)
	if err != nil {
		internalError(s.log, w, r, err)
		return
	}
	events, err := s.otp.Events(r.Context(), 300)
	if err != nil {
		internalError(s.log, w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"messages": list, "events": events})
}

func (s *srv) directorySearch(w http.ResponseWriter, r *http.Request) {
	list, err := s.otp.SearchDirectory(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": list})
}

func (s *srv) directoryImport(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Usernames []string `json:"usernames"`
		Group     string   `json:"fortinet_group"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	n, err := s.otp.ImportDirectory(r.Context(), req.Usernames, req.Group)
	if err != nil {
		s.otpError(w, r, err)
		return
	}
	sess, _ := sessionFrom(r.Context())
	s.audit(r, sess, "otp_user.import", "otp_user", "", map[string]any{"count": n})
	writeJSON(w, http.StatusOK, map[string]any{"imported": n})
}

func (s *srv) radiusClients(w http.ResponseWriter, r *http.Request) {
	list, err := s.radius.ListClients(r.Context())
	if err != nil {
		internalError(s.log, w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": list})
}

func (s *srv) setRadiusClient(w http.ResponseWriter, r *http.Request) {
	id, ok := PathID(w, r, "id")
	if !ok {
		return
	}
	var req struct {
		Enabled  bool   `json:"enabled"`
		SourceIP string `json:"source_ip"`
		Rotate   bool   `json:"rotate"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	err := s.radius.SetClient(r.Context(), id, req.Enabled, req.SourceIP)
	if err == nil && req.Rotate {
		err = s.radius.RotateSecret(r.Context(), id)
	}
	if err != nil {
		s.otpError(w, r, err)
		return
	}
	sess, _ := sessionFrom(r.Context())
	s.audit(r, sess, "radius_client.set", "device", strconv.FormatInt(id, 10), map[string]any{"enabled": req.Enabled, "rotate": req.Rotate})
	w.WriteHeader(http.StatusNoContent)
}

// fortigateOTPConfig returns the CLI for RADIUS (?device=) or the gateway.
func (s *srv) fortigateOTPConfig(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	ip := q.Get("server")
	if ip == "" {
		writeError(w, http.StatusBadRequest, "server address is required")
		return
	}
	var text string
	var err error
	if q.Get("mode") == "gateway" {
		port, _ := strconv.Atoi(q.Get("port"))
		text, err = s.smtpgw.FortiGateConfig(r.Context(), ip, port)
	} else {
		id, _ := strconv.ParseInt(q.Get("device"), 10, 64)
		text, err = s.radius.FortiGateConfig(r.Context(), id, ip, q.Get("group"))
	}
	if err != nil {
		s.otpError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"cli": text})
}

func (s *srv) gatewayUsers(w http.ResponseWriter, r *http.Request) {
	list, err := s.smtpgw.SMSUsers(r.Context())
	if err != nil {
		internalError(s.log, w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": list})
}

func (s *srv) gatewaySync(w http.ResponseWriter, r *http.Request) {
	err := s.smtpgw.SyncUsers(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	s.gatewayUsers(w, r)
}
