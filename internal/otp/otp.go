// Package otp manages the users who sign in to FortiGate with an SMS code
// (module D): their passwords (local or LDAP/AD), the code policy, and the
// challenges the RADIUS server issues.
package otp

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/farshadfighter/taktaplus/internal/auth"
	"github.com/farshadfighter/taktaplus/internal/license"
	"github.com/farshadfighter/taktaplus/internal/settings"
	"github.com/farshadfighter/taktaplus/internal/sms"
)

// InputError is a validation failure to show to the user.
type InputError struct{ msg string }

func (e InputError) Error() string { return e.msg }

func invalidf(format string, a ...any) error { return InputError{fmt.Sprintf(format, a...)} }

var (
	// ErrNotFound means the user does not exist.
	ErrNotFound = errors.New("not found")
	// ErrRejected is a failed sign-in; the reason is logged, not shown.
	ErrRejected = errors.New("access rejected")
	// ErrCapacity means the license has no room for another two-factor user.
	ErrCapacity = errors.New("the license's two-factor user capacity is full")
)

// Policy controls codes and lockouts.
type Policy struct {
	Length         int `json:"length"`       // digits, 4–8
	TTLSeconds     int `json:"ttl_seconds"`  // code validity
	MaxAttempts    int `json:"max_attempts"` // wrong codes per challenge
	MaxSends       int `json:"max_sends"`    // codes per mobile per 10 minutes
	MaxFailures    int `json:"max_failures"` // failed sign-ins before lockout
	LockoutMinutes int `json:"lockout_minutes"`
}

// DefaultPolicy is used until one is saved.
var DefaultPolicy = Policy{Length: 6, TTLSeconds: 120, MaxAttempts: 3, MaxSends: 3, MaxFailures: 5, LockoutMinutes: 15}

func (p *Policy) validate() error {
	switch {
	case p.Length < 4 || p.Length > 8:
		return invalidf("code length must be 4 to 8 digits")
	case p.TTLSeconds < 30 || p.TTLSeconds > 900:
		return invalidf("code validity must be 30 to 900 seconds")
	case p.MaxAttempts < 1 || p.MaxAttempts > 10, p.MaxSends < 1 || p.MaxSends > 20, p.MaxFailures < 1 || p.MaxFailures > 50:
		return invalidf("limits are out of range")
	case p.LockoutMinutes < 1 || p.LockoutMinutes > 1440:
		return invalidf("lockout must be 1 to 1440 minutes")
	}
	return nil
}

// User is a two-factor user.
type User struct {
	ID            int64      `json:"id"`
	Username      string     `json:"username"`
	Name          string     `json:"name"`
	Mobile        string     `json:"mobile"`
	Source        string     `json:"source"` // local, ldap
	FortinetGroup string     `json:"fortinet_group"`
	Active        bool       `json:"active"`
	Locked        bool       `json:"locked"`
	LastLoginAt   *time.Time `json:"last_login_at"`
	CreatedAt     time.Time  `json:"created_at"`
	hash          string
	failed        int
	lockedUntil   *time.Time
}

// challenge is an outstanding SMS code.
type challenge struct {
	userID   int64
	codeHash [32]byte
	expires  time.Time
	attempts int
}

// Service manages users and challenges.
type Service struct {
	db       *pgxpool.Pool
	settings *settings.Store
	sms      *sms.Service
	guard    *license.Guard
	now      func() time.Time
	ldap     ldapDialer

	mu         sync.Mutex
	challenges map[string]*challenge
}

// New returns the service.
func New(db *pgxpool.Pool, st *settings.Store, s *sms.Service, guard *license.Guard) *Service {
	return &Service{db: db, settings: st, sms: s, guard: guard, now: time.Now, ldap: dialLDAP, challenges: map[string]*challenge{}}
}

// Policy returns the stored policy.
func (s *Service) Policy(ctx context.Context) (Policy, error) {
	p := DefaultPolicy
	err := s.settings.Get(ctx, "otp_policy", &p)
	return p, err
}

