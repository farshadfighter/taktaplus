package fileserver

import (
	"bufio"
	"fmt"
	"io"
	"log/slog"
	"net"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// FTP is a minimal read-only FTP server (passive mode only) for devices
// that pull files with "execute restore ... ftp". Each grant has its own
// one-time account; connections from addresses without a grant are closed.
type FTP struct {
	reg      *Registry
	open     Opener
	log      *slog.Logger
	publicIP string // advertised in PASV replies; empty = the control connection's local address
	portMin  int    // passive port range; 0 = any free port
	portMax  int

	mu sync.Mutex
	ln net.Listener
}

// NewFTP returns an FTP server. publicIP and the passive port range are
// for NAT and container port mapping.
func NewFTP(reg *Registry, open Opener, log *slog.Logger, publicIP string, portMin, portMax int) *FTP {
	return &FTP{reg: reg, open: open, log: log, publicIP: publicIP, portMin: portMin, portMax: portMax}
}

// Serve accepts connections on addr until Close.
func (f *FTP) Serve(addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	return f.ServeListener(ln)
}

// ServeListener accepts connections on ln until Close.
func (f *FTP) ServeListener(ln net.Listener) error {
	f.mu.Lock()
	f.ln = ln
	f.mu.Unlock()
	for {
		c, err := ln.Accept()
		if err != nil {
			return err
		}
		go f.handle(c)
	}
}

// Close stops accepting connections.
func (f *FTP) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ln != nil {
		return f.ln.Close()
	}
	return nil
}

type ftpSession struct {
	f     *FTP
	c     net.Conn
	r     *bufio.Reader
	user  string
	grant *Grant
	pasv  net.Listener
}

func (s *ftpSession) reply(code int, msg string) {
	_ = s.c.SetWriteDeadline(time.Now().Add(30 * time.Second))
	fmt.Fprintf(s.c, "%d %s\r\n", code, msg)
}

func (f *FTP) handle(c net.Conn) {
	defer c.Close()
	if !f.reg.allowed(c.RemoteAddr()) {
		f.log.Warn("ftp connection refused", "from", hostOf(c.RemoteAddr()))
		fmt.Fprintf(c, "421 Not allowed\r\n")
		return
	}
	s := &ftpSession{f: f, c: c, r: bufio.NewReader(io.LimitReader(c, 1<<20))}
	defer func() {
		if s.pasv != nil {
			s.pasv.Close()
		}
	}()
	s.reply(220, "TaktaPlus file server")
	for {
		_ = c.SetReadDeadline(time.Now().Add(5 * time.Minute))
		line, err := s.r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		cmd, arg, _ := strings.Cut(line, " ")
		cmd = strings.ToUpper(cmd)
		if !s.command(cmd, arg) {
			return
		}
	}
}

// command runs one command; false ends the session.
func (s *ftpSession) command(cmd, arg string) bool {
	switch cmd {
	case "USER":
		s.user, s.grant = arg, nil
		s.reply(331, "Password required")
		return true
	case "PASS":
		g, ok := s.f.reg.login(s.c.RemoteAddr(), s.user, arg)
		if !ok {
			s.f.log.Warn("ftp login failed", "from", hostOf(s.c.RemoteAddr()), "user", s.user)
			s.reply(530, "Login incorrect")
			return false
		}
		s.grant = g
		s.reply(230, "Logged in")
		return true
	case "QUIT":
		s.reply(221, "Bye")
		return false
	case "SYST":
		s.reply(215, "UNIX Type: L8")
		return true
	case "FEAT":
		_, _ = io.WriteString(s.c, "211-Features:\r\n PASV\r\n EPSV\r\n SIZE\r\n UTF8\r\n211 End\r\n")
		return true
	case "NOOP":
		s.reply(200, "OK")
		return true
	}
	if s.grant == nil {
		s.reply(530, "Please log in")
		return true
	}
	switch cmd {
	case "OPTS", "MODE", "STRU":
		s.reply(200, "OK")
	case "TYPE":
		s.reply(200, "Type set")
	case "PWD", "XPWD":
		s.reply(257, `"/" is the current directory`)
	case "CWD", "XCWD":
		if arg == "/" || arg == "" || arg == "." {
			s.reply(250, "OK")
		} else {
			s.reply(550, "No such directory")
		}
	case "PASV", "EPSV":
		s.passive(cmd == "EPSV")
	case "SIZE":
		if sha, ok := s.file(arg); ok {
			if f, size, err := s.f.open(sha); err == nil {
				f.Close()
				s.reply(213, strconv.FormatInt(size, 10))
				return true
			}
		}
		s.reply(550, "File not found")
	case "RETR":
		s.retr(arg)
	case "LIST", "NLST":
		s.list(cmd == "LIST")
	default:
		s.reply(502, "Command not implemented")
	}
	return true
}

