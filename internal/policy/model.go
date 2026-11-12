// Package policy is the central object database and Policy Packages for
// FortiGate (F4): shared objects with per-device values, logical
// interfaces, versioned packages, install plans and local drift.
package policy

import (
	"fmt"
	"net/netip"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/farshadfighter/taktaplus/internal/fortiosconf"
)

// InputError is a validation failure to show to the user.
type InputError struct{ msg string }

func (e InputError) Error() string { return e.msg }

func invalidf(format string, a ...any) error { return InputError{fmt.Sprintf(format, a...)} }

// Object types and the FortiOS sections that hold them, in creation order.
var objectTypes = []string{"address", "addrgrp", "service", "servicegrp", "schedule", "ippool", "vip"}

var sections = map[string]string{
	"address":    "firewall address",
	"addrgrp":    "firewall addrgrp",
	"service":    "firewall service custom",
	"servicegrp": "firewall service group",
	"schedule":   "firewall schedule recurring",
	"vip":        "firewall vip",
	"ippool":     "firewall ippool",
	"policy":     "firewall policy",
}

// builtin names exist on every FortiGate and are never managed.
var builtin = map[string]bool{
	"address:all": true, "address:none": true, "address:FABRIC_DEVICE": true, "address:login.microsoftonline.com": true,
	"service:ALL": true, "service:ALL_TCP": true, "service:ALL_UDP": true, "service:ALL_ICMP": true, "service:HTTP": true,
	"service:HTTPS": true, "service:DNS": true, "service:SSH": true, "service:PING": true, "service:SMTP": true,
	"service:SMB": true, "service:RDP": true, "service:NTP": true, "service:SNMP": true, "service:FTP": true,
	"service:TELNET": true, "service:SAMBA": true, "service:IMAP": true, "service:POP3": true, "service:LDAP": true,
	"service:MYSQL": true, "service:MS-SQL": true, "service:SYSLOG": true, "service:KERBEROS": true,
	"servicegrp:Web Access": true, "servicegrp:Email Access": true, "servicegrp:Windows AD": true,
	"schedule:always": true, "schedule:none": true,
}

// IsBuiltin reports whether a name refers to a default FortiOS object.
func IsBuiltin(typ, name string) bool {
	if typ == "servicegrp" || typ == "service" {
		return builtin["service:"+name] || builtin["servicegrp:"+name]
	}
	if typ == "addrgrp" {
		return builtin["address:"+name]
	}
	return builtin[typ+":"+name]
}

// Value holds an object's settings; only the fields of its type are used.
type Value struct {
	// address: one of subnet (CIDR), fqdn, start_ip/end_ip, country
	Subnet  string `json:"subnet,omitempty"`
	FQDN    string `json:"fqdn,omitempty"`
	StartIP string `json:"start_ip,omitempty"`
	EndIP   string `json:"end_ip,omitempty"`
	Country string `json:"country,omitempty"`
	// addrgrp, servicegrp
	Members []string `json:"members,omitempty"`
	// service: TCP/UDP port ranges ("443 8000-8080") or ICMP
	Protocol string `json:"protocol,omitempty"` // TCP/UDP/SCTP (default), ICMP, IP
	TCPPorts string `json:"tcp_ports,omitempty"`
	UDPPorts string `json:"udp_ports,omitempty"`
	ICMPType *int   `json:"icmp_type,omitempty"`
	// schedule
	Days  string `json:"days,omitempty"` // "monday tuesday …"
	Start string `json:"start,omitempty"`
	End   string `json:"end,omitempty"`
	// vip
	ExtIP      string `json:"ext_ip,omitempty"`
	MappedIP   string `json:"mapped_ip,omitempty"`
	ExtZone    string `json:"ext_zone,omitempty"` // logical interface
	ExtPort    string `json:"ext_port,omitempty"`
	MappedPort string `json:"mapped_port,omitempty"`
	// ippool
	PoolStart string `json:"pool_start,omitempty"`
	PoolEnd   string `json:"pool_end,omitempty"`
}

