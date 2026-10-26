package inventory

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/farshadfighter/taktaplus/internal/device"
)

// Status is a device's latest health and signature snapshot.
type Status struct {
	At           time.Time          `json:"at"`
	CPU          float64            `json:"cpu"`
	Mem          float64            `json:"mem"`
	Disk         float64            `json:"disk"`
	Sessions     int64              `json:"sessions"`
	Uptime       int64              `json:"uptime"`
	Interfaces   []device.Interface `json:"interfaces"`
	Signatures   []device.Signature `json:"signatures"`
	Contracts    []device.Contract  `json:"contracts"`
	SignaturesAt *time.Time         `json:"signatures_at"`
}

// SaveHealth stores a health snapshot and appends it to the metrics series.
func (s *Service) SaveHealth(ctx context.Context, id int64, h device.Health) error {
	ifs, err := json.Marshal(nonNilIfs(h.Interfaces))
	if err != nil {
		return err
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `
		INSERT INTO device_status (device_id, at, cpu, mem, disk, sessions, uptime, interfaces)
		VALUES ($1, now(), $2, $3, $4, $5, $6, $7)
		ON CONFLICT (device_id) DO UPDATE SET at = now(), cpu = $2, mem = $3, disk = $4, sessions = $5, uptime = $6, interfaces = $7`,
		id, h.CPU, h.Mem, h.Disk, h.Sessions, h.Uptime, ifs); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO device_metrics (device_id, at, cpu, mem, disk, sessions) VALUES ($1, now(), $2, $3, $4, $5)
		ON CONFLICT DO NOTHING`, id, h.CPU, h.Mem, h.Disk, h.Sessions); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE devices SET status = 'online', last_error = '', last_seen_at = now(), offline_since = NULL WHERE id = $1`, id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func nonNilIfs(v []device.Interface) []device.Interface {
	if v == nil {
		return []device.Interface{}
	}
	return v
}

// SaveSignatures stores installed signature versions and contracts.
func (s *Service) SaveSignatures(ctx context.Context, id int64, sigs []device.Signature, contracts []device.Contract) error {
	if sigs == nil {
		sigs = []device.Signature{}
	}
	if contracts == nil {
		contracts = []device.Contract{}
	}
	sj, _ := json.Marshal(sigs)
	cj, _ := json.Marshal(contracts)
	_, err := s.db.Exec(ctx, `
		INSERT INTO device_status (device_id, at, signatures, contracts, signatures_at) VALUES ($1, now(), $2, $3, now())
		ON CONFLICT (device_id) DO UPDATE SET signatures = $2, contracts = $3, signatures_at = now()`, id, sj, cj)
	return err
}

// Status returns the latest snapshot, or nil if the device was never polled.
func (s *Service) Status(ctx context.Context, id int64) (*Status, error) {
	var st Status
	var ifs, sigs, contracts []byte
	err := s.db.QueryRow(ctx, `SELECT at, cpu, mem, disk, sessions, uptime, interfaces, signatures, contracts, signatures_at
		FROM device_status WHERE device_id = $1`, id).
		Scan(&st.At, &st.CPU, &st.Mem, &st.Disk, &st.Sessions, &st.Uptime, &ifs, &sigs, &contracts, &st.SignaturesAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal(ifs, &st.Interfaces)
	_ = json.Unmarshal(sigs, &st.Signatures)
	_ = json.Unmarshal(contracts, &st.Contracts)
	return &st, nil
}

// Statuses returns the latest snapshot of every polled device.
func (s *Service) Statuses(ctx context.Context) (map[int64]*Status, error) {
	rows, err := s.db.Query(ctx, `SELECT device_id, at, cpu, mem, disk, sessions, uptime, contracts FROM device_status`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]*Status{}
	for rows.Next() {
		var id int64
		var st Status
		var contracts []byte
		if err := rows.Scan(&id, &st.At, &st.CPU, &st.Mem, &st.Disk, &st.Sessions, &st.Uptime, &contracts); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(contracts, &st.Contracts)
		out[id] = &st
	}
	return out, rows.Err()
}

// Metric is one point of the health series.
type Metric struct {
	At       time.Time `json:"at"`
	CPU      float64   `json:"cpu"`
	Mem      float64   `json:"mem"`
	Disk     float64   `json:"disk"`
	Sessions int64     `json:"sessions"`
}

// Metrics returns the series since the given time, averaged into at most
// about 300 buckets.
func (s *Service) Metrics(ctx context.Context, id int64, since time.Time) ([]Metric, error) {
	bucket := max(time.Since(since)/300, time.Minute)
	rows, err := s.db.Query(ctx, `
		SELECT to_timestamp(floor(extract(epoch FROM at) / $3) * $3) AS b,
		       avg(cpu), avg(mem), avg(disk), avg(sessions)::bigint
		FROM device_metrics WHERE device_id = $1 AND at >= $2
		GROUP BY b ORDER BY b`, id, since, bucket.Seconds())
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Metric, error) {
		var m Metric
		err := r.Scan(&m.At, &m.CPU, &m.Mem, &m.Disk, &m.Sessions)
		return m, err
	})
}

// PruneMetrics deletes points older than the retention period.
func (s *Service) PruneMetrics(ctx context.Context, keep time.Duration) error {
	_, err := s.db.Exec(ctx, `DELETE FROM device_metrics WHERE at < $1`, time.Now().Add(-keep))
	return err
}

// --- groups ---

// Group is a set of devices used for filtering, alert scope and
// (later) deployments.
type Group struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Devices     int    `json:"devices"`
}

// ListGroups returns groups with their device counts.
func (s *Service) ListGroups(ctx context.Context) ([]Group, error) {
	rows, err := s.db.Query(ctx, `SELECT g.id, g.name, g.description, (SELECT count(*) FROM devices d WHERE d.group_id = g.id)
		FROM device_groups g ORDER BY lower(g.name)`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[Group])
}

// SaveGroup creates (id 0) or renames a group.
func (s *Service) SaveGroup(ctx context.Context, id int64, name, description string) (int64, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 100 {
		return 0, invalidf("group name is required (at most 100 characters)")
	}
	var err error
	if id == 0 {
		err = s.db.QueryRow(ctx, `INSERT INTO device_groups (name, description) VALUES ($1, $2) RETURNING id`, name, description).Scan(&id)
	} else {
		var tag interface{ RowsAffected() int64 }
		tag, err = s.db.Exec(ctx, `UPDATE device_groups SET name = $2, description = $3 WHERE id = $1`, id, name, description)
		if err == nil && tag.RowsAffected() == 0 {
			return 0, ErrNotFound
		}
	}
	return id, mapDBError(err)
}

// DeleteGroup removes a group; its devices become ungrouped.
func (s *Service) DeleteGroup(ctx context.Context, id int64) error {
	tag, err := s.db.Exec(ctx, `DELETE FROM device_groups WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
