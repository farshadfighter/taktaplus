package policy

import (
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/farshadfighter/taktaplus/internal/fortiosconf"
)

// Object is a shared object with its per-device values.
type Object struct {
	ID        int64           `json:"id"`
	Type      string          `json:"type"`
	Name      string          `json:"name"`
	Value     Value           `json:"value"`
	PerDevice bool            `json:"per_device"` // every device needs a mapping
	Comment   string          `json:"comment"`
	Mappings  map[int64]Value `json:"mappings"` // device ID → value
}

// Op is one install operation.
type Op struct {
	Action string `json:"action"` // create, update, delete, move
	Type   string `json:"type"`   // object type or "policy"
	Name   string `json:"name"`
	Detail string `json:"detail"`
}

// Plan is what installing a package version on one device does.
type Plan struct {
	DeviceID   int64    `json:"device_id"`
	DeviceName string   `json:"device_name"`
	Blocked    []string `json:"blocked"`  // reasons the device cannot be installed
	Warnings   []string `json:"warnings"` // e.g. local changes that will be overwritten
	Ops        []Op     `json:"ops"`
	CLI        string   `json:"cli"`
	Managed    []string `json:"managed"` // "type:name" of objects the package manages on the device
}

// Input is everything a plan needs for one device.
type Input struct {
	DeviceID   int64
	DeviceName string
	VDOM       string
	Policies   []Policy
	Objects    []*Object         // the whole object database
	Interfaces map[string]string // zone → interface on the device
	Config     *fortiosconf.Node // the device's current configuration
	Previously []string          // objects managed by the last install
}

// defaults are FortiOS values of settings absent from a configuration.
var defaults = map[string]map[string]string{
	"firewall policy":         {"status": "enable", "nat": "disable", "utm-status": "disable", "logtraffic": "utm", "ippool": "disable", "action": "deny"},
	"firewall address":        {"type": "ipmask"},
	"firewall service custom": {"protocol": "TCP/UDP/SCTP"},
	"firewall vip":            {"portforward": "disable"},
	"firewall ippool":         {"type": "overload"},
}

// differs lists the settings of e that the device entry does not match.
func differs(section string, e entry, have *fortiosconf.Node) []string {
	var out []string
	for _, s := range e.sets {
		vals, ok := have.Values[s.key]
		if !ok {
			if d, has := defaults[section][s.key]; has {
				vals = strings.Fields(d)
			}
		}
		if !sameSet(vals, s.tokens) {
			out = append(out, fmt.Sprintf("%s: %s → %s", s.key, orDash(strings.Join(vals, " ")), strings.Join(s.tokens, " ")))
		}
	}
	for _, k := range e.unsets {
		if v, ok := have.Values[k]; ok {
			out = append(out, fmt.Sprintf("%s: %s → —", k, strings.Join(v, " ")))
		}
	}
	return out
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	x, y := slices.Clone(a), slices.Clone(b)
	sort.Strings(x)
	sort.Strings(y)
	return slices.Equal(x, y)
}

// lookup finds an object by any of the types a reference can name.
func lookup(objs map[string]*Object, name string, types ...string) *Object {
	for _, t := range types {
		if o := objs[t+":"+strings.ToLower(name)]; o != nil {
			return o
		}
	}
	return nil
}

var profileSections = map[string]string{
	"av-profile": "antivirus profile", "webfilter-profile": "webfilter profile", "ips-sensor": "ips sensor",
	"application-list": "application list", "ssl-ssh-profile": "firewall ssl-ssh-profile",
}

// existsOnDevice reports whether an entry exists in any of the sections.
func existsOnDevice(cfg *fortiosconf.Node, name string, secs ...string) bool {
	for _, s := range secs {
		for _, sc := range cfg.Sections(s) {
			if sc.Node.Child(name) != nil {
				return true
			}
		}
	}
	return false
}