var (
	portRange = regexp.MustCompile(`^\d{1,5}(-\d{1,5})?(:\d{1,5}(-\d{1,5})?)?$`)
	hhmm      = regexp.MustCompile(`^([01]\d|2[0-3]):[0-5]\d$`)
	nameRe    = regexp.MustCompile(`^[^"\\\x00-\x1f\x7f]{1,79}$`) // no quotes, backslashes or control characters
	weekdays  = []string{"sunday", "monday", "tuesday", "wednesday", "thursday", "friday", "saturday"}
)

func validIP(s string) bool {
	_, err := netip.ParseAddr(s)
	return err == nil
}

func validPorts(s string) bool {
	f := strings.Fields(s)
	for _, p := range f {
		if !portRange.MatchString(p) {
			return false
		}
	}
	return len(f) > 0
}

// validate checks a value for an object type.
func (v *Value) validate(typ string) error {
	switch typ {
	case "address":
		n := 0
		for _, s := range []string{v.Subnet, v.FQDN, v.StartIP + v.EndIP, v.Country} {
			if s != "" {
				n++
			}
		}
		if n != 1 {
			return invalidf("an address is a subnet, an FQDN, an IP range or a country")
		}
		switch {
		case v.Subnet != "":
			p, err := netip.ParsePrefix(v.Subnet)
			if err != nil || !p.Addr().Is4() {
				return invalidf("subnet must be IPv4 CIDR, such as 10.0.0.0/24")
			}
			v.Subnet = p.Masked().String()
		case v.FQDN != "":
			if strings.ContainsAny(v.FQDN, " \"") {
				return invalidf("FQDN is not valid")
			}
		case v.Country != "":
			if len(v.Country) != 2 {
				return invalidf("country is a two-letter code")
			}
			v.Country = strings.ToUpper(v.Country)
		default:
			if !validIP(v.StartIP) || !validIP(v.EndIP) {
				return invalidf("IP range needs a start and an end address")
			}
		}
	case "addrgrp", "servicegrp":
		if len(v.Members) == 0 {
			return invalidf("a group needs members")
		}
	case "service":
		if v.Protocol == "" {
			v.Protocol = "TCP/UDP/SCTP"
		}
		switch v.Protocol {
		case "TCP/UDP/SCTP":
			if v.TCPPorts == "" && v.UDPPorts == "" {
				return invalidf("enter TCP or UDP ports")
			}
			if (v.TCPPorts != "" && !validPorts(v.TCPPorts)) || (v.UDPPorts != "" && !validPorts(v.UDPPorts)) {
				return invalidf("ports are numbers or ranges separated by spaces, such as \"443 8000-8080\"")
			}
		case "ICMP":
		default:
			return invalidf("protocol must be TCP/UDP/SCTP or ICMP")
		}
	case "schedule":
		for _, d := range strings.Fields(v.Days) {
			if !slices.Contains(weekdays, d) {
				return invalidf("unknown day %q", d)
			}
		}
		if v.Days == "" || !hhmm.MatchString(v.Start) || !hhmm.MatchString(v.End) {
			return invalidf("a schedule needs days and start and end times (HH:MM)")
		}
	case "vip":
		if !validIP(v.ExtIP) || !validIP(v.MappedIP) || v.ExtZone == "" {
			return invalidf("a VIP needs an external address, a mapped address and an external interface")
		}
		if (v.ExtPort != "") != (v.MappedPort != "") {
			return invalidf("port forwarding needs both ports")
		}
	case "ippool":
		if !validIP(v.PoolStart) || !validIP(v.PoolEnd) {
			return invalidf("an IP pool needs start and end addresses")
		}
	default:
		return invalidf("unknown object type %q", typ)
	}
	return nil
}

// members returns the names a group refers to.
func (v *Value) members() []string { return v.Members }

// entry is a desired "edit" block: settings in order, and settings that
// must be absent.
type entry struct {
	name   string
	sets   []setting
	unsets []string
}

type setting struct {
	key    string
	tokens []string
}

func (e *entry) set(key string, tokens ...string) {
	e.sets = append(e.sets, setting{key, tokens})
}

