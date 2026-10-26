package packages

import (
	"regexp"
	"strings"
	"time"
)

var (
	versionRe = regexp.MustCompile(`(\d+)\.(\d+)(?:\.(\d+))?`)
	buildRe   = regexp.MustCompile(`(?i)v(\d)-build(\d+)`)
)

// Guess derives a description from a file name, for files that come
// without a signed manifest. Administrators correct it in the panel.
func Guess(file string) Meta {
	name := strings.ToLower(file)
	m := Meta{Platform: "fortigate", Type: "ips_db", Models: []string{"*"}, ReleasedAt: time.Now()}
	if strings.HasPrefix(name, "fwb") || strings.HasPrefix(name, "fv") || strings.Contains(name, "fortiweb") || strings.Contains(name, "waf") {
		m.Platform = "fortiweb"
		m.Type = "waf_signature"
	}
	has := func(words ...string) bool {
		for _, w := range words {
			if strings.Contains(name, w) {
				return true
			}
		}
		return false
	}
	switch {
	case strings.HasSuffix(name, ".out") || has("firmware", "image"):
		m.Type = "firmware"
	case has("isdb", "internet-service", "ffdb"):
		m.Type = "isdb"
	case has("botnet"):
		m.Type = "botnet_db"
	case has("appctrl", "app-ctrl", "app_db", "appdb", "flen"):
		m.Type = "appctrl_db"
	case has("ot_db", "otdb", "ot-db", "icsdb"):
		m.Type = "ot_db"
	case has("ipsengine", "ips-engine", "ips_engine", "ipse"):
		m.Type = "ips_engine"
	case has("avengine", "av-engine", "av_engine", "vsigupdate-engine"):
		m.Type = "av_engine"
	case has("antivirus", "virus", "vsig", "avdb", "av_db", "av-db", "etdb", "mmdb"):
		m.Type = "av_db"
	case has("geo"):
		m.Type = "geo_db"
	case has("reputation", "iprep", "ip-rep"):
		m.Type = "ip_reputation"
	case has("ips", "nids"):
		m.Type = "ips_db"
	}
	if m.Platform == "fortiweb" && (m.Type == "ips_db" || m.Type == "ips_engine" || m.Type == "appctrl_db" || m.Type == "botnet_db" || m.Type == "isdb" || m.Type == "ot_db") {
		m.Type = "waf_signature"
	}
	if b := buildRe.FindStringSubmatch(name); b != nil && m.Type == "firmware" {
		// FGT_100F-v7-build2662-FORTINET.out: major version and build only.
		m.Version = b[1] + ".0." + strings.TrimLeft(b[2], "0")
		m.Notes = "build " + b[2] + "; set the exact version"
	} else if v := versionRe.FindAllString(name, -1); len(v) > 0 {
		m.Version = v[len(v)-1]
	} else {
		m.Version = "0.0"
	}
	return m
}
