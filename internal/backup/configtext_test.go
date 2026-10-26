package backup

import (
	"strings"
	"testing"
)

const cfgA = `#config-version=FG100F-7.04-FW-build2662-240514:opmode=0:vdom=0
#conf_file_ver=111
#buildno=2662
config system global
    set hostname "FG-HQ"
    set admintimeout 5
end
config system admin
    edit "admin"
        set password ENC SH2abc
    next
end
config firewall policy
    edit 1
        set name "web"
        set action accept
    next
end
`

func TestFingerprintIgnoresVolatile(t *testing.T) {
	b := strings.Replace(cfgA, "#conf_file_ver=111", "#conf_file_ver=222", 1)
	b = strings.ReplaceAll(b, "\n", "\r\n")
	if Fingerprint([]byte(cfgA)) != Fingerprint([]byte(b)) {
		t.Fatal("volatile lines changed the fingerprint")
	}
	if Fingerprint([]byte(cfgA)) == Fingerprint([]byte(strings.Replace(cfgA, "admintimeout 5", "admintimeout 10", 1))) {
		t.Fatal("real change not detected")
	}
}

func TestMask(t *testing.T) {
	m := Mask(cfgA + "    set psksecret ENC xyz\n    set comments \"ENC foo\"\n")
	if strings.Contains(m, "SH2abc") || strings.Contains(m, "xyz") || strings.Contains(m, "ENC foo") || !strings.Contains(m, `set hostname "FG-HQ"`) {
		t.Fatalf("masked:\n%s", m)
	}
}

func TestDiff(t *testing.T) {
	b := strings.Replace(cfgA, "set admintimeout 5", "set admintimeout 10", 1)
	b = strings.Replace(b, "set password ENC SH2abc", "set password ENC SH2zzz", 1)
	b = strings.Replace(b, "        set action accept\n    next\nend\n",
		"        set action accept\n        set logtraffic all\n    next\n    edit 2\n        set name \"dns\"\n    next\nend\n", 1)
	changes := Diff(cfgA, b)
	want := map[string]bool{
		"changed config system global admintimeout":             false,
		"changed config system admin > edit \"admin\" password": false,
		"added config firewall policy > edit 1 logtraffic":      false,
		"added config firewall policy > edit 2 ":                false,
	}
	for _, c := range changes {
		k := c.Kind + " " + c.Path + " " + c.Key
		if _, ok := want[k]; !ok {
			t.Errorf("unexpected change %+v", c)
		}
		want[k] = true
		if strings.Contains(c.Old+c.New, "SH2") {
			t.Errorf("secret in diff: %+v", c)
		}
	}
	for k, seen := range want {
		if !seen {
			t.Errorf("missing %q in %+v", k, changes)
		}
	}
	if d := Diff(cfgA, cfgA); len(d) != 0 {
		t.Fatalf("self diff = %+v", d)
	}
}
