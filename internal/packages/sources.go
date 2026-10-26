package packages

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jlaffaye/ftp"
	"github.com/pin/tftp/v3"
	"github.com/pkg/sftp"
	"github.com/robfig/cron/v3"
	"golang.org/x/crypto/ssh"

	"github.com/farshadfighter/taktaplus-lic/pkg/manifest"
	"github.com/farshadfighter/taktaplus-lic/pkg/repoauth"
)

const sourceContext = "package-source"

// Source kinds.
const (
	KindCentral = "central"
	KindHTTP    = "http"
	KindFTP     = "ftp"
	KindSFTP    = "sftp"
	KindTFTP    = "tftp"
)

// SourceConfig holds the settings of every kind; it is stored sealed.
type SourceConfig struct {
	URL      string   `json:"url,omitempty"`  // central (optional override), http
	Host     string   `json:"host,omitempty"` // ftp, sftp, tftp
	Port     int      `json:"port,omitempty"`
	User     string   `json:"user,omitempty"`
	Password string   `json:"password,omitempty"`
	Dir      string   `json:"dir,omitempty"`      // ftp, sftp
	TLS      bool     `json:"tls,omitempty"`      // ftp: explicit TLS
	HostKey  string   `json:"host_key,omitempty"` // sftp: pinned key; empty = record on first sync
	Files    []string `json:"files,omitempty"`    // tftp has no listing
}

func (c SourceConfig) redacted() SourceConfig {
	if c.Password != "" {
		c.Password = "********"
	}
	return c
}

// Source is a configured package source.
type Source struct {
	ID         int64        `json:"id"`
	Name       string       `json:"name"`
	Kind       string       `json:"kind"`
	Config     SourceConfig `json:"config"`
	Schedule   string       `json:"schedule"`
	Enabled    bool         `json:"enabled"`
	LastSyncAt *time.Time   `json:"last_sync_at"`
	LastStatus string       `json:"last_status"`
	LastError  string       `json:"last_error"`
	NextSyncAt *time.Time   `json:"next_sync_at"`
	createdAt  time.Time
}

var cronParser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)

func (src *Source) validate() error {
	src.Name = strings.TrimSpace(src.Name)
	src.Schedule = strings.TrimSpace(src.Schedule)
	c := &src.Config
	if src.Name == "" {
		return invalidf("source name is required")
	}
	if src.Schedule != "" {
		if _, err := cronParser.Parse(src.Schedule); err != nil {
			return invalidf("schedule is not a valid cron expression")
		}
	}
	switch src.Kind {
	case KindCentral:
		if c.URL != "" {
			if u, err := url.Parse(c.URL); err != nil || u.Scheme != "https" && u.Scheme != "http" {
				return invalidf("URL must be an http(s) URL")
			}
		}
	case KindHTTP:
		if u, err := url.Parse(c.URL); err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
			return invalidf("URL must be an http(s) URL")
		}
	case KindFTP, KindSFTP, KindTFTP:
		if c.Host == "" {
			return invalidf("server address is required")
		}
		if c.Port < 0 || c.Port > 65535 {
			return invalidf("port is out of range")
		}
		if src.Kind == KindTFTP {
			c.Files = clean(c.Files)
			if len(c.Files) == 0 {
				return invalidf("TFTP cannot list files: enter the file names to fetch")
			}
		}
		if src.Kind == KindSFTP && c.User == "" {
			return invalidf("SFTP needs a user name")
		}
	default:
		return invalidf("unknown source kind")
	}
	return nil
}

// remoteFile is one file a source offers.
type remoteFile struct {
	Name string
	Size int64
	pkg  *manifest.Package // from a verified manifest
}

// fetcher lists and downloads a source's files.
type fetcher interface {
	// list returns the files; signed reports whether they come from a
	// manifest verified with a trusted key.
	list(ctx context.Context) (files []remoteFile, signed bool, err error)
	open(ctx context.Context, f remoteFile) (io.ReadCloser, error)
	close()
}

type fetcherFactory func(s *Service, src *Source) (fetcher, error)

