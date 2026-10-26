package fileserver

import (
	"log/slog"
	"net"
	"strconv"
	"strings"
)

// Config selects the listeners. Empty addresses disable a protocol.
type Config struct {
	TFTPAddr     string // e.g. ":6969", mapped to UDP 69 on the host
	FTPAddr      string // e.g. ":2121", mapped to TCP 21
	PublicIP     string // the address devices use to reach TaktaPlus; empty = route-based guess per device
	PassiveRange string // e.g. "30000-30009"
	FTPPublic    int    // FTP port as devices see it (after port mapping); 0 = the listen port
}

// Server runs the TFTP and FTP listeners over one grant registry.
type Server struct {
	*Registry
	cfg  Config
	tftp *TFTP
	ftp  *FTP
	log  *slog.Logger
}

// New returns a server; call Start to listen.
func New(cfg Config, open Opener, log *slog.Logger) *Server {
	reg := NewRegistry()
	s := &Server{Registry: reg, cfg: cfg, log: log}
	if cfg.TFTPAddr != "" {
		s.tftp = NewTFTP(reg, open, log)
	}
	if cfg.FTPAddr != "" {
		lo, hi := parseRange(cfg.PassiveRange)
		s.ftp = NewFTP(reg, open, log, cfg.PublicIP, lo, hi)
	}
	return s
}

func parseRange(r string) (int, int) {
	a, b, ok := strings.Cut(r, "-")
	if !ok {
		return 0, 0
	}
	lo, err1 := strconv.Atoi(strings.TrimSpace(a))
	hi, err2 := strconv.Atoi(strings.TrimSpace(b))
	if err1 != nil || err2 != nil || lo <= 0 || hi < lo || hi > 65535 {
		return 0, 0
	}
	return lo, hi
}

// Start launches the listeners in the background.
func (s *Server) Start() {
	if s.tftp != nil {
		go func() {
			s.log.Info("tftp listening", "addr", s.cfg.TFTPAddr)
			if err := s.tftp.Serve(s.cfg.TFTPAddr); err != nil {
				s.log.Error("tftp server stopped", "err", err)
			}
		}()
	}
	if s.ftp != nil {
		go func() {
			s.log.Info("ftp listening", "addr", s.cfg.FTPAddr)
			if err := s.ftp.Serve(s.cfg.FTPAddr); err != nil && !strings.Contains(err.Error(), "use of closed") {
				s.log.Error("ftp server stopped", "err", err)
			}
		}()
	}
}

// Stop closes the listeners.
func (s *Server) Stop() {
	if s.tftp != nil {
		s.tftp.Shutdown()
	}
	if s.ftp != nil {
		_ = s.ftp.Close()
	}
}

// TFTPEnabled and FTPEnabled report which protocols are on.
func (s *Server) TFTPEnabled() bool { return s.tftp != nil }
func (s *Server) FTPEnabled() bool  { return s.ftp != nil }

// FTPPort is the port devices connect to.
func (s *Server) FTPPort() int {
	if s.cfg.FTPPublic > 0 {
		return s.cfg.FTPPublic
	}
	_, p, _ := net.SplitHostPort(s.cfg.FTPAddr)
	n, _ := strconv.Atoi(p)
	return n
}

// AddressFor returns the address a device at host should pull files from:
// the configured public address, or the local address the operating system
// would use to reach the device.
func (s *Server) AddressFor(host string) string {
	if s.cfg.PublicIP != "" {
		return s.cfg.PublicIP
	}
	c, err := net.Dial("udp", net.JoinHostPort(host, "69"))
	if err != nil {
		return ""
	}
	defer c.Close()
	return c.LocalAddr().(*net.UDPAddr).IP.String()
}