// cli renders the entry as an "edit" block.
func (e *entry) cli(b *strings.Builder) {
	fmt.Fprintf(b, "    edit %s\n", fortiosconf.Quote(e.name))
	for _, s := range e.sets {
		q := make([]string, len(s.tokens))
		for i, t := range s.tokens {
			q[i] = fortiosconf.Quote(t)
		}
		fmt.Fprintf(b, "        set %s %s\n", s.key, strings.Join(q, " "))
	}
	for _, k := range e.unsets {
		fmt.Fprintf(b, "        unset %s\n", k)
	}
	b.WriteString("    next\n")
}

func mask(bits int) string {
	m := uint32(0xffffffff) << (32 - bits)
	if bits == 0 {
		m = 0
	}
	return fmt.Sprintf("%d.%d.%d.%d", m>>24, m>>16&0xff, m>>8&0xff, m&0xff)
}

// objectEntry renders an object for one device. zone resolves logical
// interfaces.
func objectEntry(typ, name string, v Value, zone func(string) (string, bool)) (entry, error) {
	e := entry{name: name}
	switch typ {
	case "address":
		switch {
		case v.Subnet != "":
			p, _ := netip.ParsePrefix(v.Subnet)
			e.set("subnet", p.Addr().String(), mask(p.Bits()))
		case v.FQDN != "":
			e.set("type", "fqdn")
			e.set("fqdn", v.FQDN)
		case v.Country != "":
			e.set("type", "geography")
			e.set("country", v.Country)
		default:
			e.set("type", "iprange")
			e.set("start-ip", v.StartIP)
			e.set("end-ip", v.EndIP)
		}
	case "addrgrp", "servicegrp":
		e.set("member", v.Members...)
	case "service":
		if v.Protocol == "ICMP" {
			e.set("protocol", "ICMP")
			if v.ICMPType != nil {
				e.set("icmptype", strconv.Itoa(*v.ICMPType))
			}
			break
		}
		if v.TCPPorts != "" {
			e.set("tcp-portrange", strings.Fields(v.TCPPorts)...)
		}
		if v.UDPPorts != "" {
			e.set("udp-portrange", strings.Fields(v.UDPPorts)...)
		}
	case "schedule":
		e.set("day", strings.Fields(v.Days)...)
		e.set("start", v.Start)
		e.set("end", v.End)
	case "vip":
		iface, ok := zone(v.ExtZone)
		if !ok {
			return e, fmt.Errorf("interface %q has no mapping", v.ExtZone)
		}
		e.set("extip", v.ExtIP)
		e.set("mappedip", v.MappedIP)
		e.set("extintf", iface)
		if v.ExtPort != "" {
			e.set("portforward", "enable")
			e.set("extport", v.ExtPort)
			e.set("mappedport", v.MappedPort)
		}
	case "ippool":
		e.set("startip", v.PoolStart)
		e.set("endip", v.PoolEnd)
	}
	return e, nil
}

// valueFromNode reads an object from a device configuration entry (for
// import and for accepting local changes).
func valueFromNode(typ string, n *fortiosconf.Node, iface func(string) string) Value {
	get := func(k string) string { v, _ := n.Get(k); return v }
	var v Value
	switch typ {
	case "address":
		switch get("type") {
		case "fqdn":
			v.FQDN = get("fqdn")
		case "geography":
			v.Country = get("country")
		case "iprange":
			v.StartIP, v.EndIP = get("start-ip"), get("end-ip")
		default:
			s := n.Values["subnet"]
			if len(s) == 2 {
				if m, err := netip.ParseAddr(s[1]); err == nil {
					bits := 0
					for _, b := range m.As4() {
						for i := 7; i >= 0; i-- {
							if b&(1<<i) != 0 {
								bits++
							}
						}
					}
					if p, err := netip.ParsePrefix(s[0] + "/" + strconv.Itoa(bits)); err == nil {
						v.Subnet = p.Masked().String()
					}
				}
			}
			if v.Subnet == "" {
				v.Subnet = "0.0.0.0/0"
			}
		}
	case "addrgrp", "servicegrp":
		v.Members = slices.Clone(n.Values["member"])
	case "service":
		v.Protocol = get("protocol")
		if v.Protocol == "" {
			v.Protocol = "TCP/UDP/SCTP"
		}
		v.TCPPorts, v.UDPPorts = get("tcp-portrange"), get("udp-portrange")
		if t, err := strconv.Atoi(get("icmptype")); err == nil {
			v.ICMPType = &t
		}
	case "schedule":
		v.Days, v.Start, v.End = get("day"), get("start"), get("end")
	case "vip":
		v.ExtIP, v.MappedIP, v.ExtZone = get("extip"), get("mappedip"), iface(get("extintf"))
		if get("portforward") == "enable" {
			v.ExtPort, v.MappedPort = get("extport"), get("mappedport")
		}
	case "ippool":
		v.PoolStart, v.PoolEnd = get("startip"), get("endip")
	}
	return v
}

