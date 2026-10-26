package api

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/farshadfighter/taktaplus-lic/pkg/licformat"

	"github.com/farshadfighter/taktaplus/internal/auth"
	"github.com/farshadfighter/taktaplus/internal/license"
)

// PathID parses a positive integer URL parameter, answering 404 otherwise.
func PathID(w http.ResponseWriter, r *http.Request, name string) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, name), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusNotFound, "not found")
		return 0, false
	}
	return id, true
}

// --- users ---

func (s *srv) listUsers(w http.ResponseWriter, r *http.Request) {
	list, err := s.auth.ListUsers(r.Context())
	if err != nil {
		internalError(s.log, w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": list})
}

func (s *srv) createUser(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string    `json:"username"`
		Name     string    `json:"name"`
		Email    string    `json:"email"`
		Role     auth.Role `json:"role"`
		Password string    `json:"password"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	u, err := s.auth.CreateUser(r.Context(), req.Username, req.Name, req.Email, req.Role, req.Password)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	sess, _ := sessionFrom(r.Context())
	s.audit(r, sess, "user.create", "user", u.Username, map[string]any{"role": u.Role})
	writeJSON(w, http.StatusCreated, toUserJSON(u))
}

func (s *srv) userResult(w http.ResponseWriter, r *http.Request, err error, action string, id int64) {
	switch {
	case errors.Is(err, auth.ErrUserNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case err != nil:
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		sess, _ := sessionFrom(r.Context())
		s.audit(r, sess, action, "user", strconv.FormatInt(id, 10), nil)
		w.WriteHeader(http.StatusNoContent)
	}
}

func (s *srv) updateUser(w http.ResponseWriter, r *http.Request) {
	id, ok := PathID(w, r, "id")
	if !ok {
		return
	}
	var req auth.UserUpdate
	if !decodeJSON(w, r, &req) {
		return
	}
	sess, _ := sessionFrom(r.Context())
	s.userResult(w, r, s.auth.UpdateUser(r.Context(), sess.User.ID, id, req), "user.update", id)
}

func (s *srv) setPassword(w http.ResponseWriter, r *http.Request) {
	id, ok := PathID(w, r, "id")
	if !ok {
		return
	}
	var req struct {
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	s.userResult(w, r, s.auth.SetPassword(r.Context(), id, req.Password), "user.password", id)
}

func (s *srv) resetMFA(w http.ResponseWriter, r *http.Request) {
	id, ok := PathID(w, r, "id")
	if !ok {
		return
	}
	s.userResult(w, r, s.auth.ResetMFA(r.Context(), id), "user.reset_mfa", id)
}

func (s *srv) unlockUser(w http.ResponseWriter, r *http.Request) {
	id, ok := PathID(w, r, "id")
	if !ok {
		return
	}
	s.userResult(w, r, s.auth.Unlock(r.Context(), id), "user.unlock", id)
}

// --- license ---

type itemJSON struct {
	Kind     string `json:"kind"`
	Serial   string `json:"serial,omitempty"`
	Platform string `json:"platform,omitempty"`
	Users    int    `json:"users,omitempty"`
	Expires  string `json:"expires"`
	Status   string `json:"status"`
}

func (s *srv) getLicense(w http.ResponseWriter, r *http.Request) {
	g := s.lic.Guard()
	state, err := s.lic.State(r.Context())
	if err != nil {
		internalError(s.log, w, r, err)
		return
	}
	out := map[string]any{
		"installation_id":   g.Identity().ID(),
		"online_configured": s.lic.OnlineConfigured(),
		"clock_rolled_back": g.ClockRolledBack(),
		"state":             state,
		"license":           nil,
	}
	if lic, err := g.License(); err == nil {
		now := g.Now()
		items := []itemJSON{}
		for _, d := range lic.Devices {
			items = append(items, itemJSON{Kind: "device", Serial: d.Serial, Platform: string(d.Platform), Expires: d.Expires.String(), Status: string(lic.DeviceStatus(d.Serial, now))})
		}
		for _, o := range lic.OTP {
			st := licformat.StatusExpired
			if now.Before(o.Expires.EndOfDay()) {
				st = licformat.StatusActive
			} else if now.Before(o.Expires.EndOfDay().AddDate(0, 0, lic.GraceDays)) {
				st = licformat.StatusGrace
			}
			items = append(items, itemJSON{Kind: "otp", Users: o.Users, Expires: o.Expires.String(), Status: string(st)})
		}
		out["license"] = map[string]any{
			"license_id": lic.LicenseID, "customer": lic.Customer, "issued_at": lic.IssuedAt, "not_after": lic.NotAfter,
			"grace_days": lic.GraceDays, "online": lic.Online != nil, "items": items, "otp_capacity": lic.OTPCapacity(now), "kid": lic.KeyID,
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *srv) licenseError(w http.ResponseWriter, r *http.Request, err error) {
	var se license.ServerError
	switch {
	case errors.As(err, &se):
		writeError(w, http.StatusBadGateway, "license server: "+se.Message)
	case errors.Is(err, license.ErrNoServer), errors.Is(err, license.ErrNoLicense):
		writeError(w, http.StatusConflict, err.Error())
	default:
		writeError(w, http.StatusBadRequest, err.Error())
	}
}

func (s *srv) activateLicense(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Key string `json:"key"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	lic, err := s.lic.ActivateOnline(r.Context(), req.Key)
	if err != nil {
		s.licenseError(w, r, err)
		return
	}
	sess, _ := sessionFrom(r.Context())
	s.audit(r, sess, "license.activate", "license", lic.LicenseID, map[string]any{"method": "online"})
	s.getLicense(w, r)
}

func (s *srv) licenseRequest(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Key string `json:"key"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	token, err := s.lic.OfflineRequest(r.Context(), req.Key)
	if err != nil {
		s.licenseError(w, r, err)
		return
	}
	sess, _ := sessionFrom(r.Context())
	s.audit(r, sess, "license.request", "license", "", nil)
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="request-`+time.Now().UTC().Format("20060102")+`.tpreq"`)
	_, _ = w.Write([]byte(token + "\n"))
}

func (s *srv) uploadLicense(w http.ResponseWriter, r *http.Request) {
	var req struct {
		License string `json:"license"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	lic, err := s.lic.Install(r.Context(), req.License)
	if err != nil {
		s.licenseError(w, r, err)
		return
	}
	sess, _ := sessionFrom(r.Context())
	s.audit(r, sess, "license.upload", "license", lic.LicenseID, nil)
	s.getLicense(w, r)
}

func (s *srv) licenseHeartbeat(w http.ResponseWriter, r *http.Request) {
	if _, err := s.lic.Heartbeat(r.Context()); err != nil {
		s.licenseError(w, r, err)
		return
	}
	s.getLicense(w, r)
}
