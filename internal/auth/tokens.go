package auth

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/farshadfighter/taktaplus-lic/pkg/apitoken"
)

// Token is an API token as listed in the panel; the secret is never kept.
type Token struct {
	ID          int64      `json:"id"`
	TokenID     string     `json:"token_id"`
	Name        string     `json:"name"`
	Role        Role       `json:"role"`
	ReadOnly    bool       `json:"read_only"`
	AllowedNets []string   `json:"allowed_nets"`
	ExpiresAt   *time.Time `json:"expires_at"`
	CreatedBy   string     `json:"created_by"`
	CreatedAt   time.Time  `json:"created_at"`
	LastUsedAt  *time.Time `json:"last_used_at"`
	LastIP      string     `json:"last_ip"`
	RevokedAt   *time.Time `json:"revoked_at"`
}

// TokenInput creates a token.
type TokenInput struct {
	Name        string   `json:"name"`
	Role        Role     `json:"role"`
	ReadOnly    bool     `json:"read_only"`
	AllowedNets []string `json:"allowed_nets"`
	Days        int      `json:"days"` // 0 = never expires
}

// TokenSession marks a session that came from an API token.
type TokenSession struct {
	ID       int64
	Name     string
	ReadOnly bool
}

// ErrTokenNotFound means the token does not exist or is already revoked.
var ErrTokenNotFound = errors.New("token not found")

// InputError is a validation failure to show to the user.
type InputError struct{ msg string }

func (e InputError) Error() string { return e.msg }

// Covers reports whether r grants every permission of other, so a user can
// only give a token a role no stronger than their own.
func (r Role) Covers(other Role) bool {
	for _, p := range rolePerms[other] {
		if !r.Can(p) {
			return false
		}
	}
	return true
}

// CreateToken stores a new token for creator and returns it with its value,
// which is shown once.
func (s *Service) CreateToken(ctx context.Context, creator User, in TokenInput) (*Token, string, error) {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len([]rune(in.Name)) > 80 {
		return nil, "", InputError{"the token needs a name of up to 80 characters"}
	}
	if !in.Role.Valid() {
		return nil, "", InputError{"unknown role"}
	}
	if !creator.Role.Covers(in.Role) {
		return nil, "", InputError{"a token cannot have more access than your own role"}
	}
	nets, err := apitoken.NormalizeNets(in.AllowedNets)
	if err != nil {
		return nil, "", InputError{err.Error()}
	}
	exp, err := apitoken.Expiry(in.Days, s.now())
	if err != nil {
		return nil, "", InputError{err.Error()}
	}
	tid, value, hash, err := apitoken.New()
	if err != nil {
		return nil, "", err
	}
	t := &Token{TokenID: tid, Name: in.Name, Role: in.Role, ReadOnly: in.ReadOnly, AllowedNets: nets, ExpiresAt: exp, CreatedBy: creator.Username}
	err = s.db.QueryRow(ctx, `INSERT INTO api_tokens (token_id, secret_hash, name, role, read_only, allowed_nets, expires_at, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING id, created_at`,
		tid, hash, t.Name, string(t.Role), t.ReadOnly, nets, exp, creator.ID).Scan(&t.ID, &t.CreatedAt)
	if err != nil {
		return nil, "", err
	}
	return t, value, nil
}

// ListTokens lists all tokens, newest first.
func (s *Service) ListTokens(ctx context.Context) ([]Token, error) {
	rows, err := s.db.Query(ctx, `SELECT t.id, t.token_id, t.name, t.role, t.read_only, t.allowed_nets, t.expires_at, u.username,
		t.created_at, t.last_used_at, t.last_ip, t.revoked_at
		FROM api_tokens t JOIN users u ON u.id = t.created_by ORDER BY t.id DESC`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Token, error) {
		var t Token
		var role string
		err := r.Scan(&t.ID, &t.TokenID, &t.Name, &role, &t.ReadOnly, &t.AllowedNets, &t.ExpiresAt, &t.CreatedBy,
			&t.CreatedAt, &t.LastUsedAt, &t.LastIP, &t.RevokedAt)
		t.Role = Role(role)
		return t, err
	})
}

// RevokeToken stops a token at once.
func (s *Service) RevokeToken(ctx context.Context, id int64) (string, error) {
	var name string
	err := s.db.QueryRow(ctx, `UPDATE api_tokens SET revoked_at = now() WHERE id = $1 AND revoked_at IS NULL RETURNING name`, id).Scan(&name)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrTokenNotFound
	}
	return name, err
}

// AuthenticateToken turns a bearer token into a session acting as the
// token's creator with the token's role. It fails the same way for every
// reason so callers learn nothing about tokens they do not hold.
func (s *Service) AuthenticateToken(ctx context.Context, value, ip string) (Session, error) {
	tid, secret, err := apitoken.Parse(value)
	if err != nil {
		return Session{}, apitoken.ErrInvalid
	}
	var (
		id        int64
		hash      []byte
		name      string
		role      string
		readOnly  bool
		nets      []string
		expires   *time.Time
		revoked   *time.Time
		lastUsed  *time.Time
		user      User
		userRole  string
		userValid bool
	)
	err = s.db.QueryRow(ctx, `SELECT t.id, t.secret_hash, t.name, t.role, t.read_only, t.allowed_nets, t.expires_at, t.revoked_at, t.last_used_at,
		u.id, u.username, u.name, u.email, u.role, u.active
		FROM api_tokens t JOIN users u ON u.id = t.created_by WHERE t.token_id = $1`, tid).Scan(
		&id, &hash, &name, &role, &readOnly, &nets, &expires, &revoked, &lastUsed,
		&user.ID, &user.Username, &user.Name, &user.Email, &userRole, &userValid)
	if errors.Is(err, pgx.ErrNoRows) {
		apitoken.Match(secret, make([]byte, 32)) // comparable time
		return Session{}, apitoken.ErrInvalid
	}
	if err != nil {
		return Session{}, err
	}
	now := s.now()
	if !apitoken.Match(secret, hash) || revoked != nil || (expires != nil && now.After(*expires)) || !userValid || !apitoken.Allowed(nets, ip) {
		return Session{}, apitoken.ErrInvalid
	}
	// The token never has more than its creator has now.
	tokenRole := Role(role)
	if !Role(userRole).Covers(tokenRole) {
		return Session{}, apitoken.ErrInvalid
	}
	if lastUsed == nil || now.Sub(*lastUsed) > time.Minute {
		_, _ = s.db.Exec(ctx, `UPDATE api_tokens SET last_used_at = $2, last_ip = $3 WHERE id = $1`, id, now, ip)
	}
	user.Role = tokenRole
	user.Username = fmt.Sprintf("%s (token: %s)", user.Username, name)
	return Session{ID: "token:" + tid, User: user, MFAPassed: true, Token: &TokenSession{ID: id, Name: name, ReadOnly: readOnly}}, nil
}

// TokenRoles are the roles a user may give a token.
func (r Role) TokenRoles() []Role {
	var out []Role
	for _, x := range []Role{RoleSuperAdmin, RoleAdmin, RoleApprover, RoleOperator, RoleViewer} {
		if r.Covers(x) {
			out = append(out, x)
		}
	}
	return slices.Clip(out)
}
