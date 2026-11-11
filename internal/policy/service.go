package policy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/farshadfighter/taktaplus/internal/backup"
	"github.com/farshadfighter/taktaplus/internal/changes"
	"github.com/farshadfighter/taktaplus/internal/device"
	"github.com/farshadfighter/taktaplus/internal/fortiosconf"
	"github.com/farshadfighter/taktaplus/internal/inventory"
)

// Kind is the change kind for package installs.
const Kind = "policy_install"

// HitSource counts traffic per policy ID from the log store.
type HitSource interface {
	PolicyHits(ctx context.Context, deviceIDs []int64, since time.Time) (map[int]uint64, error)
}

// Service is the F4 module.
type Service struct {
	*Store
	db      *pgxpool.Pool
	inv     *inventory.Service
	backups *backup.Service
	changes *changes.Service
	hits    HitSource
	log     *slog.Logger
}

// New returns the service; register Runner() with the change service.
func New(db *pgxpool.Pool, inv *inventory.Service, b *backup.Service, ch *changes.Service, log *slog.Logger) *Service {
	return &Service{Store: NewStore(db), db: db, inv: inv, backups: b, changes: ch, log: log}
}

// SetHitSource enables hit counts.
func (s *Service) SetHitSource(h HitSource) { s.hits = h }

// HitCounts returns traffic per policy over the last 30 days on the
// package's devices, when the log store is available.
func (s *Service) HitCounts(ctx context.Context, p *Package) map[int]uint64 {
	if s.hits == nil || len(p.DeviceIDs) == 0 {
		return map[int]uint64{}
	}
	h, err := s.hits.PolicyHits(ctx, p.DeviceIDs, time.Now().AddDate(0, 0, -30))
	if err != nil {
		s.log.Warn("policy hit counts", "err", err)
		return map[int]uint64{}
	}
	return h
}

// deviceConfig returns the parsed configuration of the device's latest
// backup.
func (s *Service) deviceConfig(ctx context.Context, deviceID int64) (*fortiosconf.Node, int64, error) {
	var id int64
	err := s.db.QueryRow(ctx, `SELECT id FROM backups WHERE device_id = $1 AND status = 'ok' ORDER BY at DESC, id DESC LIMIT 1`, deviceID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, 0, invalidf("the device has no configuration backup yet")
	}
	if err != nil {
		return nil, 0, err
	}
	cfg, _, err := s.backups.Content(ctx, id)
	if err != nil {
		return nil, 0, err
	}
	return fortiosconf.Parse(string(cfg)), id, nil
}

// --- import ---

// ImportResult summarizes an import.
type ImportResult struct {
	Policies  int      `json:"policies"`
	Created   []string `json:"created"`   // new objects
	Conflicts []string `json:"conflicts"` // objects whose device value differs
	Zones     []string `json:"zones"`     // interfaces mapped to new zones
}

