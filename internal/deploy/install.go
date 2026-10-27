// Package deploy installs signature packages (module B) and firmware images
// (F2) on devices, as change workflow runners.
package deploy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/farshadfighter/taktaplus/internal/device"
	"github.com/farshadfighter/taktaplus/internal/fileserver"
	"github.com/farshadfighter/taktaplus/internal/inventory"
	"github.com/farshadfighter/taktaplus/internal/packages"
)

// Install methods.
const (
	MethodAuto = "auto" // API upload when available, else TFTP, else FTP
	MethodAPI  = "api"
	MethodTFTP = "tftp"
	MethodFTP  = "ftp"
)

// pullCommand returns the CLI that makes a device fetch a package from the
// TaktaPlus file server, or "" when the type cannot be pulled.
//
// FortiOS: "execute restore {ips|av|image} {tftp|ftp} …". The FortiWeb
// commands for signature databases are not known well enough yet, so only
// firmware is pulled on FortiWeb.
func pullCommand(platform, typ, proto, file, server string, ftpPort int, user, pass string) string {
	var what string
	switch platform {
	case "fortigate":
		switch typ {
		case "ips_db", "ips_engine", "appctrl_db", "botnet_db", "ot_db", "isdb":
			what = "ips"
		case "av_db", "av_engine":
			what = "av"
		case "firmware":
			what = "image"
		}
	case "fortiweb":
		if typ == "firmware" {
			what = "image"
		}
	}
	if what == "" {
		return ""
	}
	if proto == MethodFTP {
		return fmt.Sprintf("execute restore %s ftp %s %s:%d %s %s", what, file, server, ftpPort, user, pass)
	}
	return fmt.Sprintf("execute restore %s tftp %s %s", what, file, server)
}

// Installer installs packages on devices.
type Installer struct {
	inv   *inventory.Service
	pkgs  *packages.Service
	files *fileserver.Server
	// Verification retries (signatures are reported a little after install).
	VerifyTries int
	VerifyWait  time.Duration
}

// NewInstaller returns an installer.
func NewInstaller(inv *inventory.Service, pkgs *packages.Service, files *fileserver.Server) *Installer {
	return &Installer{inv: inv, pkgs: pkgs, files: files, VerifyTries: 6, VerifyWait: 10 * time.Second}
}

// plan picks the method for installing p on d, or explains why it cannot.
func (in *Installer) plan(d *inventory.Device, drv device.Driver, p *packages.Package, method string) (string, error) {
	if method == "" {
		method = MethodAuto
	}
	_, hasAPI := device.AsInstaller(drv)
	_, hasCLI := device.AsCLI(drv)
	canPull := func(proto string) bool {
		if !hasCLI || in.files == nil {
			return false
		}
		if proto == MethodTFTP && !in.files.TFTPEnabled() || proto == MethodFTP && !in.files.FTPEnabled() {
			return false
		}
		return pullCommand(p.Platform, p.Type, proto, "f", "s", 21, "u", "p") != ""
	}
	switch method {
	case MethodAPI:
		if !hasAPI {
			return "", errors.New("installing through the API needs a FortiGate with an API token")
		}
		return MethodAPI, nil
	case MethodTFTP, MethodFTP:
		if !canPull(method) {
			return "", fmt.Errorf("pulling %s over %s needs SSH access and the built-in %s server", p.Type, strings.ToUpper(method), strings.ToUpper(method))
		}
		return method, nil
	case MethodAuto:
		switch {
		case hasAPI:
			return MethodAPI, nil
		case canPull(MethodTFTP):
			return MethodTFTP, nil
		case canPull(MethodFTP):
			return MethodFTP, nil
		}
		return "", fmt.Errorf("no way to install %s on this device: add an API token or SSH credentials", p.Type)
	}
	return "", fmt.Errorf("unknown install method %q", method)
}

// install puts p on d.
func (in *Installer) install(ctx context.Context, d *inventory.Device, drv device.Driver, p *packages.Package, method string, out io.Writer) error {
	method, err := in.plan(d, drv, p, method)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "# installing %s %s (%s) via %s\n", p.Type, p.Version, p.File, method)
	if method == MethodAPI {
		inst, _ := device.AsInstaller(drv)
		f, err := in.pkgs.Open(p.SHA256)
		if err != nil {
			return err
		}
		defer f.Close()
		return inst.InstallPackage(ctx, p.Type, p.File, f)
	}

	// Pull: grant the device this one file and have it fetch it.
	server := in.files.AddressFor(d.Host)
	if server == "" {
		return errors.New("cannot tell which address the device should use to reach TaktaPlus; set TAKTAPLUS_FILESERVER_IP")
	}
	ips := []string{d.Host}
	g := in.files.Grant(map[string]string{p.File: p.SHA256}, ips, 2*time.Hour)
	defer in.files.Revoke(g.ID)
	cmd := pullCommand(p.Platform, p.Type, method, g.Name(p.SHA256), server, in.files.FTPPort(), g.User, g.Password)
	cli, _ := device.AsCLI(drv)
	// FortiOS asks for confirmation before replacing a database or image.
	res, err := cli.RunScript(ctx, cmd+"\ny")
	fmt.Fprintln(out, strings.ReplaceAll(res, g.Password, "********"))
	return err
}

// verify checks that the device reports p's version after install.
func (in *Installer) verify(ctx context.Context, d *inventory.Device, drv device.Driver, p *packages.Package, out io.Writer) error {
	for try := 0; try < in.VerifyTries; try++ {
		if try > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(in.VerifyWait):
			}
		}
		sigs, contracts, err := drv.Signatures(ctx)
		if err != nil {
			continue
		}
		_ = in.inv.SaveSignatures(ctx, d.ID, sigs, contracts)
		for _, s := range sigs {
			if sameVersion(s.Version, p.Version) {
				fmt.Fprintf(out, "# verified: %s reports %s\n", s.Name, s.Version)
				return nil
			}
		}
	}
	return fmt.Errorf("the device does not report version %s after install", p.Version)
}

// sameVersion compares "28.00950" and "28.950" as equal.
func sameVersion(a, b string) bool {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	if len(pa) != len(pb) {
		return false
	}
	for i := range pa {
		if strings.TrimLeft(pa[i], "0") != strings.TrimLeft(pb[i], "0") {
			return false
		}
	}
	return true
}
