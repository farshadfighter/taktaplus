// Package fortiosconf parses FortiOS and FortiWeb configuration text
// ("config / edit / set / next / end") into a tree, for drift detection and
// compliance rules.
package fortiosconf

import (
	"strings"
)

// Node is a "config" section or an "edit" entry. The root node holds the
// top-level sections.
type Node struct {
	Name     string              // section path ("system global") or entry name ("1", "port1")
	Entry    bool                // an "edit" entry
	Values   map[string][]string // "set" key → values
	Keys     []string            // set keys in file order
	Unset    map[string]bool     // explicit "unset" keys
	Children []*Node             // sections inside, or the entries of a section
}

func newNode(name string, entry bool) *Node {
	return &Node{Name: name, Entry: entry, Values: map[string][]string{}, Unset: map[string]bool{}}
}

// Child returns the first child named name.
func (n *Node) Child(name string) *Node {
	for _, c := range n.Children {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// Section finds a section by path, such as "system global" or
// "firewall policy". With VDOMs the section is looked up in the global
// part first and then in the first VDOM that has it.
func (n *Node) Section(path string) *Node {
	if c := n.Child(path); c != nil {
		return c
	}
	if g := n.Child("global"); g != nil {
		if c := g.Child(path); c != nil {
			return c
		}
	}
	if v := n.Child("vdom"); v != nil {
		for _, e := range v.Children {
			if c := e.Child(path); c != nil {
				return c
			}
		}
	}
	return nil
}

// Sections returns every instance of a section: one per VDOM in multi-VDOM
// configurations, with the VDOM name ("" outside VDOMs).
func (n *Node) Sections(path string) []Scoped {
	var out []Scoped
	if c := n.Child(path); c != nil {
		out = append(out, Scoped{Node: c})
	}
	if g := n.Child("global"); g != nil {
		if c := g.Child(path); c != nil {
			out = append(out, Scoped{Node: c})
		}
	}
	if v := n.Child("vdom"); v != nil {
		for _, e := range v.Children {
			if c := e.Child(path); c != nil {
				out = append(out, Scoped{VDOM: e.Name, Node: c})
			}
		}
	}
	return out
}

// Scoped is a section with the VDOM it belongs to.
type Scoped struct {
	VDOM string
	Node *Node
}

// Get returns the value of a setting joined by spaces, and whether it is
// set.
func (n *Node) Get(key string) (string, bool) {
	v, ok := n.Values[key]
	return strings.Join(v, " "), ok
}

// Parse reads configuration text. It is lenient: unknown lines are
// skipped and unbalanced blocks are closed at the end.
func Parse(text string) *Node {
	root := newNode("", false)
	stack := []*Node{root}
	top := func() *Node { return stack[len(stack)-1] }
	for _, line := range logicalLines(text) {
		tok := tokens(line)
		if len(tok) == 0 || strings.HasPrefix(tok[0], "#") {
			continue
		}
		switch tok[0] {
		case "config":
			if len(tok) < 2 {
				continue
			}
			name := strings.Join(tok[1:], " ")
			parent := top()
			// FortiOS repeats "config vdom" blocks; merge them.
			n := parent.Child(name)
			if n == nil || n.Entry {
				n = newNode(name, false)
				parent.Children = append(parent.Children, n)
			}
			stack = append(stack, n)
		case "edit":
			if len(tok) < 2 {
				continue
			}
			parent := top()
			n := parent.Child(tok[1])
			if n == nil {
				n = newNode(tok[1], true)
				parent.Children = append(parent.Children, n)
			}
			stack = append(stack, n)
		case "next", "end":
			if len(stack) > 1 {
				stack = stack[:len(stack)-1]
			}
		case "set":
			if len(tok) < 2 {
				continue
			}
			n := top()
			if _, ok := n.Values[tok[1]]; !ok {
				n.Keys = append(n.Keys, tok[1])
			}
			n.Values[tok[1]] = tok[2:]
			delete(n.Unset, tok[1])
		case "unset":
			if len(tok) < 2 {
				continue
			}
			n := top()
			n.Unset[tok[1]] = true
			delete(n.Values, tok[1])
		}
	}
	return root
}

// logicalLines splits text into lines, joining lines inside an open
// double-quoted string (certificates, banners).
func logicalLines(text string) []string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	var out []string
	var cur strings.Builder
	inQuote := false
	for _, raw := range strings.Split(text, "\n") {
		if inQuote {
			cur.WriteByte('\n')
		}
		cur.WriteString(raw)
		escaped := false
		for i := 0; i < len(raw); i++ {
			switch {
			case escaped:
				escaped = false
			case raw[i] == '\\':
				escaped = true
			case raw[i] == '"':
				inQuote = !inQuote
			}
		}
		if !inQuote {
			out = append(out, strings.TrimSpace(cur.String()))
			cur.Reset()
		}
	}
	if cur.Len() > 0 {
		out = append(out, strings.TrimSpace(cur.String()))
	}
	return out
}

// tokens splits a line on spaces, keeping double-quoted strings (without
// their quotes) as one token.
func tokens(line string) []string {
	var out []string
	var cur strings.Builder
	inQuote, quoted, escaped := false, false, false
	flush := func() {
		if cur.Len() > 0 || quoted {
			out = append(out, cur.String())
		}
		cur.Reset()
		quoted = false
	}
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch {
		case escaped:
			cur.WriteByte(c)
			escaped = false
		case c == '\\' && inQuote:
			escaped = true
		case c == '"':
			inQuote = !inQuote
			quoted = true
		case (c == ' ' || c == '\t') && !inQuote:
			flush()
		default:
			cur.WriteByte(c)
		}
	}
	flush()
	return out
}

// Quote renders a value for CLI output: plain words stay bare, anything
// else is double-quoted.
func Quote(v string) string {
	plain := v != ""
	for _, c := range v {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("._:/-", c)) {
			plain = false
			break
		}
	}
	if plain {
		return v
	}
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(v) + `"`
}
