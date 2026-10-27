package device

import (
	"bufio"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

const sampleConfig = "#config-version=FG100F-7.04-FW-build2662-240514:opmode=0:vdom=0\nconfig system global\n    set hostname \"FG-HQ\"\nend\n"

// fakeFortiGate serves the FortiOS REST endpoints the driver uses.
func fakeFortiGate(t *testing.T, restored *[]byte) *httptest.Server {
	mux := http.NewServeMux()
	reply := func(w http.ResponseWriter, v any) { _ = json.NewEncoder(w).Encode(v) }
	auth := func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer tok123" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			h(w, r)
		}
	}
	mux.HandleFunc("GET /api/v2/monitor/system/status", auth(func(w http.ResponseWriter, _ *http.Request) {
		reply(w, map[string]any{"serial": "FG100FTK20000001", "version": "v7.4.4", "build": 2662, "status": "success",
			"results": map[string]any{"model_name": "FortiGate", "model_number": "100F", "model": "FG100F", "hostname": "FG-HQ"}})
	}))
	mux.HandleFunc("GET /api/v2/cmdb/system/ha", auth(func(w http.ResponseWriter, _ *http.Request) {
		reply(w, map[string]any{"results": map[string]any{"mode": "a-p"}})
	}))
	mux.HandleFunc("GET /api/v2/monitor/system/ha-peer", auth(func(w http.ResponseWriter, _ *http.Request) {
		reply(w, map[string]any{"results": []any{map[string]any{"serial_no": "FG100FTK20000001"}, map[string]any{"serial_no": "FG100FTK20000002"}}})
	}))
	mux.HandleFunc("GET /api/v2/cmdb/system/vdom", auth(func(w http.ResponseWriter, _ *http.Request) {
		reply(w, map[string]any{"results": []any{map[string]any{"name": "root"}}})
	}))
	mux.HandleFunc("GET /api/v2/cmdb/system/central-management", auth(func(w http.ResponseWriter, _ *http.Request) {
		reply(w, map[string]any{"results": []any{map[string]any{"type": "fortimanager", "fmg": "\"10.0.0.5\""}}})
	}))
	mux.HandleFunc("GET /api/v2/monitor/system/resource/usage", auth(func(w http.ResponseWriter, _ *http.Request) {
		reply(w, map[string]any{"results": map[string]any{
			"cpu": []any{map[string]any{"current": 12}}, "mem": []any{map[string]any{"current": "41"}},
			"disk": []any{map[string]any{"current": 7}}, "session": []any{map[string]any{"current": 1520}},
		}})
	}))
	mux.HandleFunc("GET /api/v2/monitor/web-ui/state", auth(func(w http.ResponseWriter, _ *http.Request) {
		reply(w, map[string]any{"results": map[string]any{"snapshot_utc_time": 1_700_086_400_000, "utc_last_reboot": 1_700_000_000_000}})
	}))
	mux.HandleFunc("GET /api/v2/monitor/system/interface", auth(func(w http.ResponseWriter, _ *http.Request) {
		reply(w, map[string]any{"results": map[string]any{
			"wan1":  map[string]any{"name": "wan1", "ip": "203.0.113.2", "mask": 30, "link": true, "speed": 1000, "rx_bytes": 10, "tx_bytes": 20},
			"port1": map[string]any{"name": "port1", "ip": "0.0.0.0", "link": false},
		}})
	}))
	mux.HandleFunc("GET /api/v2/monitor/license/status", auth(func(w http.ResponseWriter, _ *http.Request) {
		reply(w, map[string]any{"results": map[string]any{
			"ips":       map[string]any{"status": "licensed", "expires": 1_798_761_600, "db": map[string]any{"version": "28.00948"}, "engine": map[string]any{"version": "7.00543"}},
			"antivirus": map[string]any{"status": "expired", "expires": 1_600_000_000, "db": map[string]any{"version": "92.01234"}},
			"vm":        map[string]any{"status": "valid"},
		}})
	}))
	mux.HandleFunc("GET /api/v2/monitor/system/config/backup", auth(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusMethodNotAllowed) // 7.6 behaviour
	}))
	mux.HandleFunc("POST /api/v2/monitor/system/config/backup", auth(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, sampleConfig)
	}))
	mux.HandleFunc("POST /api/v2/monitor/system/config/restore", auth(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		data, err := base64.StdEncoding.DecodeString(body["file_content"])
		if err != nil || body["source"] != "upload" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		*restored = data
		reply(w, map[string]any{"status": "success"})
	}))
	ts := httptest.NewTLSServer(mux)
	t.Cleanup(ts.Close)
	return ts
}

