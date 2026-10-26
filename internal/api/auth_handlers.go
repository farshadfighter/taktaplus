package api

import (
	"errors"
	"net/http"

	"github.com/farshadfighter/taktaplus/internal/audit"
	"github.com/farshadfighter/taktaplus/internal/auth"
)

type userJSON struct {
	ID          int64       `json:"id"`
	Username    string      `json:"username"`
	Name        string      `json:"name"`
	Email       string      `json:"email"`
	Role        string      `json:"role"`
	Perms       []auth.Perm `json:"perms"`
	TOTPEnabled bool        `json:"totp_enabled"`
}

func toUserJSON(u auth.User) userJSON {
	return userJSON{ID: u.ID, Username: u.Username, Name: u.Name, Email: u.Email, Role: string(u.Role), Perms: u.Role.Perms(), TOTPEnabled: u.TOTPEnabled}
}

// nextStep tells the UI what the login flow needs next.
func nextStep(sess auth.Session) string {
	switch {
	case sess.MFAPassed:
		return "done"
	case sess.User.TOTPEnabled:
		return "mfa"
	default:
		return "mfa_setup"
	}
}

func (s *srv) audit(r *http.Request, sess auth.Session, action, objType, objID string, details map[string]any) {
	id := sess.User.ID
	err := audit.Log(r.Context(), s.db, audit.Entry{
		Source: audit.SourcePanel, ActorID: &id, Actor: sess.User.Username, IP: clientIP(r),
		Action: action, ObjectType: objType, ObjectID: objID, Details: details,
	})
	if err != nil {
		s.log.Error("audit log failed", "action", action, "err", err)
	}
}

func (s *srv) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	token, sess, err := s.auth.Login(r.Context(), req.Username, req.Password, clientIP(r), r.UserAgent())
	if errors.Is(err, auth.ErrInvalidCredentials) || errors.Is(err, auth.ErrLocked) {
		_ = audit.Log(r.Context(), s.db, audit.Entry{Source: audit.SourcePanel, Actor: req.Username, IP: clientIP(r), Action: "login.failed", Details: map[string]any{"reason": err.Error()}})
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}
	if err != nil {
		internalError(s.log, w, r, err)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: token, Path: "/", HttpOnly: true, Secure: s.cookieSecure,
		SameSite: http.SameSiteStrictMode, Expires: sess.ExpiresAt,
	})
	writeJSON(w, http.StatusOK, map[string]any{"next": nextStep(sess), "user": toUserJSON(sess.User)})
}

// pendingSession returns a session that passed the password step.
func pendingSession(w http.ResponseWriter, r *http.Request) (auth.Session, bool) {
	sess, ok := sessionFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "not signed in")
	}
	return sess, ok
}

func (s *srv) mfaResult(w http.ResponseWriter, r *http.Request, sess auth.Session, err error) {
	switch {
	case errors.Is(err, auth.ErrInvalidCode), errors.Is(err, auth.ErrLocked), errors.Is(err, auth.ErrMFASetupState):
		writeError(w, http.StatusUnauthorized, err.Error())
	case err != nil:
		internalError(s.log, w, r, err)
	default:
		s.audit(r, sess, "login", "user", "", nil)
		writeJSON(w, http.StatusOK, map[string]any{"next": "done"})
	}
}

func (s *srv) handleMFA(w http.ResponseWriter, r *http.Request) {
	sess, ok := pendingSession(w, r)
	if !ok {
		return
	}
	var req struct {
		Code string `json:"code"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	s.mfaResult(w, r, sess, s.auth.VerifyMFA(r.Context(), sess, req.Code))
}

func (s *srv) handleMFASetup(w http.ResponseWriter, r *http.Request) {
	sess, ok := pendingSession(w, r)
	if !ok {
		return
	}
	uri, secret, err := s.auth.BeginTOTPSetup(r.Context(), sess)
	if errors.Is(err, auth.ErrMFASetupState) {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	if err != nil {
		internalError(s.log, w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"uri": uri, "secret": secret})
}

func (s *srv) handleMFAEnable(w http.ResponseWriter, r *http.Request) {
	sess, ok := pendingSession(w, r)
	if !ok {
		return
	}
	var req struct {
		Code string `json:"code"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	s.mfaResult(w, r, sess, s.auth.EnableTOTP(r.Context(), sess, req.Code))
}

func (s *srv) handleLogout(w http.ResponseWriter, r *http.Request) {
	if sess, ok := sessionFrom(r.Context()); ok {
		if err := s.auth.Logout(r.Context(), sess.ID); err != nil {
			internalError(s.log, w, r, err)
			return
		}
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: s.cookieSecure, SameSite: http.SameSiteStrictMode})
	w.WriteHeader(http.StatusNoContent)
}

func (s *srv) handleMe(w http.ResponseWriter, r *http.Request) {
	sess, _ := sessionFrom(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{"user": toUserJSON(sess.User)})
}

func (s *srv) handleAuditList(w http.ResponseWriter, r *http.Request) {
	entries, err := audit.List(r.Context(), s.db, 200)
	if err != nil {
		internalError(s.log, w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": entries})
}
