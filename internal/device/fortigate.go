package device

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

// fortiGateAPI implements Driver over the FortiOS REST API (v2) with an API
// token (System > Administrators > REST API Admin).
type fortiGateAPI struct{ rc *restClient }

func newFortiGateAPI(c Config) Driver {
	rc := newRESTClient(c)
	token := c.APIToken
	rc.auth = func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+token) }
	return &fortiGateAPI{rc: rc}
}

// fgResp is the FortiOS response envelope.
type fgResp[T any] struct {
	Results T      `json:"results"`
	Serial  string `json:"serial"`
	Version string `json:"version"`
	Build   num    `json:"build"`
	Status  string `json:"status"`
}

// objectOrFirst decodes a cmdb result that is an object on some firmware
// versions and a one-element array on others.
type objectOrFirst[T any] struct{ V T }

func (o *objectOrFirst[T]) UnmarshalJSON(b []byte) error {
	if len(b) > 0 && b[0] == '[' {
		var list []T
		if err := json.Unmarshal(b, &list); err != nil {
			return err
		}
		if len(list) > 0 {
			o.V = list[0]
		}
		return nil
	}
	return json.Unmarshal(b, &o.V)
}

func (f *fortiGateAPI) Probe(ctx context.Context) (Info, error) {
	var st fgResp[struct {
		Model       string `json:"model"`
		ModelName   string `json:"model_name"`
		ModelNumber string `json:"model_number"`
		Hostname    string `json:"hostname"`
	}]
	if err := f.rc.getJSON(ctx, "/api/v2/monitor/system/status", &st); err != nil {
		return Info{}, err
	}
	info := Info{
		Serial:   st.Serial,
		Model:    st.Results.Model,
		Firmware: strings.TrimPrefix(st.Version, "v"),
		Hostname: st.Results.Hostname,
		HAMode:   "standalone",
	}
	if st.Build > 0 {
		info.Build = strconv.Itoa(int(st.Build))
	}
	if info.Model == "" && st.Results.ModelNumber != "" {
		info.Model = "FG" + st.Results.ModelNumber
	}

	// The rest is best effort: an API profile may not grant every scope.
	var ha fgResp[objectOrFirst[struct {
		Mode string `json:"mode"`
	}]]
	if f.rc.getJSON(ctx, "/api/v2/cmdb/system/ha", &ha) == nil && ha.Results.V.Mode != "" {
		info.HAMode = ha.Results.V.Mode
	}
	if info.HAMode != "standalone" {
		var peers fgResp[[]struct {
			Serial string `json:"serial_no"`
		}]
		if f.rc.getJSON(ctx, "/api/v2/monitor/system/ha-peer", &peers) == nil {
			for _, p := range peers.Results {
				if p.Serial != "" && p.Serial != info.Serial {
					info.HAPeers = append(info.HAPeers, p.Serial)
				}
			}
		}
	}
	var vdoms fgResp[[]struct {
		Name string `json:"name"`
	}]
	if f.rc.getJSON(ctx, "/api/v2/cmdb/system/vdom", &vdoms) == nil {
		for _, v := range vdoms.Results {
			info.VDOMs = append(info.VDOMs, v.Name)
		}
	}
	var cm fgResp[objectOrFirst[struct {
		Type string `json:"type"`
		FMG  string `json:"fmg"`
	}]]
	if f.rc.getJSON(ctx, "/api/v2/cmdb/system/central-management", &cm) == nil {
		info.FortiManager = cm.Results.V.Type == "fortimanager" && strings.Trim(cm.Results.V.FMG, `" `) != ""
	}
	return info, nil
}