// SavePolicy stores the policy.
func (s *Service) SavePolicy(ctx context.Context, p Policy) error {
	if err := p.validate(); err != nil {
		return err
	}
	return s.settings.Set(ctx, "otp_policy", p)
}

// Usage counts two-factor users: active TaktaPlus users plus FortiGate local
// users with SMS two-factor (each username once).
func (s *Service) Usage(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM otp_users WHERE active) +
		(SELECT count(DISTINCT lower(username)) FROM fortigate_sms_users f
		 WHERE NOT EXISTS (SELECT 1 FROM otp_users u WHERE lower(u.username) = lower(f.username) AND u.active))`).Scan(&n)
	return n, err
}

// Capacity returns the license's two-factor user count.
func (s *Service) Capacity() int {
	if s.guard == nil {
		return 0
	}
	return s.guard.OTPCapacity()
}

func (s *Service) checkCapacity(ctx context.Context, adding int) error {
	used, err := s.Usage(ctx)
	if err != nil {
		return err
	}
	if used+adding > s.Capacity() {
		return ErrCapacity
	}
	return nil
}

const userCols = `id, username, name, mobile, source, fortinet_group, active, last_login_at, created_at, password_hash, failed, locked_until`

func (s *Service) scan(row pgx.Row) (*User, error) {
	var u User
	err := row.Scan(&u.ID, &u.Username, &u.Name, &u.Mobile, &u.Source, &u.FortinetGroup, &u.Active, &u.LastLoginAt, &u.CreatedAt,
		&u.hash, &u.failed, &u.lockedUntil)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	u.Locked = u.lockedUntil != nil && s.now().Before(*u.lockedUntil)
	return &u, err
}

// List returns users ordered by username.
func (s *Service) List(ctx context.Context) ([]*User, error) {
	rows, err := s.db.Query(ctx, `SELECT `+userCols+` FROM otp_users ORDER BY lower(username)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*User{}
	for rows.Next() {
		u, err := s.scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// Get returns one user.
func (s *Service) Get(ctx context.Context, id int64) (*User, error) {
	return s.scan(s.db.QueryRow(ctx, `SELECT `+userCols+` FROM otp_users WHERE id = $1`, id))
}

func (s *Service) byName(ctx context.Context, username string) (*User, error) {
	return s.scan(s.db.QueryRow(ctx, `SELECT `+userCols+` FROM otp_users WHERE lower(username) = lower($1)`, strings.TrimSpace(username)))
}

// UserInput creates or updates a user. Password is for local users; empty
// on update keeps the current one.
type UserInput struct {
	Username      string `json:"username"`
	Name          string `json:"name"`
	Mobile        string `json:"mobile"`
	Source        string `json:"source"`
	Password      string `json:"password"`
	FortinetGroup string `json:"fortinet_group"`
	Active        bool   `json:"active"`
}

func (in *UserInput) normalize() error {
	in.Username = strings.TrimSpace(in.Username)
	in.Name = strings.TrimSpace(in.Name)
	in.FortinetGroup = strings.TrimSpace(in.FortinetGroup)
	if in.Username == "" || len(in.Username) > 128 || strings.ContainsAny(in.Username, " \t") {
		return invalidf("user name is required and cannot contain spaces")
	}
	m, err := sms.NormalizeMobile(in.Mobile)
	if err != nil {
		return invalidf("mobile number is not valid")
	}
	in.Mobile = m
	if in.Source == "" {
		in.Source = "local"
	}
	if in.Source != "local" && in.Source != "ldap" {
		return invalidf("source must be local or ldap")
	}
	return nil
}

func mapErr(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return invalidf("a user with this name already exists")
	}
	return err
}

