// Package fileserver is the built-in TFTP and FTP server that devices pull
// packages and firmware from. Nothing is served without a grant: a short-
// lived, read-only permission for specific files and device addresses,
// with a one-time FTP account.
package fileserver

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base32"
	"io"
	"net"
	"strings"
	"sync"
	"time"
)

// Opener opens a stored file by its SHA-256.
type Opener func(sha string) (io.ReadSeekCloser, int64, error)

// Grant allows devices at IPs to read Files until Expires.
type Grant struct {
	ID       string
	Files    map[string]string // served name -> sha256
	IPs      map[string]bool
	User     string // FTP account
	Password string
	Expires  time.Time
}

// Name returns the name a device requests for sha (TFTP and FTP).
func (g *Grant) Name(sha string) string {
	for name, s := range g.Files {
		if s == sha {
			return name
		}
	}
	return ""
}

// Registry holds active grants.
type Registry struct {
	mu     sync.Mutex
	grants map[string]*Grant
	now    func() time.Time
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry { return &Registry{grants: map[string]*Grant{}, now: time.Now} }

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

func token(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return strings.ToLower(b32.EncodeToString(b))
}

// Grant creates a grant for files (served name -> sha256; the name is
// prefixed with an unguessable token) readable from ips for ttl.
func (r *Registry) Grant(files map[string]string, ips []string, ttl time.Duration) *Grant {
	g := &Grant{ID: token(10), Files: map[string]string{}, IPs: map[string]bool{}, User: "tp" + token(5), Password: token(10),
		Expires: r.now().Add(ttl)}
	prefix := "tp" + token(8) + "-"
	for name, sha := range files {
		g.Files[prefix+name] = sha
	}
	for _, ip := range ips {
		if p := net.ParseIP(ip); p != nil {
			g.IPs[p.String()] = true
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.gc()
	r.grants[g.ID] = g
	return g
}

// Revoke ends a grant early.
func (r *Registry) Revoke(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.grants, id)
}

func (r *Registry) gc() {
	now := r.now()
	for id, g := range r.grants {
		if now.After(g.Expires) {
			delete(r.grants, id)
		}
	}
}

func hostOf(addr net.Addr) string {
	host := addr.String()
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.String()
	}
	return host
}

// lookup finds the sha256 for a file requested from addr.
func (r *Registry) lookup(addr net.Addr, name string) (string, bool) {
	name = strings.TrimLeft(strings.ReplaceAll(name, "\\", "/"), "/")
	ip := hostOf(addr)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.gc()
	for _, g := range r.grants {
		if sha, ok := g.Files[name]; ok && g.IPs[ip] {
			return sha, true
		}
	}
	return "", false
}

// login checks FTP credentials from addr and returns the grant.
func (r *Registry) login(addr net.Addr, user, pass string) (*Grant, bool) {
	ip := hostOf(addr)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.gc()
	for _, g := range r.grants {
		if subtle.ConstantTimeCompare([]byte(g.User), []byte(user)) == 1 &&
			subtle.ConstantTimeCompare([]byte(g.Password), []byte(pass)) == 1 && g.IPs[ip] {
			return g, true
		}
	}
	return nil, false
}

// allowed reports whether any grant admits ip (FTP connections from other
// addresses are closed before login).
func (r *Registry) allowed(addr net.Addr) bool {
	ip := hostOf(addr)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.gc()
	for _, g := range r.grants {
		if g.IPs[ip] {
			return true
		}
	}
	return false
}