// Import reads a device's policies and the objects they use into the
// package's draft (taking the edit lock) and adds the device to the
// package. Objects new to the database are created; existing objects with
// another value become conflicts.
func (s *Service) Import(ctx context.Context, packageID, deviceID int64, user string) (*ImportResult, error) {
	d, err := s.inv.Get(ctx, deviceID)
	if err != nil {
		return nil, err
	}
	if d.Kind != device.KindFortiGate {
		return nil, invalidf("policy packages manage FortiGate devices")
	}
	pkg, err := s.Lock(ctx, packageID, user)
	if err != nil {
		return nil, err
	}
	if _, _, err := s.backups.Run(ctx, deviceID, backup.ReasonManual, user); err != nil {
		return nil, fmt.Errorf("reading the device configuration: %w", err)
	}
	cfg, _, err := s.deviceConfig(ctx, deviceID)
	if err != nil {
		return nil, err
	}
	res := &ImportResult{Created: []string{}, Conflicts: []string{}, Zones: []string{}}

	// Interfaces: reuse the device's zones, map the rest to zones of the
	// same (lower-cased) name.
	ifaces, err := s.Interfaces(ctx)
	if err != nil {
		return nil, err
	}
	m := ifaces[deviceID]
	if m == nil {
		m = map[string]string{}
	}
	zoneOf := func(iface string) string {
		if iface == "any" {
			return "any"
		}
		for z, i := range m {
			if i == iface {
				return z
			}
		}
		z := strings.ToLower(strings.ReplaceAll(iface, " ", "_"))
		for m[z] != "" {
			z += "_"
		}
		m[z] = iface
		res.Zones = append(res.Zones, z+" → "+iface)
		return z
	}
	var policies []Policy
	sec := cfg.Section("firewall policy")
	for _, sc := range cfg.Sections("firewall policy") {
		if sc.VDOM == pkg.VDOM {
			sec = sc.Node
		}
	}
	if sec != nil {
		for _, n := range sec.Children {
			if p, ok := policyFromNode(n, zoneOf); ok {
				policies = append(policies, p)
			}
		}
	}
	// Names that clash with each other get a suffix so the draft is valid.
	seen := map[string]bool{}
	for i := range policies {
		for seen[strings.ToLower(policies[i].Name)] {
			policies[i].Name += "-" + strconv.Itoa(policies[i].ID)
		}
		seen[strings.ToLower(policies[i].Name)] = true
		if len(policies[i].Name) > 35 {
			policies[i].Name = policies[i].Name[:35]
		}
	}
	if err := s.SetInterfaces(ctx, deviceID, m); err != nil {
		return nil, err
	}

	// Objects the policies use, with group members.
	objs, err := s.Objects(ctx)
	if err != nil {
		return nil, err
	}
	known := map[string]*Object{}
	for _, o := range objs {
		known[o.Type+":"+strings.ToLower(o.Name)] = o
	}
	done := map[string]bool{}
	var importName func(name string, types []string, depth int) error
	importName = func(name string, types []string, depth int) error {
		for _, t := range types {
			if IsBuiltin(t, name) || done[t+":"+name] || depth > 8 {
				return nil
			}
			var node *fortiosconf.Node
			for _, sc := range cfg.Sections(sections[t]) {
				if sc.VDOM == "" || sc.VDOM == pkg.VDOM {
					if n := sc.Node.Child(name); n != nil {
						node = n
					}
				}
			}
			if node == nil {
				continue
			}
			done[t+":"+name] = true
			v := valueFromNode(t, node, zoneOf)
			if t == "addrgrp" || t == "servicegrp" {
				mt := []string{"address", "addrgrp", "vip"}
				if t == "servicegrp" {
					mt = []string{"service", "servicegrp"}
				}
				for _, mem := range v.Members {
					if err := importName(mem, mt, depth+1); err != nil {
						return err
					}
				}
			}
			if err := v.validate(t); err != nil {
				res.Conflicts = append(res.Conflicts, fmt.Sprintf("%s (%s): %v", name, t, err))
				return nil
			}
			if o := known[t+":"+strings.ToLower(name)]; o != nil {
				cur := o.Value
				if mv, ok := o.Mappings[deviceID]; ok {
					cur = mv
				}
				if !sameValue(cur, v) {
					dv, _ := json.Marshal(v)
					if _, err := s.db.Exec(ctx, `INSERT INTO policy_conflicts (object_id, device_id, device_value) VALUES ($1, $2, $3)
						ON CONFLICT (object_id, device_id) DO UPDATE SET device_value = EXCLUDED.device_value, created_at = now()`, o.ID, deviceID, dv); err != nil {
						return err
					}
					res.Conflicts = append(res.Conflicts, name)
				}
				return nil
			}
			id, err := s.SaveObject(ctx, Object{Type: t, Name: name, Value: v, Mappings: map[int64]Value{}}, user)
			if err != nil {
				return fmt.Errorf("object %s: %w", name, err)
			}
			known[t+":"+strings.ToLower(name)] = &Object{ID: id, Type: t, Name: name, Value: v, Mappings: map[int64]Value{}}
			res.Created = append(res.Created, name)
			return nil
		}
		return nil
	}
	for _, p := range policies {
		for _, a := range slices.Concat(p.SrcAddr, p.DstAddr) {
			if err := importName(a, []string{"address", "addrgrp", "vip"}, 0); err != nil {
				return nil, err
			}
		}
		for _, sv := range p.Service {
			if err := importName(sv, []string{"service", "servicegrp"}, 0); err != nil {
				return nil, err
			}
		}
		if err := importName(p.Schedule, []string{"schedule"}, 0); err != nil {
			return nil, err
		}
		for _, ip := range p.IPPool {
			if err := importName(ip, []string{"ippool"}, 0); err != nil {
				return nil, err
			}
		}
	}
	if !slices.Contains(pkg.DeviceIDs, deviceID) {
		pkg.DeviceIDs = append(pkg.DeviceIDs, deviceID)
		if _, err := s.SavePackage(ctx, *pkg); err != nil {
			return nil, err
		}
	}
	// The device's policies become the draft only for an empty package;
	// otherwise the package stays the source of truth.
	if pkg.Version == 0 && len(pkg.Draft) == 0 {
		if policies == nil {
			policies = []Policy{}
		}
		if err := ValidatePolicies(policies); err != nil {
			return nil, err
		}
		if err := s.SaveDraft(ctx, packageID, user, policies); err != nil {
			return nil, err
		}
		res.Policies = len(policies)
	}
	return res, nil
}