// Create adds a user.
func (s *Service) Create(ctx context.Context, in UserInput) (*User, error) {
	if err := in.normalize(); err != nil {
		return nil, err
	}
	hash := ""
	if in.Source == "local" {
		h, err := auth.HashPassword(in.Password)
		if err != nil {
			return nil, invalidf("%v", err)
		}
		hash = h
	}
	if in.Active {
		if err := s.checkCapacity(ctx, 1); err != nil {
			return nil, err
		}
	}
	var id int64
	err := s.db.QueryRow(ctx, `INSERT INTO otp_users (username, name, mobile, source, password_hash, fortinet_group, active)
		VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id`, in.Username, in.Name, in.Mobile, in.Source, hash, in.FortinetGroup, in.Active).Scan(&id)
	if err != nil {
		return nil, mapErr(err)
	}
	return s.Get(ctx, id)
}

// Update changes a user.
func (s *Service) Update(ctx context.Context, id int64, in UserInput) error {
	if err := in.normalize(); err != nil {
		return err
	}
	cur, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	hash := cur.hash
	if in.Source == "local" && in.Password != "" {
		if hash, err = auth.HashPassword(in.Password); err != nil {
			return invalidf("%v", err)
		}
	}
	if in.Source == "local" && hash == "" {
		return invalidf("a local user needs a password")
	}
	if in.Source == "ldap" {
		hash = ""
	}
	if in.Active && !cur.Active {
		if err := s.checkCapacity(ctx, 1); err != nil {
			return err
		}
	}
	_, err = s.db.Exec(ctx, `UPDATE otp_users SET username = $2, name = $3, mobile = $4, source = $5, password_hash = $6, fortinet_group = $7, active = $8
		WHERE id = $1`, id, in.Username, in.Name, in.Mobile, in.Source, hash, in.FortinetGroup, in.Active)
	return mapErr(err)
}

