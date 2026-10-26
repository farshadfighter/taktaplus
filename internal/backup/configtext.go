package backup

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"sort"
	"strings"
)

// volatile lines change on every save without a configuration change.
var volatile = regexp.MustCompile(`^#(conf_file_ver|buildno|global_vdom)=`)

// normalize strips volatile header lines and line-ending differences.
func normalize(cfg []byte) string {
	lines := strings.Split(strings.ReplaceAll(string(cfg), "\r\n", "\n"), "\n")
	out := lines[:0]
	for _, l := range lines {
		if !volatile.MatchString(l) {
			out = append(out, strings.TrimRight(l, " \t"))
		}
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

// Fingerprint is the SHA-256 of the normalized configuration; equal
// fingerprints mean nothing changed.
func Fingerprint(cfg []byte) string {
	sum := sha256.Sum256([]byte(normalize(cfg)))
	return hex.EncodeToString(sum[:])
}

var secretSetting = regexp.MustCompile(`^(\s*set\s+\S*(?:passw|secret|psk|key|community|token)\S*\s+)(.+)$`)
var encValue = regexp.MustCompile(`ENC\s+\S+`)

// Mask hides passwords, keys and ENC values for display.
func Mask(cfg string) string {
	lines := strings.Split(cfg, "\n")
	for i, l := range lines {
		if m := secretSetting.FindStringSubmatch(l); m != nil {
			lines[i] = m[1] + "********"
			continue
		}
		lines[i] = encValue.ReplaceAllString(l, "ENC ********")
	}
	return strings.Join(lines, "\n")
}

// Change is one difference between two configurations.
type Change struct {
	Path string `json:"path"` // e.g. config firewall policy > edit 12
	Kind string `json:"kind"` // added, removed, changed
	Key  string `json:"key"`  // setting name; empty for a whole block
	Old  string `json:"old,omitempty"`
	New  string `json:"new,omitempty"`
}

// flatten maps every "set"/"unset" line to its block path. Blocks are
// "config X" and "edit Y" lines; FortiOS and FortiWeb share this syntax.
func flatten(cfg string) (settings map[string]map[string]string, blocks []string) {
	settings = map[string]map[string]string{}
	var stack []string
	seen := map[string]bool{}
	path := func() string { return strings.Join(stack, " > ") }
	for _, raw := range strings.Split(normalize([]byte(cfg)), "\n") {
		l := strings.TrimSpace(raw)
		switch {
		case l == "" || strings.HasPrefix(l, "#"):
		case strings.HasPrefix(l, "config "), strings.HasPrefix(l, "edit "):
			stack = append(stack, l)
			p := path()
			if !seen[p] {
				seen[p] = true
				blocks = append(blocks, p)
				settings[p] = map[string]string{}
			}
		case l == "end" || l == "next":
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		default:
			p := path()
			if settings[p] == nil {
				settings[p] = map[string]string{}
				if !seen[p] {
					seen[p] = true
					blocks = append(blocks, p)
				}
			}
			fields := strings.Fields(l)
			key := l
			if len(fields) >= 2 && (fields[0] == "set" || fields[0] == "unset") {
				key = fields[1]
			}
			settings[p][key] = l
		}
	}
	return settings, blocks
}

// Diff compares two configurations block by block. Values are masked.
func Diff(oldCfg, newCfg string) []Change {
	oldS, oldBlocks := flatten(oldCfg)
	newS, newBlocks := flatten(newCfg)
	var out []Change
	order := append([]string{}, newBlocks...)
	for _, b := range oldBlocks {
		if _, ok := newS[b]; !ok {
			order = append(order, b)
		}
	}
	for _, p := range order {
		o, inOld := oldS[p]
		n, inNew := newS[p]
		switch {
		case !inOld:
			if !parentAdded(p, oldS) {
				out = append(out, Change{Path: p, Kind: "added"})
			}
			continue
		case !inNew:
			if !parentRemoved(p, newS) {
				out = append(out, Change{Path: p, Kind: "removed"})
			}
			continue
		}
		keys := map[string]bool{}
		for k := range o {
			keys[k] = true
		}
		for k := range n {
			keys[k] = true
		}
		sorted := make([]string, 0, len(keys))
		for k := range keys {
			sorted = append(sorted, k)
		}
		sort.Strings(sorted)
		for _, k := range sorted {
			ov, hasO := o[k]
			nv, hasN := n[k]
			switch {
			case hasO && !hasN:
				out = append(out, Change{Path: p, Kind: "removed", Key: k, Old: Mask(ov)})
			case !hasO && hasN:
				out = append(out, Change{Path: p, Kind: "added", Key: k, New: Mask(nv)})
			case ov != nv:
				out = append(out, Change{Path: p, Kind: "changed", Key: k, Old: Mask(ov), New: Mask(nv)})
			}
		}
	}
	return out
}

// parentAdded reports whether p's parent block is missing from old too, so
// only the topmost new block is listed.
func parentAdded(p string, old map[string]map[string]string) bool {
	i := strings.LastIndex(p, " > ")
	if i < 0 {
		return false
	}
	_, ok := old[p[:i]]
	return !ok
}

func parentRemoved(p string, cur map[string]map[string]string) bool { return parentAdded(p, cur) }