// Policy is a firewall policy in a package. Interfaces are logical zones.
type Policy struct {
	ID         int      `json:"id"` // FortiOS policyid
	Name       string   `json:"name"`
	Section    string   `json:"section,omitempty"` // grouping shown in the panel
	SrcZones   []string `json:"srcintf"`
	DstZones   []string `json:"dstintf"`
	SrcAddr    []string `json:"srcaddr"`
	DstAddr    []string `json:"dstaddr"`
	Service    []string `json:"service"`
	Schedule   string   `json:"schedule"`
	Action     string   `json:"action"` // accept, deny
	NAT        bool     `json:"nat"`
	IPPool     []string `json:"ippool,omitempty"`
	AV         string   `json:"av_profile,omitempty"`
	WebFilter  string   `json:"webfilter_profile,omitempty"`
	IPS        string   `json:"ips_sensor,omitempty"`
	AppControl string   `json:"application_list,omitempty"`
	SSL        string   `json:"ssl_ssh_profile,omitempty"`
	LogTraffic string   `json:"logtraffic"` // all, utm, disable
	Disabled   bool     `json:"disabled"`
	Comments   string   `json:"comments,omitempty"`
}

// Profiles lists the security profiles a policy refers to, by FortiOS key.
func (p *Policy) Profiles() map[string]string {
	return map[string]string{"av-profile": p.AV, "webfilter-profile": p.WebFilter, "ips-sensor": p.IPS,
		"application-list": p.AppControl, "ssl-ssh-profile": p.SSL}
}

func (p *Policy) validate() error {
	p.Name = strings.TrimSpace(p.Name)
	if p.ID < 1 || p.ID > 1<<30 {
		return invalidf("policy ID must be a positive number")
	}
	if p.Name == "" || !nameRe.MatchString(p.Name) || len(p.Name) > 35 {
		return invalidf("policy %d: name is required (at most 35 characters, no quotes)", p.ID)
	}
	if len(p.SrcZones) == 0 || len(p.DstZones) == 0 || len(p.SrcAddr) == 0 || len(p.DstAddr) == 0 || len(p.Service) == 0 {
		return invalidf("policy %d: interfaces, addresses and services are required", p.ID)
	}
	if p.Schedule == "" {
		p.Schedule = "always"
	}
	if p.Action != "accept" && p.Action != "deny" {
		return invalidf("policy %d: action must be accept or deny", p.ID)
	}
	if p.LogTraffic == "" {
		p.LogTraffic = "utm"
	}
	if p.LogTraffic != "all" && p.LogTraffic != "utm" && p.LogTraffic != "disable" {
		return invalidf("policy %d: logtraffic must be all, utm or disable", p.ID)
	}
	if len(p.IPPool) > 0 && !p.NAT {
		return invalidf("policy %d: an IP pool needs NAT", p.ID)
	}
	for _, list := range [][]string{p.SrcAddr, p.DstAddr, p.Service, p.SrcZones, p.DstZones, p.IPPool} {
		for _, n := range list {
			if !nameRe.MatchString(n) {
				return invalidf("policy %d: invalid name %q", p.ID, n)
			}
		}
	}
	return nil
}