func defaultFetcher(s *Service, src *Source) (fetcher, error) {
	switch src.Kind {
	case KindCentral:
		return s.centralFetcher(src)
	case KindHTTP:
		return &httpFetcher{s: s, base: strings.TrimRight(src.Config.URL, "/"), client: httpClient}, nil
	case KindFTP:
		return newFTPFetcher(s, src)
	case KindSFTP:
		return newSFTPFetcher(s, src)
	case KindTFTP:
		return &tftpFetcher{cfg: src.Config}, nil
	}
	return nil, invalidf("unknown source kind")
}

var httpClient = &http.Client{Timeout: 30 * time.Minute}

// verifyManifest checks a fetched manifest and turns it into file entries.
func (s *Service) verifyManifest(body []byte, sig string) ([]remoteFile, error) {
	if len(s.ring) == 0 {
		return nil, errors.New("no trusted package keys are built in; signed manifests cannot be verified")
	}
	m, err := manifest.Verify(body, sig, s.ring)
	if err != nil {
		return nil, err
	}
	out := make([]remoteFile, 0, len(m.Packages))
	for i := range m.Packages {
		p := m.Packages[i]
		out = append(out, remoteFile{Name: p.File, Size: p.Size, pkg: &p})
	}
	return out, nil
}

// --- central repository and HTTP mirrors ---

type httpFetcher struct {
	s      *Service
	base   string // .../manifest.json and .../files/<file>, or the central /api/v1/repo
	client *http.Client
	sign   func(req *http.Request) // central: installation signature
	byHash bool                    // central serves files by sha256
}

func (h *httpFetcher) get(ctx context.Context, rel string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, h.base+rel, nil)
	if err != nil {
		return nil, err
	}
	if h.sign != nil {
		h.sign(req)
	}
	resp, err := h.client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		var e struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&e)
		if e.Error == "" {
			e.Error = resp.Status
		}
		return nil, fmt.Errorf("%s: %s", rel, e.Error)
	}
	return resp, nil
}

func (h *httpFetcher) list(ctx context.Context) ([]remoteFile, bool, error) {
	read := func(rel string) ([]byte, error) {
		resp, err := h.get(ctx, rel)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		return io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	}
	body, err := read("/manifest" + map[bool]string{true: "", false: ".json"}[h.byHash])
	if err != nil {
		return nil, false, err
	}
	sig, err := read("/manifest.sig")
	if err != nil {
		return nil, false, err
	}
	files, err := h.s.verifyManifest(body, string(sig))
	return files, true, err
}

func (h *httpFetcher) open(ctx context.Context, f remoteFile) (io.ReadCloser, error) {
	rel := "/files/" + url.PathEscape(f.Name)
	if h.byHash {
		rel = "/files/" + f.pkg.SHA256
	}
	resp, err := h.get(ctx, rel)
	if err != nil {
		return nil, err
	}
	return resp.Body, nil
}

func (h *httpFetcher) close() {}

// ErrNotEntitled means there is no license to authenticate with.
var ErrNotEntitled = errors.New("the central repository needs an installed online license")

func (s *Service) centralFetcher(src *Source) (fetcher, error) {
	base := strings.TrimRight(src.Config.URL, "/")
	if base == "" && s.lic != nil {
		base = s.lic.ServerURL()
	}
	if base == "" {
		return nil, errors.New("license server address is unknown; set it on the source or TAKTAPLUS_LICENSE_SERVER")
	}
	if s.lic == nil {
		return nil, ErrNotEntitled
	}
	lic, err := s.lic.Guard().License()
	if err != nil {
		return nil, ErrNotEntitled
	}
	id := s.lic.Guard().Identity()
	h := &httpFetcher{s: s, base: base + "/api/v1/repo", client: httpClient, byHash: true}
	h.sign = func(req *http.Request) {
		req.Header.Set("Authorization", repoauth.Header(id.Private, lic.LicenseID, id.ID(), req.Method, req.URL.Path, time.Now()))
	}
	return h, nil
}

// --- FTP ---

type ftpFetcher struct {
	conn *ftp.ServerConn
	dir  string
	s    *Service
}