func hostPort(t *testing.T, raw string) (string, int) {
	u, _ := url.Parse(raw)
	host, p, _ := net.SplitHostPort(u.Host)
	port, _ := strconv.Atoi(p)
	return host, port
}

func certPin(ts *httptest.Server) string {
	sum := sha256.Sum256(ts.Certificate().Raw)
	return hex.EncodeToString(sum[:])
}

func TestFortiGateAPI(t *testing.T) {
	var restored []byte
	ts := fakeFortiGate(t, &restored)
	host, port := hostPort(t, ts.URL)
	ctx := context.Background()

	pin, err := FetchCertificate(ctx, host, port)
	if err != nil || pin != certPin(ts) {
		t.Fatalf("FetchCertificate = %q, %v", pin, err)
	}
	d := New(Config{Kind: KindFortiGate, Host: host, HTTPSPort: port, APIToken: "tok123", PinnedSHA256: pin})

	info, err := d.Probe(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if info.Serial != "FG100FTK20000001" || info.Model != "FG100F" || info.Firmware != "7.4.4" || info.Build != "2662" ||
		info.HAMode != "a-p" || len(info.HAPeers) != 1 || info.HAPeers[0] != "FG100FTK20000002" || !info.FortiManager || len(info.VDOMs) != 1 {
		t.Fatalf("info = %+v", info)
	}

	h, err := d.Health(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if h.CPU != 12 || h.Mem != 41 || h.Disk != 7 || h.Sessions != 1520 || h.Uptime != 86400 || len(h.Interfaces) != 2 ||
		h.Interfaces[1].IP != "203.0.113.2/30" || !h.Interfaces[1].Up || h.Interfaces[0].IP != "" {
		t.Fatalf("health = %+v", h)
	}

	sigs, contracts, err := d.Signatures(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(sigs) != 3 || sigs[0] != (Signature{"antivirus_db", "92.01234"}) || sigs[1] != (Signature{"ips_db", "28.00948"}) {
		t.Fatalf("signatures = %+v", sigs)
	}
	if len(contracts) != 2 || contracts[1].Name != "ips" || contracts[1].Expires == nil || contracts[1].Expires.Year() != 2027 {
		t.Fatalf("contracts = %+v", contracts)
	}

	cfg, err := d.BackupConfig(ctx)
	if err != nil || string(cfg) != sampleConfig {
		t.Fatalf("backup = %q, %v", cfg, err)
	}
	if err := d.RestoreConfig(ctx, cfg); err != nil || string(restored) != sampleConfig {
		t.Fatalf("restore = %v (%q)", err, restored)
	}
}

func TestFortiGateAPIRejects(t *testing.T) {
	ts := fakeFortiGate(t, new([]byte))
	host, port := hostPort(t, ts.URL)
	ctx := context.Background()

	d := New(Config{Kind: KindFortiGate, Host: host, HTTPSPort: port, APIToken: "tok123", PinnedSHA256: strings.Repeat("ab", 32)})
	var mismatch CertMismatchError
	if _, err := d.Probe(ctx); !errors.As(err, &mismatch) || mismatch.Got != certPin(ts) {
		t.Fatalf("wrong pin = %v", err)
	}
	d = New(Config{Kind: KindFortiGate, Host: host, HTTPSPort: port, APIToken: "wrong", PinnedSHA256: certPin(ts)})
	var he HTTPError
	if _, err := d.Probe(ctx); !errors.As(err, &he) || he.Status != http.StatusUnauthorized {
		t.Fatalf("wrong token = %v", err)
	}
	if _, err := New(Config{Kind: KindFortiGate, Host: host}).Probe(ctx); !errors.Is(err, errNoTransport) {
		t.Fatalf("no credentials = %v", err)
	}
}

func TestFortiWebAPI(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v2.0/system/status.systemstatus", func(w http.ResponseWriter, r *http.Request) {
		raw, _ := base64.StdEncoding.DecodeString(r.Header.Get("Authorization"))
		var cred map[string]string
		if json.Unmarshal(raw, &cred) != nil || cred["username"] != "admin" || cred["password"] != "pw" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"results": map[string]any{
			"serialNumber": "FV-1KD3R00000001", "hostName": "waf1", "firmwareVersion": "FortiWeb-1000D 7.04 build0562 v7.4.3,build0562", "haStatus": "Standalone"}})
	})
	mux.HandleFunc("GET /api/v2.0/system/status.monitor", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"results": map[string]any{"cpu": 5, "memory": "33", "logDisk": 2}})
	})
	ts := httptest.NewTLSServer(mux)
	defer ts.Close()
	host, port := hostPort(t, ts.URL)
	d := New(Config{Kind: KindFortiWeb, Host: host, HTTPSPort: port, APIUser: "admin", APIPass: "pw", PinnedSHA256: certPin(ts)})
	info, err := d.Probe(context.Background())
	if err != nil || info.Serial != "FV-1KD3R00000001" || info.Firmware != "7.4.3" || info.HAMode != "standalone" {
		t.Fatalf("info = %+v, %v", info, err)
	}
	h, err := d.Health(context.Background())
	if err != nil || h.CPU != 5 || h.Mem != 33 || h.Disk != 2 {
		t.Fatalf("health = %+v, %v", h, err)
	}
	if _, err := d.BackupConfig(context.Background()); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("backup = %v", err)
	}
}