// ValidatePolicies checks a package's policies: valid each and unique IDs
// and names.
func ValidatePolicies(list []Policy) error {
	ids, names := map[int]bool{}, map[string]bool{}
	for i := range list {
		if err := list[i].validate(); err != nil {
			return err
		}
		if ids[list[i].ID] {
			return invalidf("policy ID %d is used twice", list[i].ID)
		}
		if names[strings.ToLower(list[i].Name)] {
			return invalidf("policy name %q is used twice", list[i].Name)
		}
		ids[list[i].ID], names[strings.ToLower(list[i].Name)] = true, true
	}
	return nil
}

// policyEntry renders a policy for one device.
func policyEntry(p Policy, iface func(string) (string, bool)) (entry, error) {
	e := entry{name: strconv.Itoa(p.ID)}
	mapZones := func(zones []string) ([]string, error) {
		out := make([]string, len(zones))
		for i, z := range zones {
			v, ok := iface(z)
			if !ok {
				return nil, fmt.Errorf("interface %q has no mapping", z)
			}
			out[i] = v
		}
		return out, nil
	}
	src, err := mapZones(p.SrcZones)
	if err != nil {
		return e, err
	}
	dst, err := mapZones(p.DstZones)
	if err != nil {
		return e, err
	}
	e.set("name", p.Name)
	e.set("srcintf", src...)
	e.set("dstintf", dst...)
	e.set("action", p.Action)
	e.set("srcaddr", p.SrcAddr...)
	e.set("dstaddr", p.DstAddr...)
	e.set("schedule", p.Schedule)
	e.set("service", p.Service...)
	status := "enable"
	if p.Disabled {
		status = "disable"
	}
	e.set("status", status)
	utm := false
	keys := []string{"av-profile", "webfilter-profile", "ips-sensor", "application-list", "ssl-ssh-profile"}
	prof := p.Profiles()
	for _, k := range keys {
		if prof[k] != "" {
			utm = true
		}
	}
	if utm && p.Action == "accept" {
		e.set("utm-status", "enable")
		for _, k := range keys {
			if prof[k] != "" {
				e.set(k, prof[k])
			} else {
				e.unsets = append(e.unsets, k)
			}
		}
	} else {
		e.set("utm-status", "disable")
	}
	e.set("logtraffic", p.LogTraffic)
	if p.NAT {
		e.set("nat", "enable")
		if len(p.IPPool) > 0 {
			e.set("ippool", "enable")
			e.set("poolname", p.IPPool...)
		} else {
			e.set("ippool", "disable")
		}
	} else {
		e.set("nat", "disable")
	}
	if p.Comments != "" {
		e.set("comments", p.Comments)
	} else {
		e.unsets = append(e.unsets, "comments")
	}
	return e, nil
}

// policyFromNode reads a device policy, turning interfaces into zones.
func policyFromNode(n *fortiosconf.Node, zoneOf func(string) string) (Policy, bool) {
	id, err := strconv.Atoi(n.Name)
	if err != nil {
		return Policy{}, false
	}
	get := func(k string) string { v, _ := n.Get(k); return v }
	zones := func(ifs []string) []string {
		out := make([]string, len(ifs))
		for i, f := range ifs {
			out[i] = zoneOf(f)
		}
		return out
	}
	p := Policy{ID: id, Name: get("name"), SrcZones: zones(n.Values["srcintf"]), DstZones: zones(n.Values["dstintf"]),
		SrcAddr: slices.Clone(n.Values["srcaddr"]), DstAddr: slices.Clone(n.Values["dstaddr"]), Service: slices.Clone(n.Values["service"]),
		Schedule: get("schedule"), Action: get("action"), NAT: get("nat") == "enable", LogTraffic: get("logtraffic"),
		Disabled: get("status") == "disable", Comments: get("comments"),
		AV: get("av-profile"), WebFilter: get("webfilter-profile"), IPS: get("ips-sensor"), AppControl: get("application-list"), SSL: get("ssl-ssh-profile")}
	if get("ippool") == "enable" {
		p.IPPool = slices.Clone(n.Values["poolname"])
	}
	if p.Action == "" {
		p.Action = "deny"
	}
	if p.Name == "" {
		p.Name = "policy-" + n.Name
	}
	if p.Schedule == "" {
		p.Schedule = "always"
	}
	if p.LogTraffic == "" {
		p.LogTraffic = "utm"
	}
	return p, true
}
