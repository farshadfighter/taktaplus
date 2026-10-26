package fileserver

import (
	"errors"
	"io"
	"log/slog"
	"net"

	"github.com/pin/tftp/v3"
)

// TFTP serves granted files read-only; writes are refused.
type TFTP struct {
	reg  *Registry
	open Opener
	log  *slog.Logger
	srv  *tftp.Server
}

// NewTFTP returns a TFTP server.
func NewTFTP(reg *Registry, open Opener, log *slog.Logger) *TFTP {
	t := &TFTP{reg: reg, open: open, log: log}
	t.srv = tftp.NewServer(t.read, nil)
	return t
}

var errDenied = errors.New("file not found")

func (t *TFTP) read(name string, rf io.ReaderFrom) error {
	ot, ok := rf.(tftp.OutgoingTransfer)
	if !ok {
		return errDenied
	}
	addr := ot.RemoteAddr()
	sha, ok := t.reg.lookup(&addr, name)
	if !ok {
		t.log.Warn("tftp request denied", "from", addr.IP.String(), "file", name)
		return errDenied
	}
	f, size, err := t.open(sha)
	if err != nil {
		return err
	}
	defer f.Close()
	ot.SetSize(size)
	n, err := rf.ReadFrom(f)
	t.log.Info("tftp transfer", "to", addr.IP.String(), "file", name, "bytes", n, "err", err)
	return err
}

// Serve listens on addr (e.g. ":69") until Shutdown.
func (t *TFTP) Serve(addr string) error {
	conn, err := net.ListenPacket("udp", addr)
	if err != nil {
		return err
	}
	return t.srv.Serve(conn.(*net.UDPConn))
}

// Shutdown stops the server.
func (t *TFTP) Shutdown() { t.srv.Shutdown() }
