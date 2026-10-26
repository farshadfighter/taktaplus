package device

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// fortiWebAPI implements Driver over the FortiWeb REST API (v2.0). FortiWeb
// authenticates each request with a base64 JSON {username, password, vdom}
// in the Authorization header.
//
// FortiWeb's API is less uniform across releases than FortiOS's, so fields
// are matched by several candidate names. Operations it cannot do over REST
// return ErrUnsupported and fall back to SSH.
type fortiWebAPI struct{ rc *restClient }

func newFortiWebAPI(c Config) Driver {
	rc := newRESTClient(c)
	cred, _ := json.Marshal(map[string]string{"username": c.APIUser, "password": c.APIPass, "vdom": "root"})
	header := base64.StdEncoding.EncodeToString(cred)
	if c.APIToken != "" && c.APIUser == "" {
		header = c.APIToken // a pre-encoded token
	}
	rc.auth = func(r *http.Request) { r.Header.Set("Authorization", header) }
	return &fortiWebAPI{rc: rc}
}

// results fetches path and returns its "results" object with lower-cased,
// separator-free keys, so serialNumber, serial_number and serial-number all
// read as "serialnumber".
func (f *fortiWebAPI) results(ctx context.Context, path string) (map[string]json.RawMessage, error) {
	var env struct {
		Results json.RawMessage `json:"results"`
	}
	if err := f.rc.getJSON(ctx, path, &env); err != nil {
		return nil, err
	}
	raw := env.Results
	if len(raw) > 0 && raw[0] == '[' {
		var list []json.RawMessage
		if err := json.Unmarshal(raw, &list); err != nil || len(list) == 0 {
			return map[string]json.RawMessage{}, nil
		}
		raw = list[0]
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return map[string]json.RawMessage{}, nil
	}
	out := make(map[string]json.RawMessage, len(m))
	for k, v := range m {
		out[normKey(k)] = v
	}
	return out, nil
}

func normKey(k string) string {
	return strings.NewReplacer("_", "", "-", "", " ", "").Replace(strings.ToLower(k))
}

func pickString(m map[string]json.RawMessage, names ...string) string {
	for _, n := range names {
		if raw, ok := m[normKey(n)]; ok {
			var s string
			if json.Unmarshal(raw, &s) == nil && s != "" {
				return s
			}
			var f num
			if json.Unmarshal(raw, &f) == nil && f != 0 {
				return strconv.FormatFloat(float64(f), 'f', -1, 64)
			}
		}
	}
	return ""
}

func pickNum(m map[string]json.RawMessage, names ...string) (float64, bool) {
	for _, n := range names {
		if raw, ok := m[normKey(n)]; ok {
			var f num
			if json.Unmarshal(raw, &f) == nil {
				return float64(f), true
			}
		}
	}
	return 0, false
}

var fwVersion = regexp.MustCompile(`v?(\d+\.\d+\.\d+)(?:,\s*build\s*(\d+))?`)

func (f *fortiWebAPI) Probe(ctx context.Context) (Info, error) {
	st, err := f.results(ctx, "/api/v2.0/system/status.systemstatus")
	if err != nil {
		return Info{}, err
	}
	info := Info{
		Serial:   pickString(st, "serialNumber", "serial"),
		Model:    pickString(st, "model", "productName"),
		Hostname: pickString(st, "hostName", "hostname"),
		HAMode:   "standalone",
	}
	if m := fwVersion.FindStringSubmatch(pickString(st, "firmwareVersion", "version")); m != nil {
		info.Firmware, info.Build = m[1], m[2]
	}
	if ha := strings.ToLower(pickString(st, "haStatus", "haMode")); ha != "" && !strings.Contains(ha, "standalone") {
		info.HAMode = ha
	}
	return info, nil
}

func (f *fortiWebAPI) Health(ctx context.Context) (Health, error) {
	m, err := f.results(ctx, "/api/v2.0/system/status.monitor")
	if err != nil {
		return Health{}, err
	}
	h := Health{Disk: -1}
	h.CPU, _ = pickNum(m, "cpu", "cpuUsage")
	h.Mem, _ = pickNum(m, "memory", "memoryUsage", "mem")
	if d, ok := pickNum(m, "logDisk", "logDiskUsage", "disk"); ok {
		h.Disk = d
	}
	if s, ok := pickNum(m, "tcpConcurrentConnection", "connections", "sessions"); ok {
		h.Sessions = int64(s)
	}
	if u, ok := pickNum(m, "upTime", "uptime"); ok {
		h.Uptime = int64(u)
	}
	return h, nil
}

func (f *fortiWebAPI) Signatures(ctx context.Context) ([]Signature, []Contract, error) {
	m, err := f.results(ctx, "/api/v2.0/system/status.licenseinformation")
	if err != nil {
		return nil, nil, err
	}
	var sigs []Signature
	var contracts []Contract
	for k, raw := range m {
		var obj map[string]json.RawMessage
		if json.Unmarshal(raw, &obj) != nil {
			continue
		}
		norm := map[string]json.RawMessage{}
		for kk, v := range obj {
			norm[normKey(kk)] = v
		}
		if v := pickString(norm, "version", "signatureVersion"); v != "" {
			sigs = append(sigs, Signature{Name: k, Version: v})
		}
		if s := pickString(norm, "status", "licenseStatus"); s != "" {
			c := Contract{Name: k, Status: strings.ToLower(s)}
			if e := pickString(norm, "expiryDate", "expires", "expiration"); e != "" {
				for _, layout := range []string{"2006-01-02", "2006/01/02", "Mon Jan 2 2006", time.RFC3339} {
					if t, err := time.Parse(layout, e); err == nil {
						c.Expires = &t
						break
					}
				}
			}
			contracts = append(contracts, c)
		}
	}
	sort.Slice(sigs, func(a, b int) bool { return sigs[a].Name < sigs[b].Name })
	sort.Slice(contracts, func(a, b int) bool { return contracts[a].Name < contracts[b].Name })
	return sigs, contracts, nil
}

// BackupConfig is not exposed uniformly by the FortiWeb REST API; the SSH
// transport takes over.
func (f *fortiWebAPI) BackupConfig(context.Context) ([]byte, error) { return nil, ErrUnsupported }

func (f *fortiWebAPI) RestoreConfig(context.Context, []byte) error { return ErrUnsupported }
