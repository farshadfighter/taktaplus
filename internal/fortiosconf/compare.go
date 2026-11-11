package fortiosconf

import (
	"slices"
	"strings"
)

// Difference is a place where a configuration departs from a template.
type Difference struct {
	Path     string `json:"path"`              // "system snmp community > 1"
	Key      string `json:"key,omitempty"`     // empty when the section or entry is missing
	Expected string `json:"expected"`          // template value ("" for unset)
	Actual   string `json:"actual"`            // device value
	Missing  bool   `json:"missing,omitempty"` // the section or entry does not exist
}

// Compare lists what the template sets that the configuration does not
// match. Only what the template mentions is compared; other settings and
// entries are the device's own. Secret values (ENC …) are not compared.
func Compare(template, config *Node) []Difference {
	var out []Difference
	for _, sec := range template.Children {
		have := config.Section(sec.Name)
		out = append(out, compareNode(sec, have, sec.Name)...)
	}
	return out
}

func compareNode(want, have *Node, path string) []Difference {
	if have == nil {
		return []Difference{{Path: path, Missing: true, Expected: summary(want)}}
	}
	var out []Difference
	for _, k := range want.Keys {
		exp := strings.Join(want.Values[k], " ")
		vals, ok := have.Values[k]
		act := strings.Join(vals, " ")
		if ok && len(vals) > 0 && vals[0] == "ENC" {
			continue
		}
		if !ok || !sameValue(want.Values[k], vals) {
			out = append(out, Difference{Path: path, Key: k, Expected: exp, Actual: act})
		}
	}
	for k := range want.Unset {
		if v, ok := have.Values[k]; ok {
			out = append(out, Difference{Path: path, Key: k, Expected: "", Actual: strings.Join(v, " ")})
		}
	}
	for _, c := range want.Children {
		out = append(out, compareNode(c, have.Child(c.Name), path+" > "+c.Name)...)
	}
	return out
}

// sameValue compares settings; multi-value lists ignore order.
func sameValue(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	if len(a) <= 1 {
		return slices.Equal(a, b)
	}
	x, y := slices.Clone(a), slices.Clone(b)
	slices.Sort(x)
	slices.Sort(y)
	return slices.Equal(x, y)
}

func summary(n *Node) string {
	var parts []string
	for _, k := range n.Keys {
		parts = append(parts, k+"="+strings.Join(n.Values[k], " "))
	}
	if len(n.Children) > 0 {
		parts = append(parts, "…")
	}
	return strings.Join(parts, ", ")
}