const statusOutput = `Version: FortiGate-100F v7.4.4,build2662,240514 (GA.F)
Security Level: 2
Firmware Signature: certified
Virus-DB: 92.01234(2026-10-25 10:00)
Extended DB: 92.01234(2026-10-25 10:00)
AV AI/ML Model: 3.01100(2026-10-24 20:00)
IPS-DB: 28.00948(2026-10-25 01:00)
IPS-ETDB: 0.00000(2001-01-01 00:00)
APP-DB: 28.00948(2026-10-25 01:00)
Serial-Number: FG100FTK20000001
Hostname: FG-HQ
Current HA mode: a-p, primary
`

const perfOutput = `CPU states: 2% user 1% system 0% nice 96% idle 0% iowait 0% irq 1% softirq
Memory: 7999712k total, 3040000k used (38.0%), 4000000k free (50.0%), 959712k freeable (12.0%)
Average network usage: 100 / 90 kbps in 1 minute
Average sessions: 812 sessions in 1 minute, 800 sessions in 10 minutes
Uptime: 10 days,  2 hours,  33 minutes
`

func TestParseCLI(t *testing.T) {
	info := parseSystemStatus(statusOutput)
	if info.Serial != "FG100FTK20000001" || info.Model != "FortiGate-100F" || info.Firmware != "7.4.4" || info.Build != "2662" || info.HAMode != "a-p" {
		t.Fatalf("info = %+v", info)
	}
	sigs := parseSignatureLines(statusOutput)
	names := map[string]string{}
	for _, s := range sigs {
		names[s.Name] = s.Version
	}
	if names["virus_db"] != "92.01234" || names["ips_db"] != "28.00948" || names["app_db"] != "28.00948" || names["extended_db"] != "92.01234" {
		t.Fatalf("signatures = %+v", sigs)
	}
	h := parsePerformance(perfOutput)
	if h.CPU != 4 || h.Mem != 38 || h.Sessions != 812 || h.Uptime != 10*86400+2*3600+33*60 || h.Disk != -1 {
		t.Fatalf("health = %+v", h)
	}
}

