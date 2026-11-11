package logs

import (
	"fmt"
	"net/netip"
	"strings"
)

// DeviceSyslog is how a device should send its logs to TaktaPlus.
type DeviceSyslog struct {
	Server   string `json:"server"`   // address devices reach TaktaPlus at
	Protocol string `json:"protocol"` // udp, tcp or tls
	Port     int    `json:"port"`
}

// Validate checks the values before they go into device CLI.
func (d *DeviceSyslog) Validate() error {
	if _, err := netip.ParseAddr(d.Server); err != nil {
		return invalidf("server must be the IP address devices use to reach TaktaPlus")
	}
	switch d.Protocol {
	case "udp", "tcp":
		if d.Port == 0 {
			d.Port = 514
		}
	case "tls":
		if d.Port == 0 {
			d.Port = 6514
		}
	default:
		return invalidf("protocol must be udp, tcp or tls")
	}
	if d.Port < 1 || d.Port > 65535 {
		return invalidf("port must be 1–65535")
	}
	return nil
}

// FortiGateCLI is the FortiOS configuration that sends logs to TaktaPlus
// in the default (key=value) format.
func (d DeviceSyslog) FortiGateCLI() string {
	mode, enc := "udp", "disable"
	switch d.Protocol {
	case "tcp":
		mode = "reliable"
	case "tls":
		mode, enc = "reliable", "high-medium"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "config log syslogd setting\n    set status enable\n    set server %q\n    set mode %s\n    set port %d\n    set format default\n", d.Server, mode, d.Port)
	if mode == "reliable" {
		fmt.Fprintf(&b, "    set enc-algorithm %s\n", enc)
	}
	b.WriteString("end\n")
	b.WriteString("config log syslogd filter\n    set severity information\n    set forward-traffic enable\n    set local-traffic disable\n    set multicast-traffic disable\n    set sniffer-traffic disable\n    set anomaly enable\n    set voip enable\nend\n")
	return b.String()
}

// FortiWebCLI is the FortiWeb configuration that sends attack, traffic and
// event logs to TaktaPlus.
func (d DeviceSyslog) FortiWebCLI() string {
	proto := "udp"
	if d.Protocol != "udp" {
		proto = "tcp"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "config log syslog-policy\n    edit \"TaktaPlus\"\n        config syslog-server-list\n            edit 1\n                set server %q\n                set port %d\n                set log-protocol %s\n", d.Server, d.Port, proto)
	if d.Protocol == "tls" {
		b.WriteString("                set enable-tls enable\n")
	}
	b.WriteString("            next\n        end\n    next\nend\n")
	b.WriteString("config log syslogd\n    set status enable\n    set policy \"TaktaPlus\"\n    set severity information\nend\n")
	b.WriteString("config log attack-log\n    set status enable\nend\nconfig log traffic-log\n    set status enable\nend\nconfig log event-log\n    set status enable\nend\n")
	return b.String()
}
