// Package compliance checks device configurations against templates
// (drift) and rules, scores each device and opens changes to fix drift.
package compliance

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/farshadfighter/taktaplus/internal/fortiosconf"
)

// Condition operators.
var ops = []string{"eq", "ne", "contains", "not_contains", "gte", "lte", "set", "unset"}

// Cond is one test on a setting. Default is the value FortiOS uses when the
// setting does not appear in the configuration.
type Cond struct {
	Key     string `json:"key"`
	Op      string `json:"op"`
	Value   string `json:"value"`
	Default string `json:"default,omitempty"`
}

func (c Cond) String() string {
	op := map[string]string{"eq": "=", "ne": "≠", "contains": "شامل", "not_contains": "شامل نباشد", "gte": "≥", "lte": "≤", "set": "تنظیم شده", "unset": "تنظیم نشده"}[c.Op]
	if c.Op == "set" || c.Op == "unset" {
		return c.Key + " " + op
	}
	return fmt.Sprintf("%s %s %s", c.Key, op, c.Value)
}

func (c Cond) validate() error {
	if strings.TrimSpace(c.Key) == "" || strings.ContainsAny(c.Key, " \t\"") {
		return invalidf("each condition needs a setting name")
	}
	if !slices.Contains(ops, c.Op) {
		return invalidf("unknown operator %q", c.Op)
	}
	if c.Op == "gte" || c.Op == "lte" {
		if _, err := strconv.ParseFloat(c.Value, 64); err != nil {
			return invalidf("%s needs a number", c.Key)
		}
	}
	return nil
}

// holds evaluates the condition on a section or entry.
func (c Cond) holds(n *fortiosconf.Node) (bool, string) {
	vals, ok := n.Values[c.Key]
	actual := strings.Join(vals, " ")
	if !ok {
		actual = c.Default
		vals = strings.Fields(c.Default)
	}
	switch c.Op {
	case "eq":
		return strings.EqualFold(actual, c.Value), actual
	case "ne":
		return !strings.EqualFold(actual, c.Value), actual
	case "contains", "not_contains":
		has := slices.ContainsFunc(vals, func(v string) bool { return strings.EqualFold(v, c.Value) })
		return has == (c.Op == "contains"), actual
	case "gte", "lte":
		a, err1 := strconv.ParseFloat(actual, 64)
		b, err2 := strconv.ParseFloat(c.Value, 64)
		if err1 != nil || err2 != nil {
			return false, actual
		}
		return (c.Op == "gte" && a >= b) || (c.Op == "lte" && a <= b), actual
	case "set":
		return ok, actual
	case "unset":
		return !ok, actual
	}
	return false, actual
}

// Rule is a compliance rule over one configuration section.
type Rule struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Severity    string `json:"severity"` // critical, high, medium, low
	Platform    string `json:"platform"`
	GroupID     *int64 `json:"group_id"`
	Section     string `json:"section"`
	Scope       string `json:"scope"`   // section: the section's own settings; each: every entry
	Match       []Cond `json:"match"`   // entries the rule applies to (each); all when empty
	Require     []Cond `json:"require"` // what must hold; empty means matching entries are forbidden
	Builtin     bool   `json:"builtin"`
	Enabled     bool   `json:"enabled"`
}

var severityPenalty = map[string]int{"critical": 25, "high": 15, "medium": 8, "low": 3}

func (r *Rule) validate() error {
	r.Name = strings.TrimSpace(r.Name)
	r.Section = strings.Join(strings.Fields(r.Section), " ")
	if r.Name == "" || len(r.Name) > 200 {
		return invalidf("rule name is required")
	}
	if _, ok := severityPenalty[r.Severity]; !ok {
		return invalidf("severity must be critical, high, medium or low")
	}
	if r.Platform != "fortigate" && r.Platform != "fortiweb" {
		return invalidf("platform must be fortigate or fortiweb")
	}
	if r.Section == "" {
		return invalidf("configuration section is required, such as \"system global\"")
	}
	if r.Scope != "section" && r.Scope != "each" {
		return invalidf("scope must be section or each")
	}
	if r.Scope == "section" && len(r.Match) > 0 {
		return invalidf("entry filters apply only to rules over each entry")
	}
	if len(r.Require) == 0 && (r.Scope == "section" || len(r.Match) == 0) {
		return invalidf("add conditions that must hold, or (for each entry) conditions of forbidden entries")
	}
	if r.Match == nil {
		r.Match = []Cond{}
	}
	if r.Require == nil {
		r.Require = []Cond{}
	}
	for _, c := range append(slices.Clone(r.Match), r.Require...) {
		if err := c.validate(); err != nil {
			return err
		}
	}
	return nil
}

// Finding is one place a rule is broken.
type Finding struct {
	RuleID   int64  `json:"rule_id"`
	Rule     string `json:"rule"`
	Severity string `json:"severity"`
	Entry    string `json:"entry"` // "vdom / entry", or "" for a section rule
	Detail   string `json:"detail"`
	Excepted bool   `json:"excepted"`
}

func (f Finding) ref() string { return fmt.Sprintf("rule:%d:%s", f.RuleID, f.Entry) }

// Check evaluates the rule on a parsed configuration.
func (r *Rule) Check(root *fortiosconf.Node) []Finding {
	var out []Finding
	finding := func(vdom, entry, detail string) {
		name := entry
		if vdom != "" {
			name = vdom + " / " + entry
			if entry == "" {
				name = vdom
			}
		}
		out = append(out, Finding{RuleID: r.ID, Rule: r.Name, Severity: r.Severity, Entry: name, Detail: detail})
	}
	secs := root.Sections(r.Section)
	if r.Scope == "section" {
		if len(secs) == 0 {
			// Absent section: every setting has its default.
			secs = []fortiosconf.Scoped{{Node: &fortiosconf.Node{Values: map[string][]string{}}}}
		}
		for _, s := range secs {
			if d := failing(r.Require, s.Node); d != "" {
				finding(s.VDOM, "", d)
			}
		}
		return out
	}
	for _, s := range secs {
		for _, e := range s.Node.Children {
			if !e.Entry || failing(r.Match, e) != "" {
				continue
			}
			if len(r.Require) == 0 {
				finding(s.VDOM, e.Name, "مطابق شرط ممنوع: "+condText(r.Match))
			} else if d := failing(r.Require, e); d != "" {
				finding(s.VDOM, e.Name, d)
			}
		}
	}
	return out
}

// failing returns a description of the first condition that does not
// hold, or "".
func failing(conds []Cond, n *fortiosconf.Node) string {
	for _, c := range conds {
		if ok, actual := c.holds(n); !ok {
			if actual == "" {
				actual = "خالی"
			}
			return fmt.Sprintf("%s (مقدار فعلی: %s)", c, actual)
		}
	}
	return ""
}

func condText(conds []Cond) string {
	parts := make([]string, len(conds))
	for i, c := range conds {
		parts[i] = c.String()
	}
	return strings.Join(parts, "، ")
}
