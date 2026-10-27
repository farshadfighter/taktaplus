package device

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"regexp"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

// CLI runs command scripts on the device over SSH.
type CLI interface {
	// RunScript sends the script's lines to a CLI shell and returns the
	// output. Lines the device rejects are reported as CLIError.
	RunScript(ctx context.Context, script string) (string, error)
}

// PackageInstaller uploads a signature package or firmware image through the
// device API. typ is a manifest package type ("ips_db", "firmware", …).
type PackageInstaller interface {
	InstallPackage(ctx context.Context, typ, name string, r io.Reader) error
}

// AsCLI returns the driver's CLI, if any transport offers one.
func AsCLI(d Driver) (CLI, bool) {
	switch v := d.(type) {
	case fallback:
		if c, ok := AsCLI(v.primary); ok {
			return c, true
		}
		return AsCLI(v.secondary)
	case CLI:
		return v, true
	}
	return nil, false
}

// AsInstaller returns the driver's API package installer, if any.
func AsInstaller(d Driver) (PackageInstaller, bool) {
	switch v := d.(type) {
	case fallback:
		if c, ok := AsInstaller(v.primary); ok {
			return c, true
		}
		return AsInstaller(v.secondary)
	case PackageInstaller:
		return v, true
	}
	return nil, false
}

// CLIError reports script lines the device rejected.
type CLIError struct{ Lines []string }

func (e CLIError) Error() string { return "device reported errors: " + strings.Join(e.Lines, "; ") }

// cliErrorRe matches FortiOS and FortiWeb CLI error messages.
var cliErrorRe = regexp.MustCompile(`(?im)^.*(Command fail|Unknown action|entry not found|node_check_object fail|value parse error|invalid (?:value|input|command)|Return code -\d+|failed to|error:).*$`)

// CheckCLIOutput returns the error lines in CLI output.
func CheckCLIOutput(out string) error {
	var lines []string
	for _, m := range cliErrorRe.FindAllString(out, -1) {
		if l := strings.TrimSpace(m); l != "" {
			lines = append(lines, l)
		}
	}
	if len(lines) > 0 {
		return CLIError{Lines: lines}
	}
	return nil
}

// RunScript opens a shell, sends the script followed by "exit" and collects
// the output until the device closes the session.
func (d *sshDriver) RunScript(ctx context.Context, script string) (string, error) {
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
	sess.Stderr = sess.Stdout
	stdin, err := sess.StdinPipe()
	if err != nil {
		return "", err
	}
	if err := sess.Shell(); err != nil {
		return "", err
	}
	lines := strings.Split(strings.ReplaceAll(script, "\r\n", "\n"), "\n")
	go func() {
		for _, l := range lines {
			if _, err := io.WriteString(stdin, l+"\n"); err != nil {
				return
			}
			time.Sleep(50 * time.Millisecond) // the CLI reads line by line
		}
		_, _ = io.WriteString(stdin, "exit\n")
	}()
	err = sess.Wait()
	text := strings.ReplaceAll(out.String(), "\r\n", "\n")
	if ctx.Err() != nil {
		return text, ctx.Err()
	}
	var exitErr *ssh.ExitError
	var missing *ssh.ExitMissingError
	if err != nil && !errors.As(err, &exitErr) && !errors.As(err, &missing) && !errors.Is(err, io.EOF) {
		return text, fmt.Errorf("SSH session: %w", err)
	}
	return text, CheckCLIOutput(text)
}

// InstallPackage uploads a package with the FortiOS API: firmware through
// /monitor/system/firmware/upgrade, databases and engines through
// /monitor/system/fortiguard/manual-update.
func (f *fortiGateAPI) InstallPackage(ctx context.Context, typ, _ string, r io.Reader) error {
	data, err := io.ReadAll(io.LimitReader(r, 4<<30))
	if err != nil {
		return err
	}
	content := base64.StdEncoding.EncodeToString(data)
	if typ == "firmware" {
		_, err = f.rc.do(ctx, http.MethodPost, "/api/v2/monitor/system/firmware/upgrade",
			map[string]any{"source": "upload", "file_content": content})
		return err
	}
	_, err = f.rc.do(ctx, http.MethodPost, "/api/v2/monitor/system/fortiguard/manual-update", map[string]any{"file_content": content})
	return err
}