// Delete removes a user.
func (s *Service) Delete(ctx context.Context, id int64) error {
	tag, err := s.db.Exec(ctx, `DELETE FROM otp_users WHERE id = $1`, id)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// Unlock clears a lockout.
func (s *Service) Unlock(ctx context.Context, id int64) error {
	tag, err := s.db.Exec(ctx, `UPDATE otp_users SET failed = 0, locked_until = NULL WHERE id = $1`, id)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// Event is a sign-in event.
type Event struct {
	ID       int64     `json:"id"`
	At       time.Time `json:"at"`
	Username string    `json:"username"`
	DeviceID *int64    `json:"device_id"`
	ClientIP string    `json:"client_ip"`
	Event    string    `json:"event"`
	Detail   string    `json:"detail"`
}

// LogEvent records a sign-in step.
func (s *Service) LogEvent(ctx context.Context, username string, deviceID *int64, ip, event, detail string) {
	_, _ = s.db.Exec(context.WithoutCancel(ctx), `INSERT INTO otp_events (username, device_id, client_ip, event, detail) VALUES ($1, $2, $3, $4, $5)`,
		username, deviceID, ip, event, detail)
}

// Events returns recent events.
func (s *Service) Events(ctx context.Context, limit int) ([]Event, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	rows, err := s.db.Query(ctx, `SELECT id, at, username, device_id, client_ip, event, detail FROM otp_events ORDER BY at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[Event])
}

func (s *Service) fail(ctx context.Context, u *User, p Policy) {
	failed := u.failed + 1
	var until *time.Time
	if failed >= p.MaxFailures {
		t := s.now().Add(time.Duration(p.LockoutMinutes) * time.Minute)
		until, failed = &t, 0
	}
	_, _ = s.db.Exec(context.WithoutCancel(ctx), `UPDATE otp_users SET failed = $2, locked_until = $3 WHERE id = $1`, u.ID, failed, until)
}

// CheckPassword verifies the first factor. Failures count towards the
// lockout; every failure returns ErrRejected with the reason as detail.
func (s *Service) CheckPassword(ctx context.Context, username, password string) (*User, string, error) {
	p, err := s.Policy(ctx)
	if err != nil {
		return nil, "", err
	}
	u, err := s.byName(ctx, username)
	if errors.Is(err, ErrNotFound) {
		_, _ = auth.CheckPassword(dummyHash, password)
		return nil, "unknown user", ErrRejected
	}
	if err != nil {
		return nil, "", err
	}
	switch {
	case !u.Active:
		return nil, "user is disabled", ErrRejected
	case u.Locked:
		return nil, "user is locked", ErrRejected
	}
	if s.Capacity() == 0 {
		return nil, "the license has no two-factor users", ErrRejected
	}
	ok := false
	if u.Source == "local" {
		ok, err = auth.CheckPassword(u.hash, password)
		if err != nil {
			return nil, "", err
		}
	} else {
		ok, err = s.ldapCheck(ctx, u.Username, password)
		if err != nil {
			return nil, "directory error: " + err.Error(), ErrRejected
		}
	}
	if !ok {
		s.fail(ctx, u, p)
		return nil, "wrong password", ErrRejected
	}
	return u, "", nil
}

var dummyHash = func() string {
	h, _ := auth.HashPassword("dummy password for timing")
	return h
}()

func newCode(n int) string {
	var b strings.Builder
	for range n {
		d, _ := rand.Int(rand.Reader, big.NewInt(10))
		b.WriteByte(byte('0' + d.Int64()))
	}
	return b.String()
}

func newState() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// ErrRateLimited means too many codes were sent to the mobile recently.
var ErrRateLimited = errors.New("too many codes sent to this mobile; try again later")

// StartChallenge sends a code to u's mobile and returns the challenge
// state that the second request must carry.
func (s *Service) StartChallenge(ctx context.Context, u *User, deviceID *int64) (string, error) {
	p, err := s.Policy(ctx)
	if err != nil {
		return "", err
	}
	sent, err := s.sms.SentSince(ctx, u.Mobile, s.now().Add(-10*time.Minute))
	if err != nil {
		return "", err
	}
	if sent >= p.MaxSends {
		s.sms.LogBlocked(ctx, u.Mobile, "radius", u.Username, deviceID, "rate limit")
		return "", ErrRateLimited
	}
	code := newCode(p.Length)
	if err := s.sms.Send(ctx, u.Mobile, code, "radius", u.Username, deviceID); err != nil {
		return "", err
	}
	state := newState()
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	for k, c := range s.challenges {
		if now.After(c.expires) {
			delete(s.challenges, k)
		}
	}
	s.challenges[state] = &challenge{userID: u.ID, codeHash: sha256.Sum256([]byte(code)), expires: now.Add(time.Duration(p.TTLSeconds) * time.Second)}
	return state, nil
}

// VerifyChallenge checks the code for a state. A wrong code may be retried
// up to the policy's attempts; the challenge ends on success or exhaustion.
func (s *Service) VerifyChallenge(ctx context.Context, state, code string) (*User, string, error) {
	p, err := s.Policy(ctx)
	if err != nil {
		return nil, "", err
	}
	s.mu.Lock()
	c, ok := s.challenges[state]
	if !ok || s.now().After(c.expires) {
		delete(s.challenges, state)
		s.mu.Unlock()
		return nil, "code expired or unknown", ErrRejected
	}
	sum := sha256.Sum256([]byte(strings.TrimSpace(code)))
	if subtle.ConstantTimeCompare(sum[:], c.codeHash[:]) != 1 {
		c.attempts++
		left := c.attempts < p.MaxAttempts
		if !left {
			delete(s.challenges, state)
		}
		s.mu.Unlock()
		if u, err := s.Get(ctx, c.userID); err == nil && !left {
			s.fail(ctx, u, p)
		}
		if left {
			return nil, "wrong code", ErrWrongCode
		}
		return nil, "too many wrong codes", ErrRejected
	}
	delete(s.challenges, state)
	s.mu.Unlock()
	u, err := s.Get(ctx, c.userID)
	if err != nil {
		return nil, "", err
	}
	if !u.Active || u.Locked {
		return nil, "user is disabled or locked", ErrRejected
	}
	_, _ = s.db.Exec(ctx, `UPDATE otp_users SET failed = 0, locked_until = NULL, last_login_at = now() WHERE id = $1`, u.ID)
	return u, "", nil
}

// ErrWrongCode means the code was wrong but the challenge may be retried.
var ErrWrongCode = errors.New("wrong code")