func newFTPFetcher(s *Service, src *Source) (fetcher, error) {
	c := src.Config
	port := c.Port
	if port == 0 {
		port = 21
	}
	opts := []ftp.DialOption{ftp.DialWithTimeout(30 * time.Second)}
	if c.TLS {
		opts = append(opts, ftp.DialWithExplicitTLS(&tls.Config{ServerName: c.Host, MinVersion: tls.VersionTLS12}))
	}
	conn, err := ftp.Dial(net.JoinHostPort(c.Host, strconv.Itoa(port)), opts...)
	if err != nil {
		return nil, err
	}
	user, pass := c.User, c.Password
	if user == "" {
		user, pass = "anonymous", "anonymous"
	}
	if err := conn.Login(user, pass); err != nil {
		_ = conn.Quit()
		return nil, fmt.Errorf("FTP login: %w", err)
	}
	return &ftpFetcher{conn: conn, dir: c.Dir, s: s}, nil
}

func (f *ftpFetcher) readSmall(name string) ([]byte, error) {
	r, err := f.conn.Retr(path.Join(f.dir, name))
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return io.ReadAll(io.LimitReader(r, 32<<20))
}

func (f *ftpFetcher) list(context.Context) ([]remoteFile, bool, error) {
	entries, err := f.conn.List(f.dir)
	if err != nil {
		return nil, false, err
	}
	names := map[string]bool{}
	var files []remoteFile
	for _, e := range entries {
		if e.Type == ftp.EntryTypeFile {
			names[e.Name] = true
			files = append(files, remoteFile{Name: e.Name, Size: int64(e.Size)})
		}
	}
	return mirrorOrPlain(f.s, names, files, f.readSmall)
}

func (f *ftpFetcher) open(_ context.Context, rf remoteFile) (io.ReadCloser, error) {
	name := rf.Name
	if rf.pkg != nil {
		name = "files/" + rf.Name
	}
	return f.conn.Retr(path.Join(f.dir, name))
}

func (f *ftpFetcher) close() { _ = f.conn.Quit() }

// mirrorOrPlain treats a directory holding manifest.json and manifest.sig
// as a signed mirror (files under files/); otherwise every file is offered
// unverified.
func mirrorOrPlain(s *Service, names map[string]bool, files []remoteFile, read func(string) ([]byte, error)) ([]remoteFile, bool, error) {
	if names["manifest.json"] && names["manifest.sig"] {
		body, err := read("manifest.json")
		if err != nil {
			return nil, false, err
		}
		sig, err := read("manifest.sig")
		if err != nil {
			return nil, false, err
		}
		list, err := s.verifyManifest(body, string(sig))
		return list, true, err
	}
	return files, false, nil
}

// --- SFTP ---

type sftpFetcher struct {
	sc   *ssh.Client
	c    *sftp.Client
	dir  string
	s    *Service
	seen string // host key presented, for pinning on first use
}

func newSFTPFetcher(s *Service, src *Source) (fetcher, error) {
	c := src.Config
	port := c.Port
	if port == 0 {
		port = 22
	}
	f := &sftpFetcher{dir: c.Dir, s: s}
	cfg := &ssh.ClientConfig{
		User:    c.User,
		Auth:    []ssh.AuthMethod{ssh.Password(c.Password)},
		Timeout: 30 * time.Second,
		HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
			f.seen = strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key)))
			if c.HostKey != "" && c.HostKey != f.seen {
				return errors.New("SFTP host key does not match the pinned key")
			}
			return nil
		},
	}
	sc, err := ssh.Dial("tcp", net.JoinHostPort(c.Host, strconv.Itoa(port)), cfg)
	if err != nil {
		return nil, err
	}
	client, err := sftp.NewClient(sc)
	if err != nil {
		sc.Close()
		return nil, err
	}
	f.sc, f.c = sc, client
	return f, nil
}

func (f *sftpFetcher) readSmall(name string) ([]byte, error) {
	r, err := f.c.Open(path.Join(f.dir, name))
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return io.ReadAll(io.LimitReader(r, 32<<20))
}