// fakeSSH runs a minimal SSH server that answers exec requests.
func fakeSSH(t *testing.T, replies map[string]string) (host string, port int, hostKey string) {
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	signer, _ := ssh.NewSignerFromKey(priv)
	cfg := &ssh.ServerConfig{PasswordCallback: func(c ssh.ConnMetadata, pw []byte) (*ssh.Permissions, error) {
		if c.User() == "admin" && string(pw) == "secret" {
			return nil, nil
		}
		return nil, errors.New("denied")
	}}
	cfg.AddHostKey(signer)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			nc, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer nc.Close()
				_, chans, reqs, err := ssh.NewServerConn(nc, cfg)
				if err != nil {
					return
				}
				go ssh.DiscardRequests(reqs)
				for nch := range chans {
					ch, creqs, _ := nch.Accept()
					go func() {
						defer ch.Close()
						for req := range creqs {
							if req.Type == "shell" {
								_ = req.Reply(true, nil)
								sc := bufio.NewScanner(ch)
								for sc.Scan() {
									line := strings.TrimSpace(sc.Text())
									if line == "exit" {
										break
									}
									out := "FG # " + line + "\n"
									if r, ok := replies[line]; ok {
										out += r
									}
									_, _ = io.WriteString(ch, strings.ReplaceAll(out, "\n", "\r\n"))
								}
								_, _ = ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{0}))
								return
							}
							if req.Type != "exec" {
								_ = req.Reply(false, nil)
								continue
							}
							var p struct{ Cmd string }
							_ = ssh.Unmarshal(req.Payload, &p)
							_ = req.Reply(true, nil)
							out, ok := replies[p.Cmd]
							if !ok {
								out = "Unknown action 0\nCommand fail. Return code -1\n"
							}
							_, _ = io.WriteString(ch, strings.ReplaceAll(out, "\n", "\r\n"))
							_, _ = ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{0}))
							return
						}
					}()
				}
			}()
		}
	}()
	addr := ln.Addr().(*net.TCPAddr)
	return "127.0.0.1", addr.Port, hostKeyLine(signer.PublicKey())
}

func TestSSHDriver(t *testing.T) {
	host, port, hk := fakeSSH(t, map[string]string{
		"get system status":             statusOutput,
		"get system performance status": perfOutput,
		"show full-configuration":       "FG-HQ # " + sampleConfig,
	})
	ctx := context.Background()
	got, err := FetchSSHHostKey(ctx, host, port)
	if err != nil || got != hk {
		t.Fatalf("host key = %q, %v", got, err)
	}
	d := New(Config{Kind: KindFortiGate, Host: host, SSHPort: port, SSHUser: "admin", SSHPassword: "secret", SSHHostKey: hk})
	info, err := d.Probe(ctx)
	if err != nil || info.Serial != "FG100FTK20000001" {
		t.Fatalf("probe = %+v, %v", info, err)
	}
	cfg, err := d.BackupConfig(ctx)
	if err != nil || strings.ReplaceAll(string(cfg), "\r\n", "\n") != sampleConfig {
		t.Fatalf("backup = %q, %v", cfg, err)
	}
	if err := d.RestoreConfig(ctx, cfg); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("restore = %v", err)
	}

	_, _, other := fakeSSH(t, nil)
	d = New(Config{Kind: KindFortiGate, Host: host, SSHPort: port, SSHUser: "admin", SSHPassword: "secret", SSHHostKey: other})
	var mm HostKeyMismatchError
	if _, err := d.Probe(ctx); !errors.As(err, &mm) {
		t.Fatalf("pinned mismatch = %v", err)
	}
}

func TestFallback(t *testing.T) {
	host, port, hk := fakeSSH(t, map[string]string{"get system status": statusOutput})
	// The API points at a closed port, so SSH answers.
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	dead := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	d := New(Config{Kind: KindFortiGate, Host: host, HTTPSPort: dead, SSHPort: port, APIToken: "x", SSHUser: "admin", SSHPassword: "secret", SSHHostKey: hk})
	info, err := d.Probe(context.Background())
	if err != nil || info.Serial != "FG100FTK20000001" {
		t.Fatalf("fallback probe = %+v, %v", info, err)
	}
	if err := d.RestoreConfig(context.Background(), []byte("x")); err == nil || errors.Is(err, ErrUnsupported) {
		t.Fatalf("restore should report the API error, got %v", err)
	}
}

func TestCheckCLIOutput(t *testing.T) {
	ok := "FG-HQ # config system global\nFG-HQ (global) # set hostname x\nFG-HQ (global) # end\n"
	if err := CheckCLIOutput(ok); err != nil {
		t.Fatalf("clean output = %v", err)
	}
	bad := "FG-HQ # config firewall address\nFG-HQ (address) # delete nope\nentry not found in datasource\nCommand fail. Return code -3\n"
	var ce CLIError
	if err := CheckCLIOutput(bad); !errors.As(err, &ce) || len(ce.Lines) != 2 {
		t.Fatalf("errors = %v", err)
	}
}

