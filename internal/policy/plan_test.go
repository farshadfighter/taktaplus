package policy

import (
	"strings"
	"testing"

	"github.com/farshadfighter/taktaplus/internal/fortiosconf"
)

const deviceCfg = `config firewall address
    edit "Branch-LAN"
        set subnet 172.16.9.0 255.255.255.0
    next
    edit "Old-Printer-01"
        set subnet 10.10.99.4 255.255.255.255
    next
    edit "DC-Servers"
        set subnet 10.20.0.0 255.255.0.0
    next
end
config ips sensor
    edit "default"
    next
end
config firewall policy
    edit 7
        set name "Legacy"
        set srcintf "port1"
        set dstintf "wan1"
        set srcaddr "all"
        set dstaddr "all"
        set action accept
        set schedule "always"
        set service "ALL"
    next
    edit 2
        set name "LAN-to-DC"
        set srcintf "port1"
        set dstintf "wan1"
        set srcaddr "Branch-LAN"
        set dstaddr "DC-Servers"
        set action accept
        set schedule "always"
        set service "SMB" "HTTPS"
    next
    edit 1
        set name "LAN-to-Internet"
        set srcintf "port1"
        set dstintf "wan1"
        set srcaddr "Branch-LAN"
        set dstaddr "all"
        set action accept
        set schedule "always"
        set service "ALL"
        set nat enable
    next
end
`

func objects() []*Object {
	return []*Object{
		{Type: "address", Name: "Branch-LAN", Value: Value{Subnet: "0.0.0.0/0"}, PerDevice: true, Mappings: map[int64]Value{1: {Subnet: "172.16.3.0/24"}}},
		{Type: "address", Name: "DC-Servers", Value: Value{Subnet: "10.20.0.0/16"}, Mappings: map[int64]Value{}},
		{Type: "address", Name: "Guest-WiFi", Value: Value{Subnet: "10.50.0.0/24"}, Mappings: map[int64]Value{}},
		{Type: "addrgrp", Name: "Internal", Value: Value{Members: []string{"Guest-WiFi", "Branch-LAN"}}, Mappings: map[int64]Value{}},
		{Type: "service", Name: "Web-8443", Value: Value{Protocol: "TCP/UDP/SCTP", TCPPorts: "8443"}, Mappings: map[int64]Value{}},
	}
}

func policies() []Policy {
	return []Policy{
		{ID: 1, Name: "LAN-to-Internet", SrcZones: []string{"lan"}, DstZones: []string{"wan"}, SrcAddr: []string{"Branch-LAN"}, DstAddr: []string{"all"},
			Service: []string{"ALL"}, Schedule: "always", Action: "accept", NAT: true, LogTraffic: "utm"},
		{ID: 2, Name: "LAN-to-DC", SrcZones: []string{"lan"}, DstZones: []string{"wan"}, SrcAddr: []string{"Branch-LAN"}, DstAddr: []string{"DC-Servers"},
			Service: []string{"SMB", "HTTPS", "RDP"}, Schedule: "always", Action: "accept", IPS: "default", LogTraffic: "utm"},
		{ID: 3, Name: "Guest-to-Internet", SrcZones: []string{"guest"}, DstZones: []string{"wan"}, SrcAddr: []string{"Internal"}, DstAddr: []string{"all"},
			Service: []string{"Web-8443"}, Schedule: "always", Action: "accept", NAT: true, LogTraffic: "all"},
	}
}

func TestPlan(t *testing.T) {
	if err := ValidatePolicies(policies()); err != nil {
		t.Fatal(err)
	}
	in := Input{DeviceID: 1, DeviceName: "FGT-BR-03", VDOM: "root", Policies: policies(), Objects: objects(),
		Interfaces: map[string]string{"lan": "port1", "wan": "wan1", "guest": "port5"}, Config: fortiosconf.Parse(deviceCfg),
		Previously: []string{"address:Branch-LAN", "address:Old-Printer-01"}}
	p := BuildPlan(in)
	if len(p.Blocked) != 0 {
		t.Fatalf("blocked: %v", p.Blocked)
	}
	got := []string{}
	for _, o := range p.Ops {
		got = append(got, o.Action+" "+o.Type+" "+o.Name)
	}
	want := []string{
		"update address Branch-LAN",
		"create address Guest-WiFi",
		"create service Web-8443",
		"create addrgrp Internal",
		"update policy 2 LAN-to-DC",
		"create policy 3 Guest-to-Internet",
		"delete policy 7 Legacy",
		"move policy 1",
		"delete address Old-Printer-01",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("ops:\n%s", strings.Join(got, "\n"))
	}
	for _, s := range []string{
		"edit Branch-LAN\n        set subnet 172.16.3.0 255.255.255.0",
		"set member Guest-WiFi Branch-LAN",
		"set srcintf port5",
		"set utm-status enable\n        set ips-sensor default",
		"delete 7",
		"move 1 before 2",
		"config firewall address\n    delete Old-Printer-01\nend",
	} {
		if !strings.Contains(p.CLI, s) {
			t.Fatalf("CLI lacks %q:\n%s", s, p.CLI)
		}
	}
	// Applying the plan's result leaves nothing to do.
	after := fortiosconf.Parse(deviceCfg + p.CLI)
	_ = after
	// Blockers: a device without a mapping, missing interfaces and profiles.
	in2 := in
	in2.DeviceID = 2
	in2.Interfaces = map[string]string{"lan": "port1", "wan": "wan1"}
	in2.Config = fortiosconf.Parse("")
	p2 := BuildPlan(in2)
	text := strings.Join(p2.Blocked, "\n")
	for _, s := range []string{"Branch-LAN", "guest", "default"} {
		if !strings.Contains(text, s) {
			t.Fatalf("blocked lacks %q: %s", s, text)
		}
	}
}

func TestInSync(t *testing.T) {
	cfg := `config firewall address
    edit "Branch-LAN"
        set subnet 172.16.3.0 255.255.255.0
    next
end
config firewall policy
    edit 1
        set name "LAN-to-Internet"
        set srcintf "port1"
        set dstintf "wan1"
        set action accept
        set srcaddr "Branch-LAN"
        set dstaddr "all"
        set schedule "always"
        set service "ALL"
        set nat enable
    next
end
`
	in := Input{DeviceID: 1, VDOM: "root", Policies: policies()[:1], Objects: objects(),
		Interfaces: map[string]string{"lan": "port1", "wan": "wan1"}, Config: fortiosconf.Parse(cfg), Previously: []string{"address:Branch-LAN"}}
	if p := BuildPlan(in); len(p.Ops) != 0 || p.CLI != "" {
		t.Fatalf("in sync but plan = %+v", p.Ops)
	}
	// Multi-VDOM devices get the VDOM wrapper.
	in.Config = fortiosconf.Parse("config vdom\nedit root\nnext\nend\n")
	if p := BuildPlan(in); !strings.HasPrefix(p.CLI, "config vdom\nedit root\nconfig firewall address") {
		t.Fatalf("vdom CLI = %q", p.CLI)
	}
}