func (f *sftpFetcher) list(context.Context) ([]remoteFile, bool, error) {
	dir := f.dir
	if dir == "" {
		dir = "."
	}
	entries, err := f.c.ReadDir(dir)
	if err != nil {
		return nil, false, err
	}
	names := map[string]bool{}
	var files []remoteFile
	for _, e := range entries {
		if e.Mode().IsRegular() {
			names[e.Name()] = true
			files = append(files, remoteFile{Name: e.Name(), Size: e.Size()})
		}
	}
	return mirrorOrPlain(f.s, names, files, f.readSmall)
}

func (f *sftpFetcher) open(_ context.Context, rf remoteFile) (io.ReadCloser, error) {
	name := rf.Name
	if rf.pkg != nil {
		name = "files/" + rf.Name
	}
	return f.c.Open(path.Join(f.dir, name))
}

func (f *sftpFetcher) close() {
	_ = f.c.Close()
	_ = f.sc.Close()
}

// --- TFTP ---

type tftpFetcher struct{ cfg SourceConfig }

func (t *tftpFetcher) list(context.Context) ([]remoteFile, bool, error) {
	out := make([]remoteFile, 0, len(t.cfg.Files))
	for _, f := range t.cfg.Files {
		out = append(out, remoteFile{Name: f, Size: -1})
	}
	return out, false, nil
}

func (t *tftpFetcher) open(_ context.Context, f remoteFile) (io.ReadCloser, error) {
	port := t.cfg.Port
	if port == 0 {
		port = 69
	}
	c, err := tftp.NewClient(net.JoinHostPort(t.cfg.Host, strconv.Itoa(port)))
	if err != nil {
		return nil, err
	}
	c.SetTimeout(10 * time.Second)
	c.SetRetries(5)
	wt, err := c.Receive(f.Name, "octet")
	if err != nil {
		return nil, err
	}
	pr, pw := io.Pipe()
	go func() {
		_, err := wt.WriteTo(pw)
		pw.CloseWithError(err)
	}()
	return pr, nil
}

func (t *tftpFetcher) close() {}

// --- source records ---

