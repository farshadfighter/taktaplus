package fileserver

import (
	"bytes"
	"errors"
	"io"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jlaffaye/ftp"
	"github.com/pin/tftp/v3"
)

type nopCloser struct{ *bytes.Reader }

func (nopCloser) Close() error { return nil }

var store = map[string]string{"aaa": "IPS DATABASE", "bbb": "FIRMWARE IMAGE"}

func opener(sha string) (io.ReadSeekCloser, int64, error) {
	c, ok := store[sha]
	if !ok {
		return nil, 0, errors.New("missing")
	}
	return nopCloser{bytes.NewReader([]byte(c))}, int64(len(c)), nil
}

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func TestTFTP(t *testing.T) {
	reg := NewRegistry()
	srv := NewTFTP(reg, opener, quiet)
	conn, _ := net.ListenPacket("udp", "127.0.0.1:0")
	go func() { _ = srv.srv.Serve(conn.(*net.UDPConn)) }()
	defer srv.Shutdown()
	addr := conn.LocalAddr().String()

	g := reg.Grant(map[string]string{"ips.pkg": "aaa"}, []string{"127.0.0.1"}, time.Minute)
	get := func(name string) (string, error) {
		c, _ := tftp.NewClient(addr)
		c.SetTimeout(time.Second)
		c.SetRetries(1)
		wt, err := c.Receive(name, "octet")
		if err != nil {
			return "", err
		}
		var buf bytes.Buffer
		_, err = wt.WriteTo(&buf)
		return buf.String(), err
	}
	if got, err := get(g.Name("aaa")); err != nil || got != "IPS DATABASE" {
		t.Fatalf("granted = %q %v", got, err)
	}
	if _, err := get("ips.pkg"); err == nil {
		t.Fatal("name without the grant token served")
	}
	reg.Revoke(g.ID)
	if _, err := get(g.Name("aaa")); err == nil {
		t.Fatal("revoked grant served")
	}
	// Another address is refused.
	g = reg.Grant(map[string]string{"ips.pkg": "aaa"}, []string{"10.9.9.9"}, time.Minute)
	if _, err := get(g.Name("aaa")); err == nil {
		t.Fatal("other address served")
	}
}

func TestFTP(t *testing.T) {
	reg := NewRegistry()
	srv := NewFTP(reg, opener, quiet, "", 0, 0)
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	go func() { _ = srv.ServeListener(ln) }()
	defer srv.Close()
	addr := ln.Addr().String()

	// No grant for this address: the connection is refused.
	if c, err := ftp.Dial(addr, ftp.DialWithTimeout(2*time.Second)); err == nil {
		c.Quit()
		t.Fatal("connection without a grant accepted")
	}
	g := reg.Grant(map[string]string{"fw.out": "bbb", "ips.pkg": "aaa"}, []string{"127.0.0.1"}, time.Minute)
	c, err := ftp.Dial(addr, ftp.DialWithTimeout(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Login(g.User, "wrong"); err == nil {
		t.Fatal("wrong password accepted")
	}
	c, _ = ftp.Dial(addr, ftp.DialWithTimeout(2*time.Second))
	defer c.Quit()
	if err := c.Login(g.User, g.Password); err != nil {
		t.Fatal(err)
	}
	names, err := c.NameList("/")
	if err != nil || len(names) != 2 {
		t.Fatalf("list = %v %v", names, err)
	}
	size, err := c.FileSize(g.Name("bbb"))
	if err != nil || size != int64(len(store["bbb"])) {
		t.Fatalf("size = %d %v", size, err)
	}
	r, err := c.Retr(g.Name("bbb"))
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(r)
	r.Close()
	if string(data) != "FIRMWARE IMAGE" {
		t.Fatalf("retr = %q", data)
	}
	if _, err := c.Retr("../etc/passwd"); err == nil {
		t.Fatal("path outside the grant served")
	}
}

func TestPassivePortRange(t *testing.T) {
	reg := NewRegistry()
	free, _ := net.Listen("tcp", "127.0.0.1:0")
	port := free.Addr().(*net.TCPAddr).Port
	free.Close()
	srv := NewFTP(reg, opener, quiet, "192.0.2.10", port, port)
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	go func() { _ = srv.ServeListener(ln) }()
	defer srv.Close()
	g := reg.Grant(map[string]string{"a": "aaa"}, []string{"127.0.0.1"}, time.Minute)

	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	buf := make([]byte, 512)
	read := func() string { n, _ := conn.Read(buf); return string(buf[:n]) }
	read()
	io.WriteString(conn, "USER "+g.User+"\r\n")
	read()
	io.WriteString(conn, "PASS "+g.Password+"\r\n")
	read()
	io.WriteString(conn, "PASV\r\n")
	reply := read()
	want := "192,0,2,10," + strconv.Itoa(port>>8) + "," + strconv.Itoa(port&0xff)
	if !strings.Contains(reply, want) {
		t.Fatalf("PASV = %q, want %s", reply, want)
	}
}
