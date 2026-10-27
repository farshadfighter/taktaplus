package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/farshadfighter/taktaplus-lic/pkg/manifest"

	"github.com/farshadfighter/taktaplus/internal/changes"
	"github.com/farshadfighter/taktaplus/internal/inventory"
	"github.com/farshadfighter/taktaplus/internal/packages"
)

// KindFirmware is the change kind for firmware upgrades.
const KindFirmware = "firmware"

// FirmwareSpec describes a firmware upgrade.
type FirmwareSpec struct {
	Version string `json:"version"` // target version, e.g. 7.4.4
	Method  string `json:"method"`  // auto, api, tftp, ftp
	// MaxCPU and MaxMem are pre-check limits in percent (default 90).
	MaxCPU float64 `json:"max_cpu"`
	MaxMem float64 `json:"max_mem"`
}

// Firmware is the runner for firmware upgrades.
type Firmware struct {
	in *Installer
	// RebootTimeout bounds the wait for a device after each image.
	RebootTimeout time.Duration
	RebootPoll    time.Duration
}

// NewFirmware returns the runner.
func NewFirmware(in *Installer) *Firmware {
	return &Firmware{in: in, RebootTimeout: 25 * time.Minute, RebootPoll: 30 * time.Second}
}

func parseFirmware(raw json.RawMessage) (*FirmwareSpec, error) {
	var s FirmwareSpec
	if err := json.Unmarshal(raw, &s); err != nil || strings.TrimSpace(s.Version) == "" {
		return nil, invalid("choose the target firmware version")
	}
	s.Version = strings.TrimPrefix(strings.TrimSpace(s.Version), "v")
	if s.MaxCPU == 0 {
		s.MaxCPU = 90
	}
	if s.MaxMem == 0 {
		s.MaxMem = 90
	}
	return &s, nil
}

// Path returns the images that take a device from its firmware to target,
// using each image's upgrade_from list (fewest steps, newest images).
func (f *Firmware) Path(ctx context.Context, d *inventory.Device, target string) ([]*packages.Package, error) {
	if d.Firmware == "" {
		return nil, invalid("the device's firmware version is unknown; refresh the device first")
	}
	if manifest.CompareVersions(target, d.Firmware) <= 0 {
		return nil, invalid(fmt.Sprintf("the device already runs %s; downgrades are not supported", d.Firmware))
	}
	list, err := f.in.pkgs.List(ctx, packages.Filter{Platform: string(d.Kind), Type: "firmware"})
	if err != nil {
		return nil, err
	}
	var images []*packages.Package
	for _, p := range list {
		if p.Compatible(string(d.Kind), d.Model, "") && manifest.CompareVersions(p.Version, d.Firmware) > 0 &&
			manifest.CompareVersions(p.Version, target) <= 0 {
			images = append(images, p)
		}
	}
	// Breadth-first search over versions; prev records the step taken.
	type node struct {
		version string
		img     *packages.Package
		prev    *node
	}
	queue := []*node{{version: d.Firmware}}
	seen := map[string]bool{d.Firmware: true}
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		if n.version == target {
			var path []*packages.Package
			for x := n; x.img != nil; x = x.prev {
				path = append([]*packages.Package{x.img}, path...)
			}
			return path, nil
		}
		// Prefer the furthest jump first so the path has the fewest steps.
		var next []*packages.Package
		for _, img := range images {
			if !seen[img.Version] && manifest.CompareVersions(img.Version, n.version) > 0 && img.Manifest().UpgradesFrom(n.version) {
				next = append(next, img)
			}
		}
		for i := range next {
			for j := i + 1; j < len(next); j++ {
				if manifest.CompareVersions(next[j].Version, next[i].Version) > 0 {
					next[i], next[j] = next[j], next[i]
				}
			}
		}
		for _, img := range next {
			seen[img.Version] = true
			queue = append(queue, &node{version: img.Version, img: img, prev: n})
		}
	}
	return nil, invalid(fmt.Sprintf("no upgrade path from %s to %s for %s with the images in the repository", d.Firmware, target, d.Model))
}

// Preview shows the upgrade path.
func (f *Firmware) Preview(ctx context.Context, raw json.RawMessage, d *inventory.Device) (string, error) {
	s, err := parseFirmware(raw)
	if err != nil {
		return "", err
	}
	path, err := f.Path(ctx, d, s.Version)
	if err != nil {
		return "", err
	}
	drv := f.in.inv.Driver(d)
	steps := []string{d.Firmware}
	for _, p := range path {
		if _, err := f.in.plan(d, drv, p, s.Method); err != nil {
			return "", invalid(err.Error())
		}
		steps = append(steps, p.Version)
	}
	text := "Upgrade path: " + strings.Join(steps, " → ")
	if d.HAMode != "" && d.HAMode != "standalone" {
		text += "\nHA cluster: FortiOS upgrades the other members itself; do not add them to the same change."
	}
	return text, nil
}

// precheck refuses to start on an unhealthy device.
func (f *Firmware) precheck(ctx context.Context, d *inventory.Device, s *FirmwareSpec, out io.Writer) error {
	h, err := f.in.inv.Driver(d).Health(ctx)
	if err != nil {
		return fmt.Errorf("pre-check: device is not reachable: %w", err)
	}
	if h.CPU > s.MaxCPU || h.Mem > s.MaxMem {
		return fmt.Errorf("pre-check: device is busy (CPU %.0f%%, memory %.0f%%)", h.CPU, h.Mem)
	}
	fmt.Fprintf(out, "# pre-check passed: CPU %.0f%%, memory %.0f%%\n", h.CPU, h.Mem)
	return nil
}

// Run upgrades step by step, waiting for the device to come back on each
// image's version.
func (f *Firmware) Run(ctx context.Context, c *changes.Change, d *inventory.Device, out io.Writer) error {
	s, err := parseFirmware(c.Spec)
	if err != nil {
		return err
	}
	path, err := f.Path(ctx, d, s.Version)
	if err != nil {
		return err
	}
	if err := f.precheck(ctx, d, s, out); err != nil {
		return err
	}
	for _, img := range path {
		drv := f.in.inv.Driver(d)
		if err := f.in.install(ctx, d, drv, img, s.Method, out); err != nil {
			return fmt.Errorf("image %s: %w", img.Version, err)
		}
		fmt.Fprintf(out, "# waiting for the device to restart on %s\n", img.Version)
		nd, err := f.waitFor(ctx, d.ID, img.Version)
		if err != nil {
			return err
		}
		d = nd
		fmt.Fprintf(out, "# device is back on %s build %s\n", d.Firmware, d.Build)
	}
	return nil
}

func (f *Firmware) waitFor(ctx context.Context, id int64, version string) (*inventory.Device, error) {
	deadline := time.Now().Add(f.RebootTimeout)
	var last error
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(f.RebootPoll):
		}
		pctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		d, err := f.in.inv.Probe(pctx, id)
		cancel()
		if err != nil {
			last = err
			continue
		}
		if sameVersion(d.Firmware, version) {
			return d, nil
		}
		last = fmt.Errorf("device reports %s", d.Firmware)
	}
	if last == nil {
		last = errors.New("no answer")
	}
	return nil, fmt.Errorf("the device did not come back on %s within %s: %v", version, f.RebootTimeout, last)
}
