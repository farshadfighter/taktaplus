package inventory

import (
	"context"
	"crypto/rand"
	"errors"
	"testing"
	"time"

	"github.com/farshadfighter/taktaplus-lic/pkg/licformat"

	"github.com/farshadfighter/taktaplus/internal/device"
	"github.com/farshadfighter/taktaplus/internal/device/devicetest"
	"github.com/farshadfighter/taktaplus/internal/license/licensetest"
	"github.com/farshadfighter/taktaplus/internal/secretbox"
	"github.com/farshadfighter/taktaplus/internal/testdb"
)

func newService(t *testing.T, fleet devicetest.Fleet) *Service {
	t.Helper()
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	box, _ := secretbox.New(key)
	next := licformat.NewDate(time.Now().Year()+1, 6, 1)
	guard := licensetest.Guard(t, map[string]licformat.Date{"FG100F0001": next, "FG100F0002": next})
	s := New(testdb.New(t), box, guard)
	s.SetDriverFactory(fleet.Factory())
	return s
}

func fgInput(name, host string) Input {
	return Input{Name: name, Kind: device.KindFortiGate, Host: host, Credentials: &Credentials{APIToken: "tok"}}
}

func TestInventoryLifecycle(t *testing.T) {
	fleet := devicetest.Fleet{
		"10.0.0.1": {Info: device.Info{Serial: "FG100F0001", Model: "FG100F", Firmware: "7.4.4", HAMode: "a-p", HAPeers: []string{"FG100F0002"}}},
		"10.0.0.9": {Info: device.Info{Serial: "FG60F9999"}},
	}
	s := newService(t, fleet)
	ctx := context.Background()

	if _, err := s.Create(ctx, Input{Name: "x", Kind: "cisco", Host: "h", Credentials: &Credentials{APIToken: "t"}}); !isInput(err) {
		t.Fatalf("bad kind = %v", err)
	}
	if _, err := s.Create(ctx, Input{Name: "x", Kind: device.KindFortiGate, Host: "h", Order: device.SSHOnly, Credentials: &Credentials{APIToken: "t"}}); !isInput(err) {
		t.Fatalf("ssh-only without ssh = %v", err)
	}

	gid, err := s.SaveGroup(ctx, 0, "HQ", "")
	if err != nil {
		t.Fatal(err)
	}
	in := fgInput("FG-HQ", "10.0.0.1")
	in.GroupID = &gid
	in.Tags = []string{"core", " core ", ""}
	id, err := s.Create(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(ctx, fgInput("fg-hq", "10.0.0.2")); !isInput(err) {
		t.Fatalf("duplicate name = %v", err)
	}
	d, err := s.Probe(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if d.Serial != "FG100F0001" || d.Status != "online" || d.License != licformat.StatusActive || d.GroupName != "HQ" ||
		len(d.Tags) != 1 || len(d.HAPeers) != 1 || d.HAPeers[0].License != licformat.StatusActive || !d.HasAPI || d.HasSSH {
		t.Fatalf("device = %+v", d)
	}

	// Same serial under another name is refused.
	fleet["10.0.0.3"] = &devicetest.Fake{Info: device.Info{Serial: "FG100F0001"}}
	id2, _ := s.Create(ctx, fgInput("dup", "10.0.0.3"))
	if _, err := s.Probe(ctx, id2); !isInput(err) {
		t.Fatalf("duplicate serial = %v", err)
	}

	// Unlicensed serial.
	id3, _ := s.Create(ctx, fgInput("lab", "10.0.0.9"))
	d3, err := s.Probe(ctx, id3)
	if err != nil || d3.License != licformat.StatusUnlicensed || d3.Operational() {
		t.Fatalf("unlicensed = %+v, %v", d3, err)
	}

	// Unreachable.
	id4, _ := s.Create(ctx, fgInput("gone", "10.0.0.50"))
	if _, err := s.Probe(ctx, id4); !errors.Is(err, devicetest.ErrDown) {
		t.Fatalf("down = %v", err)
	}
	d4, _ := s.Get(ctx, id4)
	if d4.Status != "offline" || d4.LastError == "" {
		t.Fatalf("down device = %+v", d4)
	}

	// Update keeps secrets that are not resent.
	upd := fgInput("FG-HQ", "10.0.0.1")
	upd.Credentials = &Credentials{SSHUser: "admin", SSHPassword: "pw"}
	if err := s.Update(ctx, id, upd); err != nil {
		t.Fatal(err)
	}
	d, _ = s.Get(ctx, id)
	if d.creds.APIToken != "tok" || d.creds.SSHUser != "admin" || d.GroupID != nil {
		t.Fatalf("updated creds = %+v group %v", d.creds, d.GroupID)
	}

	u, err := s.Usage(ctx)
	if err != nil || len(u.Devices) != 3 { // FG100F0001, its peer, FG60F9999
		t.Fatalf("usage = %+v, %v", u, err)
	}

	if err := s.SaveHealth(ctx, id, device.Health{CPU: 10, Mem: 20, Disk: -1, Interfaces: []device.Interface{{Name: "wan1", Up: true}}}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveSignatures(ctx, id, []device.Signature{{Name: "ips_db", Version: "1.0"}}, nil); err != nil {
		t.Fatal(err)
	}
	st, err := s.Status(ctx, id)
	if err != nil || st.CPU != 10 || len(st.Interfaces) != 1 || len(st.Signatures) != 1 || st.SignaturesAt == nil {
		t.Fatalf("status = %+v, %v", st, err)
	}
	m, err := s.Metrics(ctx, id, time.Now().Add(-time.Hour))
	if err != nil || len(m) != 1 || m[0].Mem != 20 {
		t.Fatalf("metrics = %+v, %v", m, err)
	}

	list, err := s.List(ctx, Filter{Query: "FG100"})
	if err != nil || len(list) != 1 {
		t.Fatalf("filter = %d, %v", len(list), err)
	}
	if err := s.Delete(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("after delete = %v", err)
	}
}

func isInput(err error) bool {
	var ie InputError
	return errors.As(err, &ie)
}
