// Package auth handles users, password and TOTP login, sessions and roles.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/farshadfighter/taktaplus/internal/secretbox"
)

// Errors returned to handlers. Messages are safe to show to users.
var (
	ErrInvalidCredentials = errors.New("invalid username or password")
	ErrLocked             = errors.New("account temporarily locked; try again later")
	ErrInvalidCode        = errors.New("invalid verification code")
	ErrNoSession          = errors.New("not signed in")
	ErrUsernameTaken      = errors.New("username is already in use")
	ErrMFASetupState      = errors.New("two-factor authentication is not in setup state")
)

const (
	maxFailures  = 5
	lockDuration = 15 * time.Minute
	idleTimeout  = 30 * time.Minute
	sessionTTL   = 12 * time.Hour
	totpIssuer   = "TaktaPlus"
)

// User is an authenticated principal.
type User struct {
	ID          int64
	Username    string
	Name        string
	Email       string
	Role        Role
	TOTPEnabled bool
}

// Session is a signed-in browser session.
type Session struct {
	ID        string
	User      User
	MFAPassed bool
	ExpiresAt time.Time
}

// Service implements authentication against PostgreSQL.
type Service struct {
	db  *pgxpool.Pool
	box *secretbox.Box
	now func() time.Time
}

// NewService returns an auth service; box encrypts TOTP secrets.
func NewService(db *pgxpool.Pool, box *secretbox.Box) *Service {
	return &Service{db: db, box: box, now: time.Now}
}

func normalizeUsername(u string) string { return strings.ToLower(strings.TrimSpace(u)) }

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func totpContext(userID int64) string { return "totp:" + strconv.FormatInt(userID, 10) }

// CreateUser adds a local user.
func (s *Service) CreateUser(ctx context.Context, username, name, email string, role Role, password string) (User, error) {
	if !role.Valid() {
		return User{}, fmt.Errorf("unknown role %q", role)
	}
	username = normalizeUsername(username)
	if username == "" || strings.ContainsAny(username, " \t@") {
		return User{}, errors.New("invalid username")
	}
	hash, err := HashPassword(password)
	if err != nil {
		return User{}, err
	}
	u := User{Username: username, Name: strings.TrimSpace(name), Email: strings.TrimSpace(email), Role: role}
	err = s.db.QueryRow(ctx, `
		INSERT INTO users (username, name, email, role, password_hash)
		VALUES ($1, $2, $3, $4, $5) RETURNING id`,
		username, u.Name, u.Email, string(role), hash).Scan(&u.ID)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return User{}, ErrUsernameTaken
	}
	return u, err
}

type userRow struct {
	User
	hash        string
	active      bool
	failed      int
	lockedUntil *time.Time
	totpSecret  []byte
}

const userColumns = `u.id, u.username, u.name, u.email, u.role, u.totp_enabled,
	u.password_hash, u.active, u.failed_logins, u.locked_until, u.totp_secret`

func scanUser(row pgx.Row) (userRow, error) {
	var r userRow
	var role string
	err := row.Scan(&r.ID, &r.Username, &r.Name, &r.Email, &role, &r.TOTPEnabled,
		&r.hash, &r.active, &r.failed, &r.lockedUntil, &r.totpSecret)
	r.Role = Role(role)
	return r, err
}

func (s *Service) recordFailure(ctx context.Context, userID int64, failed int) error {
	failed++
	var locked *time.Time
	if failed >= maxFailures {
		t := s.now().Add(lockDuration)
		locked, failed = &t, 0
	}
	_, err := s.db.Exec(ctx, `UPDATE users SET failed_logins = $2, locked_until = $3 WHERE id = $1`, userID, failed, locked)
	return err
}

// Login checks the password and opens a session. The session still needs
// MFA (VerifyMFA or EnableTOTP) before it grants access.
func (s *Service) Login(ctx context.Context, username, password, ip, userAgent string) (string, Session, error) {
	r, err := scanUser(s.db.QueryRow(ctx,
		`SELECT `+userColumns+` FROM users u WHERE lower(u.username) = $1`, normalizeUsername(username)))
	if errors.Is(err, pgx.ErrNoRows) {
		// Spend comparable time so response timing does not reveal accounts.
		_, _ = CheckPassword(dummyHash, password)
		return "", Session{}, ErrInvalidCredentials
	}
	if err != nil {
		return "", Session{}, err
	}
	if !r.active {
		return "", Session{}, ErrInvalidCredentials
	}
	if r.lockedUntil != nil && s.now().Before(*r.lockedUntil) {
		return "", Session{}, ErrLocked
	}
	ok, err := CheckPassword(r.hash, password)
	if err != nil {
		return "", Session{}, err
	}
	if !ok {
		if err := s.recordFailure(ctx, r.ID, r.failed); err != nil {
			return "", Session{}, err
		}
		return "", Session{}, ErrInvalidCredentials
	}

	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", Session{}, err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	sess := Session{ID: hashToken(token), User: r.User, ExpiresAt: s.now().Add(sessionTTL)}
	if _, err := s.db.Exec(ctx, `
		INSERT INTO sessions (id, user_id, ip, user_agent, created_at, last_seen_at, expires_at)
		VALUES ($1, $2, $3, $4, $5, $5, $6)`,
		sess.ID, r.ID, ip, userAgent, s.now(), sess.ExpiresAt); err != nil {
		return "", Session{}, err
	}
	return token, sess, nil
}