func (s *ftpSession) file(name string) (string, bool) {
	if !time.Now().Before(s.grant.Expires) {
		return "", false
	}
	sha, ok := s.grant.Files[strings.TrimPrefix(path.Clean("/"+name), "/")]
	return sha, ok
}

func (s *ftpSession) passive(extended bool) {
	if s.pasv != nil {
		s.pasv.Close()
		s.pasv = nil
	}
	local := s.c.LocalAddr().(*net.TCPAddr)
	var ln net.Listener
	var err error
	if s.f.portMin > 0 {
		for p := s.f.portMin; p <= s.f.portMax; p++ {
			if ln, err = net.Listen("tcp", net.JoinHostPort(local.IP.String(), strconv.Itoa(p))); err == nil {
				break
			}
		}
	} else {
		ln, err = net.Listen("tcp", net.JoinHostPort(local.IP.String(), "0"))
	}
	if ln == nil || err != nil {
		s.reply(425, "No passive port available")
		return
	}
	s.pasv = ln
	port := ln.Addr().(*net.TCPAddr).Port
	if extended {
		s.reply(229, fmt.Sprintf("Entering Extended Passive Mode (|||%d|)", port))
		return
	}
	ip := net.ParseIP(s.f.publicIP)
	if ip == nil {
		ip = local.IP
	}
	ip4 := ip.To4()
	if ip4 == nil {
		s.reply(425, "Use EPSV for IPv6")
		return
	}
	s.reply(227, fmt.Sprintf("Entering Passive Mode (%d,%d,%d,%d,%d,%d)", ip4[0], ip4[1], ip4[2], ip4[3], port>>8, port&0xff))
}

// dataConn accepts the passive connection, which must come from the same
// address as the control connection.
func (s *ftpSession) dataConn() (net.Conn, bool) {
	if s.pasv == nil {
		s.reply(425, "Use PASV first")
		return nil, false
	}
	ln := s.pasv
	s.pasv = nil
	defer ln.Close()
	_ = ln.(*net.TCPListener).SetDeadline(time.Now().Add(30 * time.Second))
	dc, err := ln.Accept()
	if err != nil {
		s.reply(425, "Data connection failed")
		return nil, false
	}
	if hostOf(dc.RemoteAddr()) != hostOf(s.c.RemoteAddr()) {
		dc.Close()
		s.reply(425, "Data connection from another address")
		return nil, false
	}
	return dc, true
}

func (s *ftpSession) retr(name string) {
	sha, ok := s.file(name)
	if !ok {
		s.reply(550, "File not found")
		return
	}
	f, _, err := s.f.open(sha)
	if err != nil {
		s.reply(550, "File not available")
		return
	}
	defer f.Close()
	dc, ok := s.dataConn()
	if !ok {
		return
	}
	s.reply(150, "Opening data connection")
	n, err := io.Copy(dc, f)
	dc.Close()
	s.f.log.Info("ftp transfer", "to", hostOf(s.c.RemoteAddr()), "file", name, "bytes", n, "err", err)
	if err != nil {
		s.reply(426, "Transfer aborted")
		return
	}
	s.reply(226, "Transfer complete")
}

func (s *ftpSession) list(long bool) {
	dc, ok := s.dataConn()
	if !ok {
		return
	}
	s.reply(150, "Here comes the listing")
	names := make([]string, 0, len(s.grant.Files))
	for name := range s.grant.Files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if long {
			size := int64(0)
			if f, sz, err := s.f.open(s.grant.Files[name]); err == nil {
				f.Close()
				size = sz
			}
			fmt.Fprintf(dc, "-r--r--r-- 1 taktaplus taktaplus %d Jan 01 00:00 %s\r\n", size, name)
		} else {
			fmt.Fprintf(dc, "%s\r\n", name)
		}
	}
	dc.Close()
	s.reply(226, "Directory send OK")
}
