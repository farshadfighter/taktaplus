package packages

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
)

// ImportBundle imports an offline bundle (zip with manifest.json,
// manifest.sig and files/<file>) produced by the license server. Files the
// bundle leaves out are skipped; the manifest must verify.
func (s *Service) ImportBundle(ctx context.Context, r io.ReaderAt, size int64, actor string) (*SyncRun, error) {
	zr, err := zip.NewReader(r, size)
	if err != nil {
		return nil, invalidf("the file is not a zip bundle")
	}
	entries := map[string]*zip.File{}
	for _, f := range zr.File {
		entries[f.Name] = f
	}
	read := func(name string) ([]byte, error) {
		f, ok := entries[name]
		if !ok {
			return nil, invalidf("the bundle has no %s", name)
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		defer rc.Close()
		return io.ReadAll(io.LimitReader(rc, 32<<20))
	}
	body, err := read("manifest.json")
	if err != nil {
		return nil, err
	}
	sig, err := read("manifest.sig")
	if err != nil {
		return nil, err
	}
	files, err := s.verifyManifest(body, string(sig))
	if err != nil {
		return nil, invalidf("the bundle's manifest is not valid: %v", err)
	}

	run := &SyncRun{Origin: "bundle", StartedAt: s.now(), Actor: actor}
	if err := s.db.QueryRow(ctx, `INSERT INTO package_sync_runs (origin, actor) VALUES ('bundle', $1) RETURNING id`, actor).Scan(&run.ID); err != nil {
		return nil, err
	}
	var errs []error
	for _, rf := range files {
		zf, ok := entries["files/"+rf.Name]
		if !ok {
			continue
		}
		run.Seen++
		m := rf.pkg
		p := &Package{SHA256: m.SHA256, File: m.File, Size: m.Size, Platform: string(m.Platform), Type: string(m.Type), Version: m.Version,
			Models: m.Models, FwMin: m.Firmware.Min, FwMax: m.Firmware.Max, UpgradeFrom: m.UpgradeFrom, Notes: m.Notes,
			ReleasedAt: m.ReleasedAt, Origin: "bundle", Verified: true}
		if ok, err := s.has(ctx, m.SHA256); err != nil {
			return nil, err
		} else if !ok {
			rc, err := zf.Open()
			if err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", rf.Name, err))
				continue
			}
			_, _, err = s.store(rc, m.SHA256)
			rc.Close()
			if err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", rf.Name, err))
				continue
			}
			run.Added++
		}
		if err := s.upsert(ctx, p); err != nil {
			return nil, err
		}
	}
	run.Status = "ok"
	if err := errors.Join(errs...); err != nil {
		run.Status, run.Error = "failed", truncate(err.Error(), 1000)
	}
	now := s.now()
	run.FinishedAt = &now
	_, err = s.db.Exec(context.WithoutCancel(ctx), `UPDATE package_sync_runs SET finished_at = $2, status = $3, added = $4, seen = $5, error = $6 WHERE id = $1`,
		run.ID, now, run.Status, run.Added, run.Seen, run.Error)
	return run, err
}
