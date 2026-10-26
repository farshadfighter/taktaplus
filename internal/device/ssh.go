package device

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

// sshDriver implements Driver over the device CLI. It works for FortiGate
// and FortiWeb, whose CLIs share the commands used here.
type sshDriver struct {
	c Config
}

func newSSHDriver(c Config) Driver { return &sshDriver{c: c} }

// HostKeyMismatchError means the SSH host key differs from the pinned one.
type HostKeyMismatchError struct{ Got string }

func (e HostKeyMismatchError) Error() string {
	return "SSH host key does not match the pinned key (got " + e.Got + ")"
}

func hostKeyLine(k ssh.PublicKey) string {
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(k)))
}

func (d *sshDriver) clientConfig() (*ssh.ClientConfig, error) {
	var methods []ssh.AuthMethod
	if len(d.c.SSHKey) > 0 {
		signer, err := ssh.ParsePrivateKey(d.c.SSHKey)
		if err != nil {
			return nil, fmt.Errorf("SSH private key: %w", err)
		}
		methods = append(methods, ssh.PublicKeys(signer))
	}
	if d.c.SSHPassword != "" {
		pw := d.c.SSHPassword
		methods = append(methods, ssh.Password(pw),
			ssh.KeyboardInteractive(func(_, _ string, questions []string, _ []bool) ([]string, error) {
				answers := make([]string, len(questions))
				for i := range answers {
					answers[i] = pw
				}
				return answers, nil
			}))
	}
	pinned := d.c.SSHHostKey
	return &ssh.ClientConfig{
		User: d.c.SSHUser,
		Auth: methods,
		HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
			if pinned == "" {
				return nil // first use: the inventory records the key from FetchSSHHostKey
			}
			if got := hostKeyLine(key); got != strings.TrimSpace(pinned) {
				return HostKeyMismatchError{Got: got}
			}
			return nil
		},
		Timeout: 15 * time.Second,
	}, nil
}

func sshAddr(host string, port int) string {
	if port == 0 {
		port = 22
	}
	return net.JoinHostPort(host, strconv.Itoa(port))
}

// run executes one CLI command and returns its output.
func (d *sshDriver) run(ctx context.Context, cmd string) (string, error) {
	cfg, err := d.clientConfig()
	if err != nil {
		return "", err
	}
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "tcp", sshAddr(d.c.Host, d.c.SSHPort))
	if err != nil {
		return "", err
	}
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	sc, chans, reqs, err := ssh.NewClientConn(conn, sshAddr(d.c.Host, d.c.SSHPort), cfg)
	if err != nil {
		conn.Close()
		return "", err
	}
	client := ssh.NewClient(sc, chans, reqs)
	defer client.Close()
	sess, err := client.NewSession()
	if err != nil {
		return "", err
	}
	defer sess.Close()
	var out bytes.Buffer
	sess.Stdout = &limitedBuffer{buf: &out, max: maxBody}
	if err := sess.Run(cmd); err != nil {
		var exitErr *ssh.ExitError
		if !errors.As(err, &exitErr) || out.Len() == 0 {
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			return "", fmt.Errorf("SSH %q: %w", cmd, err)
		}
	}
	text := strings.ReplaceAll(out.String(), "\r\n", "\n")
	if strings.Contains(text, "Unknown action") || strings.Contains(text, "Command fail") {
		return "", fmt.Errorf("SSH %q: device rejected the command", cmd)
	}
	return text, nil
}

type limitedBuffer struct {
	buf *bytes.Buffer
	max int
}

func (l *limitedBuffer) Write(p []byte) (int, error) {
	if l.buf.Len()+len(p) > l.max {
		return 0, errors.New("SSH output too large")
	}
	return l.buf.Write(p)
}

// FetchSSHHostKey connects to host:port and returns the host key in
// authorized_keys format, for trust-on-first-use pinning.
func FetchSSHHostKey(ctx context.Context, host string, port int) (string, error) {
	var got string
	errGot := errors.New("host key captured")
	cfg := &ssh.ClientConfig{
		User: "taktaplus",
		HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
			got = hostKeyLine(key)
			return errGot
		},
		Timeout: 15 * time.Second,
	}
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "tcp", sshAddr(host, port))
	if err != nil {
		return "", err
	}
	defer conn.Close()
	_, _, _, err = ssh.NewClientConn(conn, sshAddr(host, port), cfg)
	if got != "" {
		return got, nil
	}
	return "", err
}

// kv parses "Key: value" lines of CLI output.
func kv(out string) map[string]string {
	m := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		k = strings.ToLower(strings.TrimSpace(k))
		if _, dup := m[k]; !dup {
			m[k] = strings.TrimSpace(v)
		}
	}
	return m
}

var (
	cliVersion = regexp.MustCompile(`^(\S+?)(?:-(\S+))?\s+v(\d+\.\d+\.\d+),\s*build(\d+)`)
	sigLine    = regexp.MustCompile(`(?im)^\s*([A-Za-z][\w\- ]*?(?:DB|Engine|Definitions|Signature)[\w\- ]*?)\s*:\s*(\d+\.\d+)`)
)