// BuildPlan compares the desired state with the device configuration.
func BuildPlan(in Input) *Plan {
	p := &Plan{DeviceID: in.DeviceID, DeviceName: in.DeviceName, Blocked: []string{}, Warnings: []string{}, Ops: []Op{}, Managed: []string{}}
	block := func(format string, a ...any) {
		msg := fmt.Sprintf(format, a...)
		if !slices.Contains(p.Blocked, msg) {
			p.Blocked = append(p.Blocked, msg)
		}
	}
	iface := func(zone string) (string, bool) {
		if zone == "any" {
			return "any", true
		}
		v, ok := in.Interfaces[zone]
		return v, ok
	}
	objs := map[string]*Object{}
	for _, o := range in.Objects {
		objs[o.Type+":"+strings.ToLower(o.Name)] = o
	}
	vdomSection := func(path string) *fortiosconf.Node {
		for _, s := range in.Config.Sections(path) {
			if s.VDOM == "" || s.VDOM == in.VDOM {
				return s.Node
			}
		}
		return nil
	}

	// Objects the policies need, members before groups.
	need := map[string]*Object{}
	var order []*Object
	var visit func(o *Object, depth int)
	visit = func(o *Object, depth int) {
		key := o.Type + ":" + o.Name
		if need[key] != nil || depth > 8 {
			return
		}
		if o.Type == "addrgrp" || o.Type == "servicegrp" {
			for _, m := range o.Value.members() {
				types := []string{"address", "addrgrp"}
				if o.Type == "servicegrp" {
					types = []string{"service", "servicegrp"}
				}
				if c := lookup(objs, m, types...); c != nil {
					visit(c, depth+1)
				} else if !IsBuiltin(types[0], m) && !existsOnDevice(in.Config, m, sections[types[0]], sections[types[1]]) {
					block("%s %q is not in the object database or on the device", types[0], m)
				}
			}
		}
		need[key] = o
		order = append(order, o)
	}
	ref := func(name string, types ...string) {
		if o := lookup(objs, name, types...); o != nil {
			visit(o, 0)
			return
		}
		if IsBuiltin(types[0], name) {
			return
		}
		secs := make([]string, len(types))
		for i, t := range types {
			secs[i] = sections[t]
		}
		if !existsOnDevice(in.Config, name, secs...) {
			block("object %q is not in the object database or on the device", name)
		}
	}
	for _, pol := range in.Policies {
		for _, a := range append(slices.Clone(pol.SrcAddr), pol.DstAddr...) {
			ref(a, "address", "addrgrp", "vip")
		}
		for _, sv := range pol.Service {
			ref(sv, "service", "servicegrp")
		}
		ref(pol.Schedule, "schedule")
		for _, ip := range pol.IPPool {
			ref(ip, "ippool")
		}
		for k, v := range pol.Profiles() {
			if v != "" && !existsOnDevice(in.Config, v, profileSections[k]) {
				block("security profile %q (%s) does not exist on the device", v, k)
			}
		}
	}
	rank := map[string]int{}
	for i, t := range objectTypes {
		rank[t] = i
	}
	sort.SliceStable(order, func(i, j int) bool {
		// Groups after their members (visit order); otherwise by type.
		gi := order[i].Type == "addrgrp" || order[i].Type == "servicegrp"
		gj := order[j].Type == "addrgrp" || order[j].Type == "servicegrp"
		if gi != gj {
			return !gi
		}
		if gi {
			return false
		}
		return rank[order[i].Type] < rank[order[j].Type]
	})

	var cli strings.Builder
	sectionBlock := func(path string, entries []entry) {
		if len(entries) == 0 {
			return
		}
		fmt.Fprintf(&cli, "config %s\n", path)
		for i := range entries {
			entries[i].cli(&cli)
		}
		cli.WriteString("end\n")
	}

	// Objects to create or update, grouped by section in dependency order.
	var pending []entry
	pendingSec := ""
	flush := func() {
		sectionBlock(pendingSec, pending)
		pending, pendingSec = nil, ""
	}
	for _, o := range order {
		p.Managed = append(p.Managed, o.Type+":"+o.Name)
		v := o.Value
		if m, ok := o.Mappings[in.DeviceID]; ok {
			v = m
		} else if o.PerDevice {
			block("object %q has no value for this device (dynamic mapping)", o.Name)
			continue
		}
		e, err := objectEntry(o.Type, o.Name, v, iface)
		if err != nil {
			block("object %q: %v", o.Name, err)
			continue
		}
		sec := sections[o.Type]
		var have *fortiosconf.Node
		if s := vdomSection(sec); s != nil {
			have = s.Child(o.Name)
		}
		var op *Op
		switch {
		case have == nil:
			e.unsets = nil // nothing to clear on a new entry
			op = &Op{Action: "create", Type: o.Type, Name: o.Name, Detail: describe(o.Type, v)}
		default:
			if d := differs(sec, e, have); len(d) > 0 {
				op = &Op{Action: "update", Type: o.Type, Name: o.Name, Detail: strings.Join(d, "، ")}
			}
		}
		if op == nil {
			continue
		}
		if _, mapped := o.Mappings[in.DeviceID]; mapped {
			op.Detail += " (نگاشت)"
		}
		p.Ops = append(p.Ops, *op)
		if sec != pendingSec {
			flush()
			pendingSec = sec
		}
		pending = append(pending, e)
	}
	flush()

	// Policies: create or update, then order, then delete the rest.
	devPolicies := vdomSection("firewall policy")
	var devOrder []string
	if devPolicies != nil {
		for _, c := range devPolicies.Children {
			if c.Entry {
				devOrder = append(devOrder, c.Name)
			}
		}
	}
	var polEntries []entry
	want := map[string]bool{}
	for _, pol := range in.Policies {
		id := strconv.Itoa(pol.ID)
		want[id] = true
		e, err := policyEntry(pol, iface)
		if err != nil {
			block("policy %d (%s): %v", pol.ID, pol.Name, err)
			continue
		}
		var have *fortiosconf.Node
		if devPolicies != nil {
			have = devPolicies.Child(id)
		}
		if have == nil {
			p.Ops = append(p.Ops, Op{Action: "create", Type: "policy", Name: fmt.Sprintf("%d %s", pol.ID, pol.Name),
				Detail: fmt.Sprintf("%s → %s، %s", strings.Join(pol.SrcZones, ","), strings.Join(pol.DstZones, ","), pol.Action)})
			e.unsets = nil
			polEntries = append(polEntries, e)
		} else if d := differs("firewall policy", e, have); len(d) > 0 {
			p.Ops = append(p.Ops, Op{Action: "update", Type: "policy", Name: fmt.Sprintf("%d %s", pol.ID, pol.Name), Detail: strings.Join(d, "، ")})
			polEntries = append(polEntries, e)
		}
	}
	var deletes []string
	for _, id := range devOrder {
		if !want[id] {
			deletes = append(deletes, id)
			name := id
			if n := devPolicies.Child(id); n != nil {
				if v, ok := n.Get("name"); ok {
					name += " " + v
				}
			}
			p.Ops = append(p.Ops, Op{Action: "delete", Type: "policy", Name: name, Detail: "در Package نیست"})
		}
	}
	// Order: FortiOS appends new policies; move until the order matches.
	current := []string{}
	for _, id := range devOrder {
		if want[id] {
			current = append(current, id)
		}
	}
	for _, pol := range in.Policies {
		id := strconv.Itoa(pol.ID)
		if !slices.Contains(current, id) {
			current = append(current, id)
		}
	}
	var moves []string
	for i, pol := range in.Policies {
		id := strconv.Itoa(pol.ID)
		if i == 0 {
			if current[0] != id {
				moves = append(moves, fmt.Sprintf("move %s before %s", id, current[0]))
				current = slices.DeleteFunc(current, func(x string) bool { return x == id })
				current = slices.Insert(current, 0, id)
				p.Ops = append(p.Ops, Op{Action: "move", Type: "policy", Name: id, Detail: "اول فهرست"})
			}
			continue
		}
		prev := strconv.Itoa(in.Policies[i-1].ID)
		pos := slices.Index(current, id)
		if pos > 0 && current[pos-1] == prev {
			continue
		}
		moves = append(moves, fmt.Sprintf("move %s after %s", id, prev))
		current = slices.DeleteFunc(current, func(x string) bool { return x == id })
		current = slices.Insert(current, slices.Index(current, prev)+1, id)
		p.Ops = append(p.Ops, Op{Action: "move", Type: "policy", Name: id, Detail: "بعد از Policy " + prev})
	}
	if len(polEntries) > 0 || len(moves) > 0 || len(deletes) > 0 {
		cli.WriteString("config firewall policy\n")
		for i := range polEntries {
			polEntries[i].cli(&cli)
		}
		for _, id := range deletes {
			fmt.Fprintf(&cli, "    delete %s\n", id)
		}
		for _, m := range moves {
			fmt.Fprintf(&cli, "    %s\n", m)
		}
		cli.WriteString("end\n")
	}

	// Objects the last install managed and the package no longer uses;
	// groups first.
	var stale []string
	for _, key := range in.Previously {
		if !slices.Contains(p.Managed, key) {
			stale = append(stale, key)
		}
	}
	sort.SliceStable(stale, func(i, j int) bool {
		gi := strings.HasPrefix(stale[i], "addrgrp:") || strings.HasPrefix(stale[i], "servicegrp:")
		gj := strings.HasPrefix(stale[j], "addrgrp:") || strings.HasPrefix(stale[j], "servicegrp:")
		return gi && !gj
	})
	for _, key := range stale {
		typ, name, _ := strings.Cut(key, ":")
		sec := vdomSection(sections[typ])
		if sec == nil || sec.Child(name) == nil {
			continue
		}
		p.Ops = append(p.Ops, Op{Action: "delete", Type: typ, Name: name, Detail: "دیگر استفاده نمی‌شود"})
		fmt.Fprintf(&cli, "config %s\n    delete %s\nend\n", sections[typ], fortiosconf.Quote(name))
	}

	if cli.Len() > 0 && in.Config.Child("vdom") != nil {
		p.CLI = fmt.Sprintf("config vdom\nedit %s\n%send\n", fortiosconf.Quote(in.VDOM), cli.String())
	} else {
		p.CLI = cli.String()
	}
	return p
}

// describe summarizes an object value for the operation list.
func describe(typ string, v Value) string {
	switch typ {
	case "address":
		switch {
		case v.Subnet != "":
			return v.Subnet
		case v.FQDN != "":
			return v.FQDN
		case v.Country != "":
			return "کشور " + v.Country
		}
		return v.StartIP + " - " + v.EndIP
	case "addrgrp", "servicegrp":
		return strings.Join(v.Members, "، ")
	case "service":
		if v.Protocol == "ICMP" {
			return "ICMP"
		}
		return strings.TrimSpace("TCP " + v.TCPPorts + " UDP " + v.UDPPorts)
	case "schedule":
		return v.Days + " " + v.Start + "-" + v.End
	case "vip":
		return v.ExtIP + " → " + v.MappedIP
	case "ippool":
		return v.PoolStart + " - " + v.PoolEnd
	}
	return ""
}
