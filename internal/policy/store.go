package policy

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound means the object, package or version does not exist.
var ErrNotFound = errors.New("not found")

// ErrLocked means another administrator holds the package's edit lock.
var ErrLocked = errors.New("the package is being edited by someone else")

// lockTTL is how long an idle edit lock lasts.
const lockTTL = 30 * time.Minute

// Store keeps objects, mappings and packages.
type Store struct {
	db  *pgxpool.Pool
	now func() time.Time
}

// NewStore returns a store.
func NewStore(db *pgxpool.Pool) *Store { return &Store{db: db, now: time.Now} }

// --- objects ---

// Objects returns every object with its mappings.
func (s *Store) Objects(ctx context.Context) ([]*Object, error) {
	rows, err := s.db.Query(ctx, `SELECT id, type, name, value, per_device, comment FROM policy_objects ORDER BY type, lower(name)`)
	if err != nil {
		return nil, err
	}
	var out []*Object
	byID := map[int64]*Object{}
	for rows.Next() {
		var o Object
		var v []byte
		if err := rows.Scan(&o.ID, &o.Type, &o.Name, &v, &o.PerDevice, &o.Comment); err != nil {
			rows.Close()
			return nil, err
		}
		_ = json.Unmarshal(v, &o.Value)
		o.Mappings = map[int64]Value{}
		out = append(out, &o)
		byID[o.ID] = &o
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows, err = s.db.Query(ctx, `SELECT object_id, device_id, value FROM policy_object_mappings`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var oid, did int64
		var v []byte
		if err := rows.Scan(&oid, &did, &v); err != nil {
			return nil, err
		}
		var val Value
		_ = json.Unmarshal(v, &val)
		if o := byID[oid]; o != nil {
			o.Mappings[did] = val
		}
	}
	if out == nil {
		out = []*Object{}
	}
	return out, rows.Err()
}

func validName(n string) error {
	if strings.TrimSpace(n) != n || n == "" || !nameRe.MatchString(n) {
		return invalidf("name is required: up to 79 characters, without quotes or leading spaces")
	}
	return nil
}

// SaveObject creates (ID 0) or updates an object; its mappings are
// replaced by o.Mappings.
func (s *Store) SaveObject(ctx context.Context, o Object, by string) (int64, error) {
	if err := validName(o.Name); err != nil {
		return 0, err
	}
	if !slices.Contains(objectTypes, o.Type) {
		return 0, invalidf("unknown object type %q", o.Type)
	}
	if IsBuiltin(o.Type, o.Name) {
		return 0, invalidf("%q is a built-in FortiOS object", o.Name)
	}
	if err := o.Value.validate(o.Type); err != nil {
		return 0, err
	}
	for id, m := range o.Mappings {
		if err := m.validate(o.Type); err != nil {
			return 0, invalidf("mapping for device %d: %v", id, err)
		}
		o.Mappings[id] = m
	}
	val, _ := json.Marshal(o.Value)
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		if o.ID == 0 {
			if err := tx.QueryRow(ctx, `INSERT INTO policy_objects (type, name, value, per_device, comment, updated_by) VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`,
				o.Type, o.Name, val, o.PerDevice, o.Comment, by).Scan(&o.ID); err != nil {
				return err
			}
		} else {
			tag, err := tx.Exec(ctx, `UPDATE policy_objects SET name = $2, value = $3, per_device = $4, comment = $5, updated_by = $6, updated_at = now()
				WHERE id = $1 AND type = $7`, o.ID, o.Name, val, o.PerDevice, o.Comment, by, o.Type)
			if err != nil {
				return err
			}
			if tag.RowsAffected() == 0 {
				return ErrNotFound
			}
			if _, err := tx.Exec(ctx, `DELETE FROM policy_object_mappings WHERE object_id = $1`, o.ID); err != nil {
				return err
			}
		}
		for did, m := range o.Mappings {
			mv, _ := json.Marshal(m)
			if _, err := tx.Exec(ctx, `INSERT INTO policy_object_mappings (object_id, device_id, value) VALUES ($1, $2, $3)`, o.ID, did, mv); err != nil {
				return err
			}
			// A mapping resolves an import conflict for that device.
			if _, err := tx.Exec(ctx, `DELETE FROM policy_conflicts WHERE object_id = $1 AND device_id = $2`, o.ID, did); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil && strings.Contains(err.Error(), "policy_objects_name_key") {
		return 0, invalidf("an object of this type with this name already exists")
	}
	return o.ID, err
}

// DeleteObject removes an object unless a package version still uses it.
func (s *Store) DeleteObject(ctx context.Context, id int64) error {
	var name string
	if err := s.db.QueryRow(ctx, `SELECT name FROM policy_objects WHERE id = $1`, id).Scan(&name); errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	usage, err := s.Usage(ctx)
	if err != nil {
		return err
	}
	if u := usage[strings.ToLower(name)]; len(u) > 0 {
		return invalidf("%q is used by %s", name, strings.Join(u, "، "))
	}
	_, err = s.db.Exec(ctx, `DELETE FROM policy_objects WHERE id = $1`, id)
	return err
}

// Usage maps lower-cased object names to the packages and policies that
// refer to them (latest version and draft), and to groups.
func (s *Store) Usage(ctx context.Context) (map[string][]string, error) {
	pkgs, err := s.Packages(ctx)
	if err != nil {
		return nil, err
	}
	out := map[string][]string{}
	add := func(name, where string) {
		k := strings.ToLower(name)
		if !slices.Contains(out[k], where) {
			out[k] = append(out[k], where)
		}
	}
	for _, p := range pkgs {
		lists := [][]Policy{p.Draft}
		if p.Version > 0 {
			v, err := s.Version(ctx, p.ID, p.Version)
			if err != nil {
				return nil, err
			}
			lists = append(lists, v.Policies)
		}
		for _, l := range lists {
			for _, pol := range l {
				where := p.Name + ": Policy " + strconv.Itoa(pol.ID)
				for _, n := range slices.Concat(pol.SrcAddr, pol.DstAddr, pol.Service, pol.IPPool, []string{pol.Schedule}) {
					add(n, where)
				}
			}
		}
	}
	objs, err := s.Objects(ctx)
	if err != nil {
		return nil, err
	}
	for _, o := range objs {
		for _, m := range o.Value.Members {
			add(m, "گروه "+o.Name)
		}
	}
	return out, nil
}

// --- interface mappings ---

// Interfaces returns the zone → interface mapping of every device.
func (s *Store) Interfaces(ctx context.Context) (map[int64]map[string]string, error) {
	rows, err := s.db.Query(ctx, `SELECT device_id, zone, interface FROM interface_mappings`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]map[string]string{}
	for rows.Next() {
		var d int64
		var z, i string
		if err := rows.Scan(&d, &z, &i); err != nil {
			return nil, err
		}
		if out[d] == nil {
			out[d] = map[string]string{}
		}
		out[d][z] = i
	}
	return out, rows.Err()
}

// SetInterfaces replaces a device's interface mapping.
func (s *Store) SetInterfaces(ctx context.Context, deviceID int64, m map[string]string) error {
	for z, i := range m {
		if !nameRe.MatchString(z) || !nameRe.MatchString(i) || strings.ContainsAny(z, " ") {
			return invalidf("zone and interface names are required (zones without spaces)")
		}
	}
	return s.inTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM interface_mappings WHERE device_id = $1`, deviceID); err != nil {
			return err
		}
		for z, i := range m {
			if _, err := tx.Exec(ctx, `INSERT INTO interface_mappings (device_id, zone, interface) VALUES ($1, $2, $3)`, deviceID, z, i); err != nil {
				return err
			}
		}
		return nil
	})
}

// --- packages ---

// Package is a policy package. Draft holds unpublished edits.
type Package struct {
	ID          int64      `json:"id"`
	Name        string     `json:"name"`
	Description string     `json:"description"`
	DeviceIDs   []int64    `json:"device_ids"`
	VDOM        string     `json:"vdom"`
	Version     int        `json:"version"`
	Draft       []Policy   `json:"draft"`
	HasDraft    bool       `json:"has_draft"`
	DraftBase   *int       `json:"draft_base"`
	LockedBy    string     `json:"locked_by"`
	LockedAt    *time.Time `json:"locked_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

// VersionInfo is a published version.
type VersionInfo struct {
	Version   int       `json:"version"`
	Policies  []Policy  `json:"policies"`
	Note      string    `json:"note"`
	CreatedBy string    `json:"created_by"`
	CreatedAt time.Time `json:"created_at"`
}

const packageCols = `id, name, description, device_ids, vdom, version, draft, draft_base, coalesce(locked_by, ''), locked_at, updated_at`

func scanPackage(row pgx.Row) (*Package, error) {
	var p Package
	var draft []byte
	err := row.Scan(&p.ID, &p.Name, &p.Description, &p.DeviceIDs, &p.VDOM, &p.Version, &draft, &p.DraftBase, &p.LockedBy, &p.LockedAt, &p.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	p.Draft = []Policy{}
	if draft != nil {
		p.HasDraft = true
		_ = json.Unmarshal(draft, &p.Draft)
	}
	return &p, nil
}

// Packages lists packages.
func (s *Store) Packages(ctx context.Context) ([]*Package, error) {
	rows, err := s.db.Query(ctx, `SELECT `+packageCols+` FROM policy_packages ORDER BY lower(name)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Package{}
	for rows.Next() {
		p, err := scanPackage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Package returns one package.
func (s *Store) Package(ctx context.Context, id int64) (*Package, error) {
	return scanPackage(s.db.QueryRow(ctx, `SELECT `+packageCols+` FROM policy_packages WHERE id = $1`, id))
}

// SavePackage creates (ID 0) or updates a package's name, devices and VDOM.
// A device belongs to at most one package.
func (s *Store) SavePackage(ctx context.Context, p Package) (int64, error) {
	if err := validName(p.Name); err != nil {
		return 0, err
	}
	if p.VDOM == "" {
		p.VDOM = "root"
	}
	if p.DeviceIDs == nil {
		p.DeviceIDs = []int64{}
	}
	others, err := s.Packages(ctx)
	if err != nil {
		return 0, err
	}
	for _, o := range others {
		if o.ID == p.ID {
			continue
		}
		for _, d := range p.DeviceIDs {
			if slices.Contains(o.DeviceIDs, d) {
				return 0, invalidf("a device is already in package %q", o.Name)
			}
		}
	}
	if p.ID == 0 {
		err = s.db.QueryRow(ctx, `INSERT INTO policy_packages (name, description, device_ids, vdom) VALUES ($1, $2, $3, $4) RETURNING id`,
			p.Name, p.Description, p.DeviceIDs, p.VDOM).Scan(&p.ID)
	} else {
		var tag interface{ RowsAffected() int64 }
		tag, err = s.db.Exec(ctx, `UPDATE policy_packages SET name = $2, description = $3, device_ids = $4, vdom = $5, updated_at = now() WHERE id = $1`,
			p.ID, p.Name, p.Description, p.DeviceIDs, p.VDOM)
		if err == nil && tag.RowsAffected() == 0 {
			return 0, ErrNotFound
		}
	}
	if err != nil && strings.Contains(err.Error(), "policy_packages_name_key") {
		return 0, invalidf("a package with this name already exists")
	}
	return p.ID, err
}

// DeletePackage removes a package and its versions.
func (s *Store) DeletePackage(ctx context.Context, id int64) error {
	tag, err := s.db.Exec(ctx, `DELETE FROM policy_packages WHERE id = $1`, id)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// Lock takes (or renews) the edit lock for user.
func (s *Store) Lock(ctx context.Context, id int64, user string) (*Package, error) {
	now := s.now()
	tag, err := s.db.Exec(ctx, `UPDATE policy_packages SET locked_by = $2, locked_at = $3
		WHERE id = $1 AND (locked_by IS NULL OR locked_by = $2 OR locked_at < $4)`, id, user, now, now.Add(-lockTTL))
	if err != nil {
		return nil, err
	}
	p, err := s.Package(ctx, id)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		return p, ErrLocked
	}
	return p, nil
}

// Unlock releases the lock if user holds it.
func (s *Store) Unlock(ctx context.Context, id int64, user string) error {
	_, err := s.db.Exec(ctx, `UPDATE policy_packages SET locked_by = NULL, locked_at = NULL WHERE id = $1 AND locked_by = $2`, id, user)
	return err
}

func (s *Store) holdsLock(ctx context.Context, tx pgx.Tx, id int64, user string) (*Package, error) {
	p, err := scanPackage(tx.QueryRow(ctx, `SELECT `+packageCols+` FROM policy_packages WHERE id = $1 FOR UPDATE`, id))
	if err != nil {
		return nil, err
	}
	if p.LockedBy != user || p.LockedAt == nil || p.LockedAt.Before(s.now().Add(-lockTTL)) {
		return p, invalidf("take the edit lock first")
	}
	return p, nil
}

// SaveDraft stores edited policies under the user's lock.
func (s *Store) SaveDraft(ctx context.Context, id int64, user string, policies []Policy) error {
	if policies == nil {
		policies = []Policy{}
	}
	if err := ValidatePolicies(policies); err != nil {
		return err
	}
	data, _ := json.Marshal(policies)
	return s.inTx(ctx, func(tx pgx.Tx) error {
		p, err := s.holdsLock(ctx, tx, id, user)
		if err != nil {
			return err
		}
		base := p.DraftBase
		if !p.HasDraft {
			v := p.Version
			base = &v
		}
		_, err = tx.Exec(ctx, `UPDATE policy_packages SET draft = $2, draft_base = $3, locked_at = $4, updated_at = now() WHERE id = $1`,
			id, data, base, s.now())
		return err
	})
}

// DiscardDraft drops unpublished edits.
func (s *Store) DiscardDraft(ctx context.Context, id int64, user string) error {
	return s.inTx(ctx, func(tx pgx.Tx) error {
		if _, err := s.holdsLock(ctx, tx, id, user); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE policy_packages SET draft = NULL, draft_base = NULL WHERE id = $1`, id)
		return err
	})
}

// Publish turns the draft into the next version and releases the lock.
func (s *Store) Publish(ctx context.Context, id int64, user, note string) (int, error) {
	var version int
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		p, err := s.holdsLock(ctx, tx, id, user)
		if err != nil {
			return err
		}
		if !p.HasDraft {
			return invalidf("there are no changes to publish")
		}
		if p.DraftBase != nil && *p.DraftBase != p.Version {
			return invalidf("the package changed since this draft started (v%d); discard the draft and edit again", p.Version)
		}
		version = p.Version + 1
		data, _ := json.Marshal(p.Draft)
		if _, err := tx.Exec(ctx, `INSERT INTO policy_package_versions (package_id, version, policies, note, created_by) VALUES ($1, $2, $3, $4, $5)`,
			id, version, data, note, user); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE policy_packages SET version = $2, draft = NULL, draft_base = NULL, locked_by = NULL, locked_at = NULL, updated_at = now()
			WHERE id = $1`, id, version)
		return err
	})
	return version, err
}

// Versions lists a package's versions, newest first, without policies.
func (s *Store) Versions(ctx context.Context, id int64) ([]VersionInfo, error) {
	rows, err := s.db.Query(ctx, `SELECT version, '[]'::jsonb, note, created_by, created_at FROM policy_package_versions
		WHERE package_id = $1 ORDER BY version DESC`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []VersionInfo{}
	for rows.Next() {
		var v VersionInfo
		var pol []byte
		if err := rows.Scan(&v.Version, &pol, &v.Note, &v.CreatedBy, &v.CreatedAt); err != nil {
			return nil, err
		}
		v.Policies = []Policy{}
		out = append(out, v)
	}
	return out, rows.Err()
}

// Version returns one version with its policies.
func (s *Store) Version(ctx context.Context, id int64, version int) (*VersionInfo, error) {
	var v VersionInfo
	var pol []byte
	err := s.db.QueryRow(ctx, `SELECT version, policies, note, created_by, created_at FROM policy_package_versions
		WHERE package_id = $1 AND version = $2`, id, version).Scan(&v.Version, &pol, &v.Note, &v.CreatedBy, &v.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal(pol, &v.Policies)
	if v.Policies == nil {
		v.Policies = []Policy{}
	}
	return &v, nil
}

// Revert starts a draft from an older version (published like any edit).
func (s *Store) Revert(ctx context.Context, id int64, user string, version int) error {
	v, err := s.Version(ctx, id, version)
	if err != nil {
		return err
	}
	return s.SaveDraft(ctx, id, user, v.Policies)
}

func (s *Store) inTx(ctx context.Context, fn func(pgx.Tx) error) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