func (s *Service) loadSources(ctx context.Context, where string, args ...any) ([]*Source, error) {
	rows, err := s.db.Query(ctx, `SELECT id, name, kind, config, schedule, enabled, last_sync_at, last_status, last_error, created_at
		FROM package_sources `+where+` ORDER BY id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Source{}
	now := s.now()
	for rows.Next() {
		var src Source
		var sealed []byte
		if err := rows.Scan(&src.ID, &src.Name, &src.Kind, &sealed, &src.Schedule, &src.Enabled, &src.LastSyncAt, &src.LastStatus, &src.LastError, &src.createdAt); err != nil {
			return nil, err
		}
		plain, err := s.box.Open(sealed, sourceContext)
		if err != nil {
			return nil, fmt.Errorf("source %d: %w", src.ID, err)
		}
		if err := json.Unmarshal(plain, &src.Config); err != nil {
			return nil, err
		}
		if src.Enabled && src.Schedule != "" {
			if sched, err := cronParser.Parse(src.Schedule); err == nil {
				from := src.createdAt
				if src.LastSyncAt != nil {
					from = *src.LastSyncAt
				}
				n := sched.Next(from)
				if n.Before(now.Add(-24 * time.Hour)) {
					n = sched.Next(now.Add(-time.Minute))
				}
				src.NextSyncAt = &n
			}
		}
		out = append(out, &src)
	}
	return out, rows.Err()
}

// ListSources returns sources with secrets redacted.
func (s *Service) ListSources(ctx context.Context) ([]*Source, error) {
	list, err := s.loadSources(ctx, "")
	for _, src := range list {
		src.Config = src.Config.redacted()
	}
	return list, err
}

func (s *Service) getSource(ctx context.Context, id int64) (*Source, error) {
	list, err := s.loadSources(ctx, "WHERE id = $1", id)
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, ErrNotFound
	}
	return list[0], nil
}

// SaveSource creates (ID 0) or updates a source; a redacted or empty
// password keeps the stored one.
func (s *Service) SaveSource(ctx context.Context, src Source) (int64, error) {
	if src.ID != 0 {
		old, err := s.getSource(ctx, src.ID)
		if err != nil {
			return 0, err
		}
		if src.Config.Password == "" || src.Config.Password == "********" {
			src.Config.Password = old.Config.Password
		}
		if src.Config.HostKey == "" && src.Config.Host == old.Config.Host {
			src.Config.HostKey = old.Config.HostKey
		}
	}
	if err := src.validate(); err != nil {
		return 0, err
	}
	data, _ := json.Marshal(src.Config)
	sealed := s.box.Seal(data, sourceContext)
	if src.ID == 0 {
		err := s.db.QueryRow(ctx, `INSERT INTO package_sources (name, kind, config, schedule, enabled) VALUES ($1, $2, $3, $4, $5) RETURNING id`,
			src.Name, src.Kind, sealed, src.Schedule, src.Enabled).Scan(&src.ID)
		return src.ID, mapSourceErr(err)
	}
	_, err := s.db.Exec(ctx, `UPDATE package_sources SET name = $2, kind = $3, config = $4, schedule = $5, enabled = $6 WHERE id = $1`,
		src.ID, src.Name, src.Kind, sealed, src.Schedule, src.Enabled)
	return src.ID, mapSourceErr(err)
}

func mapSourceErr(err error) error {
	if err != nil && strings.Contains(err.Error(), "package_sources_name_key") {
		return invalidf("a source with this name already exists")
	}
	return err
}

// DeleteSource removes a source; its packages stay in the repository.
func (s *Service) DeleteSource(ctx context.Context, id int64) error {
	tag, err := s.db.Exec(ctx, `DELETE FROM package_sources WHERE id = $1`, id)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// SyncRun is one sync attempt.
type SyncRun struct {
	ID         int64      `json:"id"`
	SourceID   *int64     `json:"source_id"`
	Origin     string     `json:"origin"`
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at"`
	Status     string     `json:"status"`
	Added      int        `json:"added"`
	Seen       int        `json:"seen"`
	Error      string     `json:"error"`
	Actor      string     `json:"actor"`
}

// Runs returns recent sync runs.
func (s *Service) Runs(ctx context.Context) ([]SyncRun, error) {
	rows, err := s.db.Query(ctx, `SELECT id, source_id, origin, started_at, finished_at, status, added, seen, error, actor
		FROM package_sync_runs ORDER BY started_at DESC LIMIT 50`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[SyncRun])
}

// Sync fetches a source's new files into the repository.
func (s *Service) Sync(ctx context.Context, id int64, actor string) (*SyncRun, error) {
	src, err := s.getSource(ctx, id)
	if err != nil {
		return nil, err
	}
	run := &SyncRun{SourceID: &src.ID, Origin: src.Name, StartedAt: s.now(), Actor: actor}
	if err := s.db.QueryRow(ctx, `INSERT INTO package_sync_runs (source_id, origin, actor) VALUES ($1, $2, $3) RETURNING id`,
		src.ID, src.Name, actor).Scan(&run.ID); err != nil {
		return nil, err
	}
	syncErr := s.syncSource(ctx, src, run)
	run.Status = "ok"
	if syncErr != nil {
		run.Status, run.Error = "failed", truncate(syncErr.Error(), 1000)
	}
	now := s.now()
	run.FinishedAt = &now
	bg := context.WithoutCancel(ctx)
	if _, err := s.db.Exec(bg, `UPDATE package_sync_runs SET finished_at = $2, status = $3, added = $4, seen = $5, error = $6 WHERE id = $1`,
		run.ID, now, run.Status, run.Added, run.Seen, run.Error); err != nil {
		return nil, err
	}
	if _, err := s.db.Exec(bg, `UPDATE package_sources SET last_sync_at = $2, last_status = $3, last_error = $4 WHERE id = $1`,
		src.ID, now, run.Status, run.Error); err != nil {
		return nil, err
	}
	return run, nil
}

func truncate(v string, n int) string {
	if len(v) > n {
		return v[:n]
	}
	return v
}

func (s *Service) syncSource(ctx context.Context, src *Source, run *SyncRun) error {
	f, err := s.fetch(s, src)
	if err != nil {
		return err
	}
	defer f.close()
	if sf, ok := f.(*sftpFetcher); ok && src.Config.HostKey == "" && sf.seen != "" {
		// Pin the SFTP host key seen on first use.
		src.Config.HostKey = sf.seen
		data, _ := json.Marshal(src.Config)
		if _, err := s.db.Exec(ctx, `UPDATE package_sources SET config = $2 WHERE id = $1`, src.ID, s.box.Seal(data, sourceContext)); err != nil {
			return err
		}
	}
	files, signed, err := f.list(ctx)
	if err != nil {
		return err
	}
	var errs []error
	seen := []string{}
	for _, rf := range files {
		run.Seen++
		sha, added, err := s.syncFile(ctx, f, src, rf, signed)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", rf.Name, err))
			continue
		}
		seen = append(seen, sha)
		if added {
			run.Added++
		}
	}
	if len(errs) == 0 {
		// Whatever the source no longer offers is withdrawn (kept on disk for
		// deployments already planned).
		if _, err := s.db.Exec(ctx, `UPDATE packages SET withdrawn = true WHERE source_id = $1 AND NOT (sha256 = ANY($2))`, src.ID, seen); err != nil {
			return err
		}
	}
	return errors.Join(errs...)
}