func (f *fortiGateAPI) Health(ctx context.Context) (Health, error) {
	type sample struct {
		Current num `json:"current"`
	}
	var usage fgResp[map[string][]sample]
	if err := f.rc.getJSON(ctx, "/api/v2/monitor/system/resource/usage?interval=1-min&scope=global", &usage); err != nil {
		return Health{}, err
	}
	cur := func(name string) (float64, bool) {
		if s := usage.Results[name]; len(s) > 0 {
			return float64(s[0].Current), true
		}
		return 0, false
	}
	h := Health{Disk: -1}
	h.CPU, _ = cur("cpu")
	h.Mem, _ = cur("mem")
	if d, ok := cur("disk"); ok {
		h.Disk = d
	}
	if s, ok := cur("session"); ok {
		h.Sessions = int64(s)
	}

	var ui fgResp[struct {
		Snapshot   num `json:"snapshot_utc_time"`
		LastReboot num `json:"utc_last_reboot"`
	}]
	if f.rc.getJSON(ctx, "/api/v2/monitor/web-ui/state", &ui) == nil && ui.Results.LastReboot > 0 && ui.Results.Snapshot > ui.Results.LastReboot {
		h.Uptime = int64(ui.Results.Snapshot-ui.Results.LastReboot) / 1000
	}

	var ifs fgResp[map[string]struct {
		Name    string `json:"name"`
		Alias   string `json:"alias"`
		IP      string `json:"ip"`
		Mask    num    `json:"mask"`
		Link    bool   `json:"link"`
		Speed   num    `json:"speed"`
		RxBytes num    `json:"rx_bytes"`
		TxBytes num    `json:"tx_bytes"`
	}]
	if f.rc.getJSON(ctx, "/api/v2/monitor/system/interface?scope=global", &ifs) == nil {
		for key, i := range ifs.Results {
			name := i.Name
			if name == "" {
				name = key
			}
			ip := i.IP
			if ip == "0.0.0.0" {
				ip = ""
			} else if ip != "" && i.Mask > 0 {
				ip += "/" + strconv.Itoa(int(i.Mask))
			}
			h.Interfaces = append(h.Interfaces, Interface{
				Name: name, Alias: i.Alias, IP: ip, Up: i.Link, Speed: int(i.Speed),
				RxBytes: int64(i.RxBytes), TxBytes: int64(i.TxBytes),
			})
		}
		sort.Slice(h.Interfaces, func(a, b int) bool { return h.Interfaces[a].Name < h.Interfaces[b].Name })
	}
	return h, nil
}

// Signatures reads /monitor/license/status, whose shape varies between
// releases: each service is an object with a status, an expiry (Unix
// seconds) and nested objects (db, engine, …) carrying a version.
func (f *fortiGateAPI) Signatures(ctx context.Context) ([]Signature, []Contract, error) {
	var lic fgResp[map[string]json.RawMessage]
	if err := f.rc.getJSON(ctx, "/api/v2/monitor/license/status", &lic); err != nil {
		return nil, nil, err
	}
	sigs, contracts := parseLicenseStatus(lic.Results)
	return sigs, contracts, nil
}

func parseLicenseStatus(services map[string]json.RawMessage) ([]Signature, []Contract) {
	var sigs []Signature
	var contracts []Contract
	for name, raw := range services {
		var svc map[string]json.RawMessage
		if json.Unmarshal(raw, &svc) != nil {
			continue
		}
		var status string
		_ = json.Unmarshal(svc["status"], &status)
		var expires num
		_ = json.Unmarshal(svc["expires"], &expires)
		if status != "" && name != "vm" {
			c := Contract{Name: name, Status: status}
			if expires > 0 {
				t := time.Unix(int64(expires), 0).UTC()
				c.Expires = &t
			}
			contracts = append(contracts, c)
		}
		for sub, subRaw := range svc {
			var part struct {
				Version string `json:"version"`
			}
			if json.Unmarshal(subRaw, &part) == nil && part.Version != "" {
				sigs = append(sigs, Signature{Name: name + "_" + sub, Version: part.Version})
			}
		}
	}
	sort.Slice(sigs, func(a, b int) bool { return sigs[a].Name < sigs[b].Name })
	sort.Slice(contracts, func(a, b int) bool { return contracts[a].Name < contracts[b].Name })
	return sigs, contracts
}

func (f *fortiGateAPI) BackupConfig(ctx context.Context) ([]byte, error) {
	data, err := f.rc.do(ctx, http.MethodGet, "/api/v2/monitor/system/config/backup?scope=global", nil)
	var he HTTPError
	if errors.As(err, &he) && (he.Status == http.StatusMethodNotAllowed || he.Status == http.StatusNotFound) {
		// FortiOS 7.6 only accepts POST here.
		data, err = f.rc.do(ctx, http.MethodPost, "/api/v2/monitor/system/config/backup",
			map[string]string{"destination": "file", "scope": "global"})
	}
	if err != nil {
		return nil, err
	}
	if !looksLikeConfig(data) {
		return nil, errors.New("device API: backup did not return a configuration file")
	}
	return data, nil
}

// looksLikeConfig reports whether data is a FortiOS/FortiWeb configuration
// file rather than a JSON error envelope.
func looksLikeConfig(data []byte) bool {
	s := strings.TrimSpace(string(data[:min(len(data), 4096)]))
	return strings.HasPrefix(s, "#config-version=") || strings.HasPrefix(s, "config ")
}

func (f *fortiGateAPI) RestoreConfig(ctx context.Context, cfg []byte) error {
	_, err := f.rc.do(ctx, http.MethodPost, "/api/v2/monitor/system/config/restore", map[string]string{
		"source": "upload", "scope": "global", "file_content": base64.StdEncoding.EncodeToString(cfg),
	})
	return err
}
