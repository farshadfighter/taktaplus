package fortiosconf

import "testing"

const sample = `#config-version=FG100F-7.4.4-FW-build2662:opmode=0:vdom=0
config system global
    set hostname "FGT HQ"
    set admintimeout 5
    set strong-crypto enable
end
config system interface
    edit "wan1"
        set ip 203.0.113.10 255.255.255.248
        set allowaccess ping https ssh http
        set role wan
    next
    edit "port1"
        set allowaccess ping https
    next
end
config firewall policy
    edit 1
        set srcaddr "all"
        set dstaddr "all"
        set action accept
        set service "ALL"
        unset logtraffic
    next
end
config system admin
    edit "admin"
        set password ENC SH2abc
        set trusthost1 10.0.0.0 255.0.0.0
    next
end
config vpn certificate local
    edit "c"
        set private-key "-----BEGIN KEY-----
line two
-----END KEY-----"
    next
end
`

func TestParse(t *testing.T) {
	root := Parse(sample)
	g := root.Section("system global")
	if v, _ := g.Get("hostname"); v != "FGT HQ" {
		t.Fatalf("hostname = %q", v)
	}
	ifs := root.Section("system interface")
	if len(ifs.Children) != 2 || ifs.Children[0].Name != "wan1" || !ifs.Children[0].Entry {
		t.Fatalf("interfaces = %+v", ifs.Children)
	}
	if v := ifs.Child("wan1").Values["allowaccess"]; len(v) != 4 || v[3] != "http" {
		t.Fatalf("allowaccess = %v", v)
	}
	p := root.Section("firewall policy").Child("1")
	if !p.Unset["logtraffic"] {
		t.Fatal("unset not recorded")
	}
	key := root.Section("vpn certificate local").Child("c").Values["private-key"]
	if len(key) != 1 || key[0] != "-----BEGIN KEY-----\nline two\n-----END KEY-----" {
		t.Fatalf("multi-line value = %q", key)
	}
}

func TestVDOMs(t *testing.T) {
	root := Parse(`config vdom
edit root
next
edit dmz
next
end
config global
config system global
    set hostname "fw"
end
end
config vdom
edit root
config firewall policy
    edit 3
        set action deny
    next
end
next
edit dmz
config firewall policy
    edit 9
        set action accept
    next
end
next
end
`)
	if v, _ := root.Section("system global").Get("hostname"); v != "fw" {
		t.Fatalf("global hostname = %q", v)
	}
	ps := root.Sections("firewall policy")
	if len(ps) != 2 || ps[0].VDOM != "root" || ps[1].VDOM != "dmz" || ps[1].Node.Children[0].Name != "9" {
		t.Fatalf("policies = %+v", ps)
	}
}

func TestCompare(t *testing.T) {
	tpl := Parse(`config system global
    set admintimeout 10
    set strong-crypto enable
end
config system interface
    edit "wan1"
        set allowaccess ssh https ping http
    next
    edit "port9"
        set allowaccess ping
    next
end
config system admin
    edit "admin"
        set password "secret"
        unset trusthost1
    next
end
config system ntp
    set ntpsync enable
end
`)
	d := Compare(tpl, Parse(sample))
	want := map[string]bool{
		"system global|admintimeout":      true, // 5 ≠ 10
		"system interface > port9|":       true, // missing entry
		"system admin > admin|trusthost1": true, // must be unset
		"system ntp|":                     true, // missing section
	}
	if len(d) != len(want) {
		t.Fatalf("differences = %+v", d)
	}
	for _, x := range d {
		if !want[x.Path+"|"+x.Key] {
			t.Fatalf("unexpected difference %+v", x)
		}
	}
	if Quote("all") != "all" || Quote("FGT HQ") != `"FGT HQ"` || Quote(`a"b`) != `"a\"b"` {
		t.Fatal("quote")
	}
}