func (d *sshDriver) Probe(ctx context.Context) (Info, error) {
	out, err := d.run(ctx, "get system status")
	if err != nil {
		return Info{}, err
	}
	return parseSystemStatus(out), nil
}

func parseSystemStatus(out string) Info {
	m := kv(out)
	info := Info{Serial: m["serial-number"], Hostname: m["hostname"], HAMode: "standalone"}
	if info.Serial == "" {
		info.Serial = m["serial number"]
	}
	if v := cliVersion.FindStringSubmatch(m["version"]); v != nil {
		// "FortiGate-100F v7.4.4,build2662,…" → model FortiGate-100F
		info.Model = v[1]
		if v[2] != "" {
			info.Model += "-" + v[2]
		}
		info.Firmware, info.Build = v[3], v[4]
	}
	if ha := strings.ToLower(m["current ha mode"]); ha != "" && ha != "standalone" {
		switch {
		case strings.Contains(ha, "a-p"), strings.Contains(ha, "active-passive"):
			info.HAMode = "a-p"
		case strings.Contains(ha, "a-a"), strings.Contains(ha, "active-active"):
			info.HAMode = "a-a"
		default:
			info.HAMode = ha
		}
	}
	return info
}

func (d *sshDriver) Health(ctx context.Context) (Health, error) {
	out, err := d.run(ctx, "get system performance status")
	if err != nil {
		return Health{}, err
	}
	return parsePerformance(out), nil
}

var (
	perfIdle     = regexp.MustCompile(`CPU states:.*?(\d+(?:\.\d+)?)% idle`)
	perfCPU      = regexp.MustCompile(`(?i)CPU(?: usage)?\s*:\s*(\d+(?:\.\d+)?)%`)
	perfMem      = regexp.MustCompile(`(?i)Memory(?: usage)?\s*:.*?(\d+(?:\.\d+)?)%`)
	perfDisk     = regexp.MustCompile(`(?i)(?:Log )?Disk(?: usage)?\s*:.*?(\d+(?:\.\d+)?)%`)
	perfSessions = regexp.MustCompile(`(?i)Average sessions:\s*(\d+)`)
	perfUptime   = regexp.MustCompile(`(?i)Up ?time:\s*(?:(\d+) days?,?)?\s*(?:(\d+) hours?,?)?\s*(?:(\d+) minutes?)?`)
)

func parsePerformance(out string) Health {
	h := Health{Disk: -1}
	f := func(s string) float64 { v, _ := strconv.ParseFloat(s, 64); return v }
	if m := perfIdle.FindStringSubmatch(out); m != nil {
		h.CPU = 100 - f(m[1])
	} else if m := perfCPU.FindStringSubmatch(out); m != nil {
		h.CPU = f(m[1])
	}
	if m := perfMem.FindStringSubmatch(out); m != nil {
		h.Mem = f(m[1])
	}
	if m := perfDisk.FindStringSubmatch(out); m != nil {
		h.Disk = f(m[1])
	}
	if m := perfSessions.FindStringSubmatch(out); m != nil {
		h.Sessions = int64(f(m[1]))
	}
	if m := perfUptime.FindStringSubmatch(out); m != nil {
		h.Uptime = int64(f(m[1])*86400 + f(m[2])*3600 + f(m[3])*60)
	}
	return h
}

// Signatures reads the database versions listed by "get system status".
// Contract expiry is not shown there; the REST API supplies it.
func (d *sshDriver) Signatures(ctx context.Context) ([]Signature, []Contract, error) {
	out, err := d.run(ctx, "get system status")
	if err != nil {
		return nil, nil, err
	}
	return parseSignatureLines(out), nil, nil
}

func parseSignatureLines(out string) []Signature {
	var sigs []Signature
	for _, m := range sigLine.FindAllStringSubmatch(out, -1) {
		name := strings.ToLower(strings.Join(strings.Fields(strings.ReplaceAll(m[1], "-", " ")), "_"))
		sigs = append(sigs, Signature{Name: name, Version: m[2]})
	}
	return sigs
}

func (d *sshDriver) BackupConfig(ctx context.Context) ([]byte, error) {
	out, err := d.run(ctx, "show full-configuration")
	if err != nil {
		return nil, err
	}
	// Drop the CLI prompt echo and paging artefacts around the config.
	if i := strings.Index(out, "#config-version="); i > 0 {
		out = out[i:]
	}
	if !looksLikeConfig([]byte(out)) {
		return nil, errors.New("SSH backup did not return a configuration")
	}
	return []byte(out), nil
}

// RestoreConfig over SSH would need a TFTP/FTP server reachable by the
// device; it is done through the REST API instead.
func (d *sshDriver) RestoreConfig(context.Context, []byte) error { return ErrUnsupported }
