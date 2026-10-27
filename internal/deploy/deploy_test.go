package deploy

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/farshadfighter/taktaplus-lic/pkg/keys"
	"github.com/farshadfighter/taktaplus-lic/pkg/licformat"

	"github.com/farshadfighter/taktaplus/internal/backup"
	"github.com/farshadfighter/taktaplus/internal/changes"
	"github.com/farshadfighter/taktaplus/internal/device"
	"github.com/farshadfighter/taktaplus/internal/device/devicetest"
	"github.com/farshadfighter/taktaplus/internal/fileserver"
	"github.com/farshadfighter/taktaplus/internal/inventory"
	"github.com/farshadfighter/taktaplus/internal/license/licensetest"
	"github.com/farshadfighter/taktaplus/internal/packages"
	"github.com/farshadfighter/taktaplus/internal/secretbox"
	"github.com/farshadfighter/taktaplus/internal/testdb"
)

type env struct {
	ch    *changes.Service
	pkgs  *packages.Service
	inv   *inventory.Service
	fleet devicetest.Fleet
	ids   map[string]int64
	fw    *Firmware
}

func setup(t *testing.T, devices map[string]device.Info) *env {
	t.Helper()
	ctx := context.Background()
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	box, _ := secretbox.New(key)
	db := testdb.New(t)
	serials := map[string]licformat.Date{}
	fleet := devicetest.Fleet{}
	for host, info := range devices {
		serials[info.Serial] = licformat.NewDate(time.Now().Year()+1, 1, 1)
		fleet[host] = &devicetest.Fake{Info: info, Config: []byte("#config-version=x\nconfig system global\nend\n"), HealthV: device.Health{CPU: 10, Mem: 30}}
	}
	inv := inventory.New(db, box, licensetest.Guard(t, serials))
	inv.SetDriverFactory(fleet.Factory())
	ids := map[string]int64{}
	for host := range devices {
		id, err := inv.Create(ctx, inventory.Input{Name: host, Kind: device.KindFortiGate, Host: host,
			Credentials: &inventory.Credentials{APIToken: "t", SSHUser: "admin", SSHPassword: "pw"}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := inv.Probe(ctx, id); err != nil {
			t.Fatal(err)
		}
		ids[host] = id
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	pkgs := packages.New(db, box, t.TempDir(), keys.Keyring{}, nil)
	files := fileserver.New(fileserver.Config{TFTPAddr: "127.0.0.1:0", FTPAddr: "127.0.0.1:0", PublicIP: "10.0.0.1"}, pkgs.OpenFile, log)
	in := NewInstaller(inv, pkgs, files)
	in.VerifyWait = time.Millisecond
	ch := changes.New(db, inv, backup.New(db, box, inv, t.TempDir(), log, backup.Options{}), log)
	ch.Register(KindSignatures, NewSignatures(in))
	fw := NewFirmware(in)
	fw.RebootPoll = time.Millisecond
	fw.RebootTimeout = 2 * time.Second
	ch.Register(KindFirmware, fw)
	if err := ch.SetPolicy(ctx, nil, 0); err != nil {
		t.Fatal(err)
	}
	return &env{ch: ch, pkgs: pkgs, inv: inv, fleet: fleet, ids: ids, fw: fw}
}

func (e *env) upload(t *testing.T, name, content string, m packages.Meta) *packages.Package {
	p, err := e.pkgs.Upload(context.Background(), strings.NewReader(content), name, &m)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// run creates, submits and executes a change.
func (e *env) run(t *testing.T, kind string, spec any, hosts ...string) (*changes.Change, []changes.Target) {
	t.Helper()
	ctx := context.Background()
	raw, _ := json.Marshal(spec)
	var ids []int64
	for _, h := range hosts {
		ids = append(ids, e.ids[h])
	}
	c, err := e.ch.Create(ctx, changes.CreateInput{Kind: kind, Title: "t", Spec: raw, DeviceIDs: ids}, changes.Actor{Username: "op"})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.ch.Submit(ctx, c.ID, changes.Actor{Username: "op"}); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	if err := e.ch.Tick(ctx, &wg); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	c, _ = e.ch.Get(ctx, c.ID)
	ts, _ := e.ch.Targets(ctx, c.ID)
	return c, ts
}

func TestSignatureDeployment(t *testing.T) {
	e := setup(t, map[string]device.Info{
		"fg1": {Serial: "FG1", Model: "FG100F", Firmware: "7.4.4"},
		"fg2": {Serial: "FG2", Model: "FG60F", Firmware: "7.4.4"},
	})
	ctx := context.Background()
	ips := e.upload(t, "ips-28.950.pkg", "IPS", packages.Meta{Platform: "fortigate", Type: "ips_db", Version: "28.950", Models: []string{"FG100F"}})
	e.upload(t, "ips-28.900.pkg", "OLD IPS", packages.Meta{Platform: "fortigate", Type: "ips_db", Version: "28.900"})

	// The device reports the installed version once the package is applied.
	for _, f := range e.fleet {
		f.Set(func(f *devicetest.Fake) {
			f.OnInstall = func(f *devicetest.Fake, typ, name string, _ []byte) {
				v := "28.00950"
				if strings.Contains(name, "28.900") {
					v = "28.00900"
				}
				f.Set(func(f *devicetest.Fake) { f.Sigs = []device.Signature{{Name: "ips_db", Version: v}} })
			}
		})
	}

	// Explicit package compatible only with fg1: fg2 has nothing to install.
	raw, _ := json.Marshal(SignatureSpec{Packages: []string{ips.SHA256}, Verify: true})
	if _, err := e.ch.Create(ctx, changes.CreateInput{Kind: KindSignatures, Title: "x", Spec: raw, DeviceIDs: []int64{e.ids["fg2"]}}, changes.Actor{Username: "op"}); err == nil ||
		!strings.Contains(err.Error(), "no compatible package") {
		t.Fatalf("incompatible = %v", err)
	}
	c, ts := e.run(t, KindSignatures, SignatureSpec{Packages: []string{ips.SHA256}, Verify: true}, "fg1")
	if c.Status != changes.StatusSucceeded || !strings.Contains(ts[0].Preview, "ips_db 28.950") || !strings.Contains(ts[0].Output, "verified") {
		t.Fatalf("api deploy = %+v %+v", c, ts)
	}
	if got := e.fleet["fg1"].Installed; len(got) != 1 || got[0] != "ips_db:ips-28.950.pkg" {
		t.Fatalf("installed = %v", got)
	}

	// "Latest" picks the newest compatible package per device.
	c, ts = e.run(t, KindSignatures, SignatureSpec{Latest: []string{"ips_db"}, Verify: true}, "fg1", "fg2")
	if c.Status != changes.StatusSucceeded {
		t.Fatalf("latest = %+v %+v", c, ts)
	}
	if got := e.fleet["fg2"].Installed; len(got) != 1 || got[0] != "ips_db:ips-28.900.pkg" {
		t.Fatalf("fg2 latest = %v", got)
	}

	// Pull over TFTP: the device gets a CLI command naming a granted file.
	e.fleet["fg1"].Set(func(f *devicetest.Fake) { f.ScriptOutput = "Get file from tftp server OK.\n" })
	c, ts = e.run(t, KindSignatures, SignatureSpec{Packages: []string{ips.SHA256}, Method: MethodTFTP}, "fg1")
	scripts := e.fleet["fg1"].Scripts
	if c.Status != changes.StatusSucceeded || len(scripts) != 1 || !strings.HasPrefix(scripts[0], "execute restore ips tftp tp") ||
		!strings.Contains(scripts[0], "ips-28.950.pkg 10.0.0.1\ny") {
		t.Fatalf("tftp deploy = %+v %v", c, scripts)
	}

	// Verification fails when the device keeps the old version.
	e.fleet["fg1"].Set(func(f *devicetest.Fake) { f.OnInstall = nil; f.Sigs = nil })
	c, ts = e.run(t, KindSignatures, SignatureSpec{Packages: []string{ips.SHA256}, Verify: true}, "fg1")
	if c.Status != changes.StatusFailed || !strings.Contains(ts[0].Error, "does not report version 28.950") {
		t.Fatalf("verify failure = %+v %+v", c, ts)
	}
}

func TestFirmwareUpgrade(t *testing.T) {
	e := setup(t, map[string]device.Info{"fg1": {Serial: "FG1", Model: "FG100F", Firmware: "7.0.12", Build: "0523"}})
	fw72 := e.upload(t, "FGT_100F-v7.2.8.out", "FW72", packages.Meta{Platform: "fortigate", Type: "firmware", Version: "7.2.8", Models: []string{"FG100F"}, UpgradeFrom: []string{"7.0"}})
	fw74 := e.upload(t, "FGT_100F-v7.4.4.out", "FW74", packages.Meta{Platform: "fortigate", Type: "firmware", Version: "7.4.4", Models: []string{"FG100F"}, UpgradeFrom: []string{"7.2"}})
	e.upload(t, "FGT_60F-v7.4.4.out", "FW60", packages.Meta{Platform: "fortigate", Type: "firmware", Version: "7.4.4", Models: []string{"FG60F"}, UpgradeFrom: []string{"7.0"}})

	d, _ := e.inv.Get(context.Background(), e.ids["fg1"])
	path, err := e.fw.Path(context.Background(), d, "7.4.4")
	if err != nil || len(path) != 2 || path[0].ID != fw72.ID || path[1].ID != fw74.ID {
		t.Fatalf("path = %v %v", path, err)
	}
	if _, err := e.fw.Path(context.Background(), d, "7.6.0"); err == nil {
		t.Fatal("path to a missing version")
	}

	// Each image "reboots" the device onto its version.
	e.fleet["fg1"].Set(func(f *devicetest.Fake) {
		f.OnInstall = func(f *devicetest.Fake, _, name string, _ []byte) {
			v := map[string]string{"FGT_100F-v7.2.8.out": "7.2.8", "FGT_100F-v7.4.4.out": "7.4.4"}[name]
			f.Set(func(f *devicetest.Fake) { f.Info.Firmware = v })
		}
	})
	c, ts := e.run(t, KindFirmware, FirmwareSpec{Version: "7.4.4"}, "fg1")
	if c.Status != changes.StatusSucceeded || ts[0].Preview != "Upgrade path: 7.0.12 → 7.2.8 → 7.4.4" ||
		!strings.Contains(ts[0].Output, "device is back on 7.4.4") {
		t.Fatalf("upgrade = %+v %+v", c, ts)
	}
	if d, _ := e.inv.Get(context.Background(), e.ids["fg1"]); d.Firmware != "7.4.4" {
		t.Fatalf("firmware = %s", d.Firmware)
	}

	// A busy device fails the pre-check; a device that does not come back fails.
	_, err = e.ch.Create(context.Background(), changes.CreateInput{Kind: KindFirmware, Title: "x", Spec: json.RawMessage(`{"version":"7.4.4"}`),
		DeviceIDs: []int64{e.ids["fg1"]}}, changes.Actor{Username: "op"})
	var ie changes.InputError
	if !errors.As(err, &ie) || !strings.Contains(err.Error(), "already runs 7.4.4") {
		t.Fatalf("same version = %v", err)
	}
}
