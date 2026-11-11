package policy

import (
	"context"
	"crypto/rand"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/farshadfighter/taktaplus-lic/pkg/licformat"

	"github.com/farshadfighter/taktaplus/internal/auth"
	"github.com/farshadfighter/taktaplus/internal/backup"
	"github.com/farshadfighter/taktaplus/internal/changes"
	"github.com/farshadfighter/taktaplus/internal/device"
	"github.com/farshadfighter/taktaplus/internal/device/devicetest"
	"github.com/farshadfighter/taktaplus/internal/inventory"
	"github.com/farshadfighter/taktaplus/internal/license/licensetest"
	"github.com/farshadfighter/taktaplus/internal/secretbox"
	"github.com/farshadfighter/taktaplus/internal/testdb"
)

func TestImportInstallDrift(t *testing.T) {
	ctx := context.Background()
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	box, _ := secretbox.New(key)
	db := testdb.New(t)
	exp := licformat.NewDate(time.Now().Year()+1, 1, 1)
	guard := licensetest.Guard(t, map[string]licformat.Date{"FGBR03": exp, "FGBR05": exp})
	inv := inventory.New(db, box, guard)
	br3 := &devicetest.Fake{Info: device.Info{Serial: "FGBR03"}, Config: []byte(deviceCfg)}
	br5 := &devicetest.Fake{Info: device.Info{Serial: "FGBR05"}, Config: []byte(strings.Replace(deviceCfg, "172.16.9.0", "172.16.5.0", 1))}
	inv.SetDriverFactory(devicetest.Fleet{"br3": br3, "br5": br5}.Factory())
	creds := &inventory.Credentials{APIToken: "t", SSHUser: "admin", SSHPassword: "p"}
	id3, _ := inv.Create(ctx, inventory.Input{Name: "FGT-BR-03", Kind: device.KindFortiGate, Host: "br3", Credentials: creds})
	id5, _ := inv.Create(ctx, inventory.Input{Name: "FGT-BR-05", Kind: device.KindFortiGate, Host: "br5", Credentials: creds})
	for _, id := range []int64{id3, id5} {
		if _, err := inv.Probe(ctx, id); err != nil {
			t.Fatal(err)
		}
	}
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	backups := backup.New(db, box, inv, t.TempDir(), quiet, backup.Options{RebootWait: time.Millisecond, RebootPoll: time.Millisecond})
	ch := changes.New(db, inv, backups, quiet)
	svc := New(db, inv, backups, ch, quiet)
	ch.Register(Kind, svc.Runner())

	pkgID, err := svc.SavePackage(ctx, Package{Name: "Branch-Std"})
	if err != nil {
		t.Fatal(err)
	}
	res, err := svc.Import(ctx, pkgID, id3, "admin")
	if err != nil {
		t.Fatal(err)
	}
	if res.Policies != 3 || len(res.Created) != 2 || len(res.Zones) != 2 {
		t.Fatalf("import = %+v", res)
	}
	// The second device's Branch-LAN differs: a conflict, resolved as a mapping.
	res, err = svc.Import(ctx, pkgID, id5, "admin")
	if err != nil {
		t.Fatal(err)
	}
	conf, _ := svc.Conflicts(ctx)
	if len(res.Conflicts) != 1 || len(conf) != 1 || conf[0].Name != "Branch-LAN" || conf[0].DeviceValue.Subnet != "172.16.5.0/24" {
		t.Fatalf("conflicts = %+v %+v", res, conf)
	}
	if err := svc.ResolveConflict(ctx, conf[0].ID, "map", "admin"); err != nil {
		t.Fatal(err)
	}
	// Someone else cannot edit while the lock is held.
	if _, err := svc.Lock(ctx, pkgID, "other"); !errors.Is(err, ErrLocked) {
		t.Fatalf("lock = %v", err)
	}
	p, _ := svc.Package(ctx, pkgID)
	// Edit: drop the legacy any-any policy and add a guest policy.
	var draft []Policy
	for _, pol := range p.Draft {
		if pol.ID != 7 {
			draft = append(draft, pol)
		}
	}
	draft = append(draft, Policy{ID: 3, Name: "Guest-to-Internet", SrcZones: []string{"port1"}, DstZones: []string{"wan1"},
		SrcAddr: []string{"Guest-WiFi"}, DstAddr: []string{"all"}, Service: []string{"HTTPS"}, Action: "accept", NAT: true})
	if _, err := svc.SaveObject(ctx, Object{Type: "address", Name: "Guest-WiFi", Value: Value{Subnet: "10.50.0.0/24"}}, "admin"); err != nil {
		t.Fatal(err)
	}
	if err := svc.SaveDraft(ctx, pkgID, "admin", draft); err != nil {
		t.Fatal(err)
	}
	if v, err := svc.Publish(ctx, pkgID, "admin", "first"); err != nil || v != 1 {
		t.Fatalf("publish = %d %v", v, err)
	}
	plans, _, err := svc.Plans(ctx, pkgID, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, pl := range plans {
		if len(pl.Blocked) > 0 {
			t.Fatalf("%s blocked: %v", pl.DeviceName, pl.Blocked)
		}
		if !strings.Contains(pl.CLI, "delete 7") || !strings.Contains(pl.CLI, "edit Guest-WiFi") {
			t.Fatalf("%s plan:\n%s", pl.DeviceName, pl.CLI)
		}
	}
	// br5 refuses the commands: the backup is restored and the install fails there.
	var mu sync.Mutex
	br5.OnScript = func(_ *devicetest.Fake, script string) (string, error) { return "", errors.New("command parse error") }
	br3.OnScript = func(f *devicetest.Fake, script string) (string, error) {
		mu.Lock()
		defer mu.Unlock()
		return "ok", nil
	}
	c, err := svc.Install(ctx, pkgID, 0, changes.CreateInput{RunMode: "now", UrgentReason: "test"}, changes.Actor{Username: "admin"})
	if err != nil {
		t.Fatal(err)
	}
	if c.Status == "pending" {
		u, err := auth.NewService(db, box).CreateUser(ctx, "approver", "Approver", "", auth.RoleApprover, "correct horse battery staple")
		if err != nil {
			t.Fatal(err)
		}
		if err := ch.Decide(ctx, c.ID, changes.Actor{UserID: u.ID, Username: "approver"}, true, ""); err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	if err := ch.Tick(ctx, &wg); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	backups.Wait()
	c, _ = ch.Get(ctx, c.ID)
	targets, _ := ch.Targets(ctx, c.ID)
	status := map[int64]string{}
	for _, tg := range targets {
		status[tg.DeviceID] = tg.Status
	}
	if status[id3] != "succeeded" || status[id5] != "failed" || len(br5.Restored) != 1 || len(br3.Scripts) != 1 {
		t.Fatalf("change %s targets %+v restored %d", c.Status, targets, len(br5.Restored))
	}
	// br3's configuration did not really change (fake): drift is found.
	if err := svc.CheckDrift(ctx, id3); err != nil {
		t.Fatal(err)
	}
	p, _ = svc.Package(ctx, pkgID)
	sts, _ := svc.Statuses(ctx, p)
	state := map[int64]string{}
	for _, st := range sts {
		state[st.DeviceID] = st.State
	}
	if state[id3] != "local_changes" || state[id5] != "not_installed" {
		t.Fatalf("states = %+v", sts)
	}
	// Accepting local changes brings the device's policies back into a draft.
	if err := svc.AcceptLocal(ctx, pkgID, id3, "admin"); err != nil {
		t.Fatal(err)
	}
	p, _ = svc.Package(ctx, pkgID)
	if !p.HasDraft || len(p.Draft) != 3 {
		t.Fatalf("accepted draft = %+v", p.Draft)
	}
	// Objects in use cannot be deleted.
	objs, _ := svc.Objects(ctx)
	for _, o := range objs {
		if o.Name == "Branch-LAN" {
			if err := svc.DeleteObject(ctx, o.ID); err == nil {
				t.Fatal("object in use deleted")
			}
		}
	}
}