func (s *Service) syncFile(ctx context.Context, f fetcher, src *Source, rf remoteFile, signed bool) (string, bool, error) {
	var p *Package
	if rf.pkg != nil {
		m := rf.pkg
		p = &Package{SHA256: m.SHA256, File: m.File, Size: m.Size, Platform: string(m.Platform), Type: string(m.Type), Version: m.Version,
			Models: m.Models, FwMin: m.Firmware.Min, FwMax: m.Firmware.Max, UpgradeFrom: m.UpgradeFrom, Notes: m.Notes,
			ReleasedAt: m.ReleasedAt, Verified: signed}
		if ok, err := s.has(ctx, m.SHA256); err != nil {
			return "", false, err
		} else if ok {
			p.SourceID, p.Origin = &src.ID, src.Name
			return m.SHA256, false, s.upsert(ctx, p)
		}
	} else {
		// Unsigned: a file with the same name and size from this source is
		// taken as already fetched.
		var sha string
		err := s.db.QueryRow(ctx, `SELECT sha256 FROM packages WHERE source_id = $1 AND file = $2 AND (size = $3 OR $3 < 0)`, src.ID, rf.Name, rf.Size).Scan(&sha)
		if err == nil {
			if ok, _ := s.has(ctx, sha); ok {
				_, err := s.db.Exec(ctx, `UPDATE packages SET withdrawn = false WHERE sha256 = $1`, sha)
				return sha, false, err
			}
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return "", false, err
		}
	}
	r, err := f.open(ctx, rf)
	if err != nil {
		return "", false, err
	}
	want := ""
	if p != nil {
		want = p.SHA256
	}
	sha, size, err := s.store(r, want)
	r.Close()
	if err != nil {
		return "", false, err
	}
	if p == nil {
		g := Guess(rf.Name)
		p = &Package{SHA256: sha, File: rf.Name, Size: size, Platform: g.Platform, Type: g.Type, Version: g.Version, Models: g.Models,
			Notes: g.Notes, ReleasedAt: s.now()}
	}
	p.SourceID, p.Origin = &src.ID, src.Name
	return sha, true, s.upsert(ctx, p)
}

// RunDue syncs every enabled source whose schedule has come due.
func (s *Service) RunDue(ctx context.Context) error {
	list, err := s.loadSources(ctx, "WHERE enabled")
	if err != nil {
		return err
	}
	var errs []error
	for _, src := range list {
		if src.NextSyncAt == nil || src.NextSyncAt.After(s.now()) {
			continue
		}
		if _, err := s.Sync(ctx, src.ID, "schedule"); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// Scheduler syncs due sources every minute until ctx ends.
func (s *Service) Scheduler(ctx context.Context, logf func(msg string, args ...any)) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		if err := s.RunDue(ctx); err != nil && ctx.Err() == nil {
			logf("package sync failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