func TestFortiGateInstallPackage(t *testing.T) {
	var got map[string]any
	var path string
	mux := http.NewServeMux()
	handler := func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&got)
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "success"})
	}
	mux.HandleFunc("POST /api/v2/monitor/system/fortiguard/manual-update", handler)
	mux.HandleFunc("POST /api/v2/monitor/system/firmware/upgrade", handler)
	ts := httptest.NewTLSServer(mux)
	defer ts.Close()
	host, port := hostPort(t, ts.URL)
	d := New(Config{Kind: KindFortiGate, Host: host, HTTPSPort: port, APIToken: "t", PinnedSHA256: certPin(ts)})
	inst, ok := AsInstaller(d)
	if !ok {
		t.Fatal("no installer")
	}
	if err := inst.InstallPackage(context.Background(), "ips_db", "x.pkg", strings.NewReader("IPS")); err != nil {
		t.Fatal(err)
	}
	if path != "/api/v2/monitor/system/fortiguard/manual-update" || got["file_content"] != base64.StdEncoding.EncodeToString([]byte("IPS")) {
		t.Fatalf("manual update = %s %v", path, got)
	}
	_ = inst.InstallPackage(context.Background(), "firmware", "fw.out", strings.NewReader("FW"))
	if path != "/api/v2/monitor/system/firmware/upgrade" || got["source"] != "upload" {
		t.Fatalf("firmware = %s %v", path, got)
	}
	if _, ok := AsCLI(d); ok {
		t.Fatal("API-only driver offers a CLI")
	}
}

func TestRunScript(t *testing.T) {
	host, port, hk := fakeSSH(t, map[string]string{
		"execute restore ips tftp tpX-ips.pkg 10.0.0.5": "This operation will replace the current IPS definitions.\nDo you want to continue? (y/n)",
		"y":           "Get file from tftp server OK.\n",
		"delete nope": "entry not found in datasource\nCommand fail. Return code -3\n",
	})
	d := New(Config{Kind: KindFortiGate, Host: host, SSHPort: port, SSHUser: "admin", SSHPassword: "secret", SSHHostKey: hk})
	cli, ok := AsCLI(d)
	if !ok {
		t.Fatal("no CLI")
	}
	out, err := cli.RunScript(context.Background(), "execute restore ips tftp tpX-ips.pkg 10.0.0.5\ny")
	if err != nil || !strings.Contains(out, "Get file from tftp server OK") {
		t.Fatalf("script = %q %v", out, err)
	}
	_, err = cli.RunScript(context.Background(), "config firewall address\ndelete nope\nend")
	var ce CLIError
	if !errors.As(err, &ce) {
		t.Fatalf("failing script = %v", err)
	}
}

func TestFortiWebInstallPackage(t *testing.T) {
	var got string
	mux := http.NewServeMux()
	mux.HandleFunc("POST "+FortiWebUploadPath, func(w http.ResponseWriter, r *http.Request) {
		f, h, err := r.FormFile("file")
		if err != nil {
			w.WriteHeader(400)
			return
		}
		data, _ := io.ReadAll(f)
		got = h.Filename + ":" + string(data)
	})
	ts := httptest.NewTLSServer(mux)
	defer ts.Close()
	host, port := hostPort(t, ts.URL)
	d := New(Config{Kind: KindFortiWeb, Host: host, HTTPSPort: port, APIUser: "admin", APIPass: "pw", PinnedSHA256: certPin(ts)})
	inst, ok := AsInstaller(d)
	if !ok {
		t.Fatal("no installer")
	}
	if err := inst.InstallPackage(context.Background(), "waf_signature", "waf.pkg", strings.NewReader("WAF")); err != nil || got != "waf.pkg:WAF" {
		t.Fatalf("upload = %q %v", got, err)
	}
	if err := inst.InstallPackage(context.Background(), "firmware", "fw.out", strings.NewReader("x")); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("firmware = %v", err)
	}
}