// Authenticate resolves a session token, enforcing idle and absolute expiry.
func (s *Service) Authenticate(ctx context.Context, token string) (Session, error) {
	if token == "" {
		return Session{}, ErrNoSession
	}
	id := hashToken(token)
	var sess Session
	var lastSeen time.Time
	var role string
	err := s.db.QueryRow(ctx, `
		SELECT s.id, s.mfa_passed, s.expires_at, s.last_seen_at,
		       u.id, u.username, u.name, u.email, u.role, u.totp_enabled
		FROM sessions s JOIN users u ON u.id = s.user_id
		WHERE s.id = $1 AND u.active`, id).Scan(
		&sess.ID, &sess.MFAPassed, &sess.ExpiresAt, &lastSeen,
		&sess.User.ID, &sess.User.Username, &sess.User.Name, &sess.User.Email, &role, &sess.User.TOTPEnabled)
	if errors.Is(err, pgx.ErrNoRows) {
		return Session{}, ErrNoSession
	}
	if err != nil {
		return Session{}, err
	}
	sess.User.Role = Role(role)
	now := s.now()
	if now.After(sess.ExpiresAt) || now.Sub(lastSeen) > idleTimeout {
		_, _ = s.db.Exec(ctx, `DELETE FROM sessions WHERE id = $1`, id)
		return Session{}, ErrNoSession
	}
	if _, err := s.db.Exec(ctx, `UPDATE sessions SET last_seen_at = $2 WHERE id = $1`, id, now); err != nil {
		return Session{}, err
	}
	return sess, nil
}

// Logout ends the session.
func (s *Service) Logout(ctx context.Context, sessionID string) error {
	_, err := s.db.Exec(ctx, `DELETE FROM sessions WHERE id = $1`, sessionID)
	return err
}

// BeginTOTPSetup creates a new TOTP secret for a user who has none enabled
// and returns its otpauth URI and text form.
func (s *Service) BeginTOTPSetup(ctx context.Context, sess Session) (uri, secretText string, err error) {
	if sess.User.TOTPEnabled {
		return "", "", ErrMFASetupState
	}
	secret := NewTOTPSecret()
	sealed := s.box.Seal(secret, totpContext(sess.User.ID))
	if _, err := s.db.Exec(ctx, `UPDATE users SET totp_secret = $2 WHERE id = $1 AND NOT totp_enabled`, sess.User.ID, sealed); err != nil {
		return "", "", err
	}
	return TOTPURI(totpIssuer, sess.User.Username, secret), TOTPSecretText(secret), nil
}

func (s *Service) checkCode(ctx context.Context, userID int64, code string) (userRow, error) {
	r, err := scanUser(s.db.QueryRow(ctx,
		`SELECT `+userColumns+` FROM users u WHERE u.id = $1`, userID))
	if err != nil {
		return r, err
	}
	if r.lockedUntil != nil && s.now().Before(*r.lockedUntil) {
		return r, ErrLocked
	}
	if r.totpSecret == nil {
		return r, ErrMFASetupState
	}
	secret, err := s.box.Open(r.totpSecret, totpContext(userID))
	if err != nil {
		return r, err
	}
	if !VerifyTOTP(secret, strings.TrimSpace(code), s.now()) {
		if err := s.recordFailure(ctx, userID, r.failed); err != nil {
			return r, err
		}
		return r, ErrInvalidCode
	}
	return r, nil
}

func (s *Service) completeLogin(ctx context.Context, sess Session) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `UPDATE sessions SET mfa_passed = true WHERE id = $1`, sess.ID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE users SET failed_logins = 0, locked_until = NULL, last_login_at = $2 WHERE id = $1`, sess.User.ID, s.now()); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// EnableTOTP confirms the setup with a first code and completes the login.
func (s *Service) EnableTOTP(ctx context.Context, sess Session, code string) error {
	if sess.User.TOTPEnabled {
		return ErrMFASetupState
	}
	if _, err := s.checkCode(ctx, sess.User.ID, code); err != nil {
		return err
	}
	if _, err := s.db.Exec(ctx, `UPDATE users SET totp_enabled = true WHERE id = $1`, sess.User.ID); err != nil {
		return err
	}
	return s.completeLogin(ctx, sess)
}

// VerifyMFA checks the TOTP code of a user who has it enabled.
func (s *Service) VerifyMFA(ctx context.Context, sess Session, code string) error {
	if !sess.User.TOTPEnabled {
		return ErrMFASetupState
	}
	if _, err := s.checkCode(ctx, sess.User.ID, code); err != nil {
		return err
	}
	return s.completeLogin(ctx, sess)
}

// dummyHash is a valid hash of a random password, used to equalize timing.
var dummyHash = func() string {
	h, err := HashPassword(base64.RawURLEncoding.EncodeToString(NewTOTPSecret()))
	if err != nil {
		panic(err)
	}
	return h
}()
