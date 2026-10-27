package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/farshadfighter/taktaplus/internal/changes"
	"github.com/farshadfighter/taktaplus/internal/inventory"
	"github.com/farshadfighter/taktaplus/internal/packages"
)

// KindSignatures is the change kind for signature deployments.
const KindSignatures = "signatures"

// SignatureSpec describes a signature deployment.
type SignatureSpec struct {
	// Packages are explicit packages by SHA-256; each is installed on the
	// devices it is compatible with and skipped on the others.
	Packages []string `json:"packages"`
	// Latest lists package types for which the newest compatible package
	// is chosen per device when the change runs.
	Latest []string `json:"latest"`
	Method string   `json:"method"` // auto, api, tftp, ftp
	Verify bool     `json:"verify"`
}

// Signatures is the runner for signature deployments.
type Signatures struct{ in *Installer }

// NewSignatures returns the runner.
func NewSignatures(in *Installer) *Signatures { return &Signatures{in: in} }

func parseSpec(raw json.RawMessage) (*SignatureSpec, error) {
	var s SignatureSpec
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, invalid("invalid deployment")
	}
	if len(s.Packages) == 0 && len(s.Latest) == 0 {
		return nil, invalid("choose packages or package types to install")
	}
	if slices.Contains(s.Latest, "firmware") {
		return nil, invalid("firmware is upgraded with a firmware change, not a signature deployment")
	}
	return &s, nil
}

func invalid(msg string) error { return changes.Invalid(msg) }

// resolve returns the packages to install on d, in install order.
func (r *Signatures) resolve(ctx context.Context, s *SignatureSpec, d *inventory.Device) ([]*packages.Package, error) {
	var out []*packages.Package
	for _, sha := range s.Packages {
		p, err := r.in.pkgs.BySHA(ctx, sha)
		if errors.Is(err, packages.ErrNotFound) {
			return nil, invalid("package " + sha[:min(12, len(sha))] + " is not in the repository")
		} else if err != nil {
			return nil, err
		}
		if p.Type == "firmware" {
			return nil, invalid("firmware is upgraded with a firmware change, not a signature deployment")
		}
		if p.Compatible(string(d.Kind), d.Model, d.Firmware) {
			out = append(out, p)
		}
	}
	for _, typ := range s.Latest {
		p, err := r.in.pkgs.Latest(ctx, string(d.Kind), typ, d.Model, d.Firmware)
		if errors.Is(err, packages.ErrNotFound) {
			continue
		} else if err != nil {
			return nil, err
		}
		if !slices.ContainsFunc(out, func(x *packages.Package) bool { return x.SHA256 == p.SHA256 }) {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return nil, invalid("no compatible package for this device (" + string(d.Kind) + " " + d.Model + " v" + d.Firmware + ")")
	}
	return out, nil
}

// Preview lists the packages and install method per device.
func (r *Signatures) Preview(ctx context.Context, raw json.RawMessage, d *inventory.Device) (string, error) {
	s, err := parseSpec(raw)
	if err != nil {
		return "", err
	}
	list, err := r.resolve(ctx, s, d)
	if err != nil {
		return "", err
	}
	drv := r.in.inv.Driver(d)
	var b strings.Builder
	for _, p := range list {
		method, err := r.in.plan(d, drv, p, s.Method)
		if err != nil {
			return "", invalid(err.Error())
		}
		fmt.Fprintf(&b, "%s %s (%s) via %s\n", p.Type, p.Version, p.File, method)
	}
	if len(s.Latest) > 0 {
		b.WriteString("Newest compatible packages are chosen again when the change runs.\n")
	}
	return strings.TrimSpace(b.String()), nil
}

// Run installs the packages and verifies them when asked.
func (r *Signatures) Run(ctx context.Context, c *changes.Change, d *inventory.Device, out io.Writer) error {
	s, err := parseSpec(c.Spec)
	if err != nil {
		return err
	}
	list, err := r.resolve(ctx, s, d)
	if err != nil {
		return err
	}
	drv := r.in.inv.Driver(d)
	for _, p := range list {
		if err := r.in.install(ctx, d, drv, p, s.Method, out); err != nil {
			return fmt.Errorf("%s %s: %w", p.Type, p.Version, err)
		}
		if s.Verify {
			if err := r.in.verify(ctx, d, drv, p, out); err != nil {
				return err
			}
		}
	}
	return nil
}
