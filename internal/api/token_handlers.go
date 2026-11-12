package api

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/farshadfighter/taktaplus/internal/auth"
)

func (s *srv) listTokens(w http.ResponseWriter, r *http.Request) {
	list, err := s.auth.ListTokens(r.Context())
	if err != nil {
		internalError(s.log, w, r, err)
		return
	}
	sess, _ := sessionFrom(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{"items": list, "roles": sess.User.Role.TokenRoles()})
}

func (s *srv) createToken(w http.ResponseWriter, r *http.Request) {
	var in struct {
		auth.TokenInput
		Password string `json:"password"`
		Code     string `json:"code"`
	}
	if !decodeJSON(w, r, &in) || !s.reauth(w, r, in.Password, in.Code) {
		return
	}
	sess, _ := sessionFrom(r.Context())
	t, value, err := s.auth.CreateToken(r.Context(), sess.User, in.TokenInput)
	var ie auth.InputError
	if errors.As(err, &ie) {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	} else if err != nil {
		internalError(s.log, w, r, err)
		return
	}
	s.audit(r, sess, "api_token.create", "api_token", strconv.FormatInt(t.ID, 10), map[string]any{
		"name": t.Name, "role": t.Role, "read_only": t.ReadOnly, "allowed_nets": t.AllowedNets, "expires_at": t.ExpiresAt})
	writeJSON(w, http.StatusCreated, map[string]any{"token": t, "value": value})
}

func (s *srv) revokeToken(w http.ResponseWriter, r *http.Request) {
	id, ok := PathID(w, r, "id")
	if !ok {
		return
	}
	name, err := s.auth.RevokeToken(r.Context(), id)
	if errors.Is(err, auth.ErrTokenNotFound) {
		writeError(w, http.StatusNotFound, "not found")
		return
	} else if err != nil {
		internalError(s.log, w, r, err)
		return
	}
	sess, _ := sessionFrom(r.Context())
	s.audit(r, sess, "api_token.revoke", "api_token", strconv.FormatInt(id, 10), map[string]any{"name": name})
	w.WriteHeader(http.StatusNoContent)
}