func sameValue(a, b Value) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}

// Conflict is an object whose value on a device differs from the database.
type Conflict struct {
	ID          int64  `json:"id"`
	ObjectID    int64  `json:"object_id"`
	Type        string `json:"type"`
	Name        string `json:"name"`
	DeviceID    int64  `json:"device_id"`
	DeviceName  string `json:"device_name"`
	Shared      Value  `json:"shared"`
	DeviceValue Value  `json:"device_value"`
}

// Conflicts lists open import conflicts.
func (s *Service) Conflicts(ctx context.Context) ([]Conflict, error) {
	rows, err := s.db.Query(ctx, `SELECT c.id, o.id, o.type, o.name, d.id, d.name, o.value, c.device_value
		FROM policy_conflicts c JOIN policy_objects o ON o.id = c.object_id JOIN devices d ON d.id = c.device_id ORDER BY o.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Conflict{}
	for rows.Next() {
		var c Conflict
		var sv, dv []byte
		if err := rows.Scan(&c.ID, &c.ObjectID, &c.Type, &c.Name, &c.DeviceID, &c.DeviceName, &sv, &dv); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(sv, &c.Shared)
		_ = json.Unmarshal(dv, &c.DeviceValue)
		out = append(out, c)
	}
	return out, rows.Err()
}

// ResolveConflict keeps the device's value as its mapping ("map"), makes it
// the shared value ("default"), or keeps the shared value ("keep", the next
// install overwrites the device).
func (s *Service) ResolveConflict(ctx context.Context, id int64, how, user string) error {
	var oid, did int64
	var dv []byte
	err := s.db.QueryRow(ctx, `SELECT object_id, device_id, device_value FROM policy_conflicts WHERE id = $1`, id).Scan(&oid, &did, &dv)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	var v Value
	_ = json.Unmarshal(dv, &v)
	switch how {
	case "map":
		_, err = s.db.Exec(ctx, `INSERT INTO policy_object_mappings (object_id, device_id, value) VALUES ($1, $2, $3)
			ON CONFLICT (object_id, device_id) DO UPDATE SET value = EXCLUDED.value`, oid, did, dv)
	case "default":
		_, err = s.db.Exec(ctx, `UPDATE policy_objects SET value = $2, updated_by = $3, updated_at = now() WHERE id = $1`, oid, dv, user)
	case "keep":
	default:
		return invalidf("resolve with map, default or keep")
	}
	if err != nil {
		return err
	}
	_, err = s.db.Exec(ctx, `DELETE FROM policy_conflicts WHERE id = $1`, id)
	return err
}

// --- plans and installs ---

// Status is a device's state against its package.
type Status struct {
	DeviceID    int64      `json:"device_id"`
	DeviceName  string     `json:"device_name"`
	Installed   int        `json:"installed"` // version, 0 when never installed
	InstalledAt *time.Time `json:"installed_at"`
	Drift       []Op       `json:"drift"` // local changes found since
	CheckedAt   *time.Time `json:"checked_at"`
	State       string     `json:"state"` // in_sync, local_changes, outdated, not_installed
}

// Statuses returns the state of each device of a package.
func (s *Service) Statuses(ctx context.Context, p *Package) ([]Status, error) {
	out := []Status{}
	for _, id := range p.DeviceIDs {
		d, err := s.inv.Get(ctx, id)
		if err != nil {
			continue
		}
		st := Status{DeviceID: id, DeviceName: d.Name, Drift: []Op{}, State: "not_installed"}
		var drift []byte
		err = s.db.QueryRow(ctx, `SELECT version, installed_at, drift, checked_at FROM policy_installs WHERE package_id = $1 AND device_id = $2`,
			p.ID, id).Scan(&st.Installed, &st.InstalledAt, &drift, &st.CheckedAt)
		if err == nil {
			_ = json.Unmarshal(drift, &st.Drift)
			switch {
			case len(st.Drift) > 0:
				st.State = "local_changes"
			case st.Installed < p.Version:
				st.State = "outdated"
			default:
				st.State = "in_sync"
			}
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
		out = append(out, st)
	}
	return out, nil
}

func (s *Service) input(ctx context.Context, p *Package, policies []Policy, d *inventory.Device, cfg *fortiosconf.Node) (Input, error) {
	objs, err := s.Objects(ctx)
	if err != nil {
		return Input{}, err
	}
	ifaces, err := s.Interfaces(ctx)
	if err != nil {
		return Input{}, err
	}
	var prev []string
	var raw []byte
	err = s.db.QueryRow(ctx, `SELECT objects FROM policy_installs WHERE package_id = $1 AND device_id = $2`, p.ID, d.ID).Scan(&raw)
	if err == nil {
		_ = json.Unmarshal(raw, &prev)
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return Input{}, err
	}
	m := ifaces[d.ID]
	if m == nil {
		m = map[string]string{}
	}
	return Input{DeviceID: d.ID, DeviceName: d.Name, VDOM: p.VDOM, Policies: policies, Objects: objs, Interfaces: m, Config: cfg, Previously: prev}, nil
}

// Plans previews installing a version (0: the latest) on every device of
// the package, against each device's latest backup.
func (s *Service) Plans(ctx context.Context, packageID int64, version int) ([]*Plan, int, error) {
	p, err := s.Package(ctx, packageID)
	if err != nil {
		return nil, 0, err
	}
	if version == 0 {
		version = p.Version
	}
	if version == 0 {
		return nil, 0, invalidf("publish the package before installing it")
	}
	v, err := s.Version(ctx, packageID, version)
	if err != nil {
		return nil, 0, err
	}
	sts, err := s.Statuses(ctx, p)
	if err != nil {
		return nil, 0, err
	}
	var out []*Plan
	for _, id := range p.DeviceIDs {
		d, err := s.inv.Get(ctx, id)
		if err != nil {
			continue
		}
		plan := &Plan{DeviceID: d.ID, DeviceName: d.Name, Blocked: []string{}, Warnings: []string{}, Ops: []Op{}, Managed: []string{}}
		cfg, _, err := s.deviceConfig(ctx, id)
		if err == nil {
			in, ierr := s.input(ctx, p, v.Policies, d, cfg)
			if ierr != nil {
				return nil, 0, ierr
			}
			plan = BuildPlan(in)
		} else {
			plan.Blocked = append(plan.Blocked, err.Error())
		}
		if !d.HasSSH {
			plan.Blocked = append(plan.Blocked, "install needs SSH credentials on the device")
		}
		if !d.Operational() {
			plan.Blocked = append(plan.Blocked, "the device's license is not active")
		}
		for _, st := range sts {
			if st.DeviceID == id && len(st.Drift) > 0 {
				plan.Warnings = append(plan.Warnings, fmt.Sprintf("%d local changes on the device will be overwritten; accept them into the package first to keep them", len(st.Drift)))
			}
		}
		out = append(out, plan)
	}
	return out, version, nil
}

// InstallSpec is the change spec of an install.
type InstallSpec struct {
	PackageID int64 `json:"package_id"`
	Version   int   `json:"version"`
}

// Install opens a change request that installs a version on the package's
// devices that are ready, and submits it for approval.
func (s *Service) Install(ctx context.Context, packageID int64, version int, in changes.CreateInput, by changes.Actor) (*changes.Change, error) {
	plans, version, err := s.Plans(ctx, packageID, version)
	if err != nil {
		return nil, err
	}
	p, _ := s.Package(ctx, packageID)
	var ids []int64
	for _, pl := range plans {
		if len(pl.Blocked) == 0 && len(pl.Ops) > 0 {
			ids = append(ids, pl.DeviceID)
		}
	}
	if len(ids) == 0 {
		return nil, invalidf("no device needs this install, or every device is blocked")
	}
	spec, _ := json.Marshal(InstallSpec{PackageID: packageID, Version: version})
	in.Kind, in.Spec, in.DeviceIDs = Kind, spec, ids
	if in.Title == "" {
		in.Title = fmt.Sprintf("نصب Policy Package «%s» نسخه %d", p.Name, version)
	}
	t := true
	in.BackupFirst = &t
	c, err := s.changes.Create(ctx, in, by)
	if err != nil {
		return nil, err
	}
	if err := s.changes.Submit(ctx, c.ID, by); err != nil {
		return nil, err
	}
	return s.changes.Get(ctx, c.ID)
}

// Runner returns the change runner for installs.
func (s *Service) Runner() changes.Runner { return installer{s} }

type installer struct{ s *Service }

func (r installer) plan(ctx context.Context, raw json.RawMessage, d *inventory.Device) (*Plan, *Package, *VersionInfo, error) {
	var sp InstallSpec
	if err := json.Unmarshal(raw, &sp); err != nil || sp.PackageID == 0 {
		return nil, nil, nil, invalidf("invalid install")
	}
	p, err := r.s.Package(ctx, sp.PackageID)
	if err != nil {
		return nil, nil, nil, err
	}
	v, err := r.s.Version(ctx, sp.PackageID, sp.Version)
	if err != nil {
		return nil, nil, nil, err
	}
	cfg, _, err := r.s.deviceConfig(ctx, d.ID)
	if err != nil {
		return nil, nil, nil, err
	}
	in, err := r.s.input(ctx, p, v.Policies, d, cfg)
	if err != nil {
		return nil, nil, nil, err
	}
	return BuildPlan(in), p, v, nil
}

// Preview describes the install on one device.
func (r installer) Preview(ctx context.Context, raw json.RawMessage, d *inventory.Device) (string, error) {
	if !d.HasSSH {
		return "", invalidf("install needs SSH credentials on the device")
	}
	pl, _, _, err := r.plan(ctx, raw, d)
	if err != nil {
		return "", err
	}
	if len(pl.Blocked) > 0 {
		return "", invalidf("%s", strings.Join(pl.Blocked, "; "))
	}
	var b strings.Builder
	for i, op := range pl.Ops {
		fmt.Fprintf(&b, "# %d. %s %s %s %s\n", i+1, op.Action, op.Type, op.Name, op.Detail)
	}
	b.WriteString(pl.CLI)
	return b.String(), nil
}

// Run reads the device again (the change took a backup just before),
// applies the plan over SSH and restores that backup if the device
// refuses any command.
func (r installer) Run(ctx context.Context, c *changes.Change, d *inventory.Device, out io.Writer) error {
	pre, _, err := r.s.backups.Run(ctx, d.ID, backup.ReasonPreChange, fmt.Sprintf("change #%d", c.ID))
	if err != nil {
		return fmt.Errorf("reading the device before the install: %w", err)
	}
	pl, p, v, err := r.plan(ctx, c.Spec, d)
	if err != nil {
		return err
	}
	if len(pl.Blocked) > 0 {
		return errors.New(strings.Join(pl.Blocked, "; "))
	}
	if pl.CLI != "" {
		cli, ok := device.AsCLI(r.s.inv.Driver(d))
		if !ok {
			return errors.New("the device has no SSH access")
		}
		res, err := cli.RunScript(ctx, pl.CLI)
		fmt.Fprintln(out, res)
		if err != nil {
			fmt.Fprintf(out, "# install failed: %v; restoring backup #%d\n", err, pre.ID)
			if _, rerr := r.s.backups.Restore(ctx, pre.ID, fmt.Sprintf("change #%d rollback", c.ID)); rerr != nil {
				return fmt.Errorf("%w; the rollback could not start: %v", err, rerr)
			}
			return fmt.Errorf("%w; the configuration before the install is being restored (the device reboots)", err)
		}
	} else {
		fmt.Fprintln(out, "# the device already matches the package")
	}
	managed, _ := json.Marshal(pl.Managed)
	if _, err := r.s.db.Exec(ctx, `INSERT INTO policy_installs (package_id, device_id, version, installed_at, objects, drift, checked_at)
		VALUES ($1, $2, $3, now(), $4, '[]', now()) ON CONFLICT (package_id, device_id) DO UPDATE SET version = EXCLUDED.version,
		installed_at = EXCLUDED.installed_at, objects = EXCLUDED.objects, drift = '[]', checked_at = now()`, p.ID, d.ID, v.Version, managed); err != nil {
		return err
	}
	// Read the result so drift checks start from the installed state.
	if _, _, err := r.s.backups.Run(ctx, d.ID, backup.ReasonManual, fmt.Sprintf("change #%d", c.ID)); err != nil {
		fmt.Fprintf(out, "# backup after the install failed: %v\n", err)
	}
	return nil
}

// CheckDrift compares a device's latest backup with the version installed
// on it and records local changes.
func (s *Service) CheckDrift(ctx context.Context, deviceID int64) error {
	var pkgID int64
	var version int
	err := s.db.QueryRow(ctx, `SELECT package_id, version FROM policy_installs WHERE device_id = $1`, deviceID).Scan(&pkgID, &version)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	} else if err != nil {
		return err
	}
	p, err := s.Package(ctx, pkgID)
	if err != nil {
		return err
	}
	v, err := s.Version(ctx, pkgID, version)
	if err != nil {
		return err
	}
	d, err := s.inv.Get(ctx, deviceID)
	if err != nil {
		return err
	}
	cfg, _, err := s.deviceConfig(ctx, deviceID)
	if err != nil {
		return err
	}
	in, err := s.input(ctx, p, v.Policies, d, cfg)
	if err != nil {
		return err
	}
	pl := BuildPlan(in)
	drift, _ := json.Marshal(pl.Ops)
	_, err = s.db.Exec(ctx, `UPDATE policy_installs SET drift = $3, checked_at = now() WHERE package_id = $1 AND device_id = $2`, pkgID, deviceID, drift)
	return err
}

// AcceptLocal takes a device's current policies into the package draft
// (keeping sections of known policies) and stores the device's values of
// changed objects as its mappings.
func (s *Service) AcceptLocal(ctx context.Context, packageID, deviceID int64, user string) error {
	p, err := s.Lock(ctx, packageID, user)
	if err != nil {
		return err
	}
	cfg, _, err := s.deviceConfig(ctx, deviceID)
	if err != nil {
		return err
	}
	ifaces, err := s.Interfaces(ctx)
	if err != nil {
		return err
	}
	zoneOf := func(iface string) string {
		for z, i := range ifaces[deviceID] {
			if i == iface {
				return z
			}
		}
		return iface
	}
	base := p.Draft
	if !p.HasDraft && p.Version > 0 {
		v, err := s.Version(ctx, packageID, p.Version)
		if err != nil {
			return err
		}
		base = v.Policies
	}
	sectionOf := map[int]string{}
	for _, b := range base {
		sectionOf[b.ID] = b.Section
	}
	var policies []Policy
	for _, sc := range cfg.Sections("firewall policy") {
		if sc.VDOM != "" && sc.VDOM != p.VDOM {
			continue
		}
		for _, n := range sc.Node.Children {
			if pol, ok := policyFromNode(n, zoneOf); ok {
				pol.Section = sectionOf[pol.ID]
				policies = append(policies, pol)
			}
		}
	}
	objs, err := s.Objects(ctx)
	if err != nil {
		return err
	}
	for _, o := range objs {
		var node *fortiosconf.Node
		for _, sc := range cfg.Sections(sections[o.Type]) {
			if sc.VDOM == "" || sc.VDOM == p.VDOM {
				node = sc.Node.Child(o.Name)
			}
		}
		if node == nil {
			continue
		}
		dv := valueFromNode(o.Type, node, zoneOf)
		cur := o.Value
		if m, ok := o.Mappings[deviceID]; ok {
			cur = m
		}
		if dv.validate(o.Type) == nil && !sameValue(cur, dv) {
			o.Mappings[deviceID] = dv
			if _, err := s.SaveObject(ctx, *o, user); err != nil {
				return err
			}
		}
	}
	if policies == nil {
		policies = []Policy{}
	}
	return s.SaveDraft(ctx, packageID, user, policies)
}
