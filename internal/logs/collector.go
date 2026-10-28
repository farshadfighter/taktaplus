package logs

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"slices"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/farshadfighter/taktaplus/internal/chstore"
	"github.com/farshadfighter/taktaplus/internal/inventory"
)

// Drop reasons.
const (
	DropUnparsable  = "unparsable"
	DropUnknown     = "unknown_device" // serial not registered in the panel
	DropUnlicensed  = "unlicensed"     // serial has no operational license
	DropUntrusted   = "untrusted_source"
	DropBufferFull  = "buffer_full" // the log store was unreachable for too long
	maxBuffered     = 200_000
	flushRows       = 5_000
	flushEvery      = 2 * time.Second
	maxTCPConns     = 512
	tcpIdle         = 10 * time.Minute
	maxMessageBytes = 64 << 10
)

// Listeners configures where the collector listens; empty disables one.
type Listeners struct {
	UDP string
	TCP string
	TLS string
	// TLSConfig is required when TLS is set.
	TLSConfig *tls.Config
}

type deviceEntry struct {
	id          int64
	name        string
	operational bool
	hostIPs     []netip.Addr
}

// Rejected is a sender whose logs were dropped, shown so an administrator
// can register the device or trust its address.
type Rejected struct {
	Serial string    `json:"serial"`
	IP     string    `json:"ip"`
	Reason string    `json:"reason"`
	Count  uint64    `json:"count"`
	LastAt time.Time `json:"last_at"`
}

// DeviceStat is per-device ingestion.
type DeviceStat struct {
	DeviceID int64     `json:"device_id"`
	Name     string    `json:"name"`
	Count    uint64    `json:"count"`
	LastAt   time.Time `json:"last_at"`
	SourceIP string    `json:"source_ip"`
}

// Stats summarizes ingestion since start.
type Stats struct {
	Received  uint64            `json:"received"`
	Accepted  uint64            `json:"accepted"`
	Dropped   map[string]uint64 `json:"dropped"`
	EPS       float64           `json:"eps"` // accepted per second over the last minute
	Buffered  int               `json:"buffered"`
	LastError string            `json:"last_error"`
	Devices   []DeviceStat      `json:"devices"`
	Rejected  []Rejected        `json:"rejected"`
	Listeners map[string]string `json:"listeners"`
}

// Collector receives syslog, filters and parses it and writes batches to
// ClickHouse.
type Collector struct {
	ch       *chstore.Client
	inv      *inventory.Service
	geo      *GeoIP
	trusted  func() []netip.Prefix
	log      *slog.Logger
	now      func() time.Time
	listened map[string]string

	devMu   sync.RWMutex
	devices map[string]deviceEntry // by serial, HA members included

	bufMu   sync.Mutex
	buf     map[string][]any
	nBuf    int
	kick    chan struct{}
	lastErr string

	received, accepted atomic.Uint64
	statMu             sync.Mutex
	dropped            map[string]uint64
	perDev             map[int64]*DeviceStat
	rejected           map[string]*Rejected
	secs               [60]uint64 // accepted per second, ring
	secAt              int64

	closers []io.Closer
	conns   atomic.Int32
	wg      sync.WaitGroup
}

// NewCollector returns a collector. trusted lists extra source networks
// allowed to send logs besides each device's registered address.
func NewCollector(ch *chstore.Client, inv *inventory.Service, geo *GeoIP, trusted func() []netip.Prefix, log *slog.Logger) *Collector {
	return &Collector{ch: ch, inv: inv, geo: geo, trusted: trusted, log: log, now: time.Now,
		listened: map[string]string{}, devices: map[string]deviceEntry{}, buf: map[string][]any{},
		kick: make(chan struct{}, 1), dropped: map[string]uint64{}, perDev: map[int64]*DeviceStat{},
		rejected: map[string]*Rejected{}}
}

// RefreshDevices reloads the serial → device map.
func (c *Collector) RefreshDevices(ctx context.Context) error {
	list, err := c.inv.List(ctx, inventory.Filter{})
	if err != nil {
		return err
	}
	m := map[string]deviceEntry{}
	for _, d := range list {
		var ips []netip.Addr
		if a, err := netip.ParseAddr(d.Host); err == nil {
			ips = append(ips, a.Unmap())
		} else {
			rctx, cancel := context.WithTimeout(ctx, 3*time.Second)
			addrs, _ := net.DefaultResolver.LookupNetIP(rctx, "ip", d.Host)
			cancel()
			for _, a := range addrs {
				ips = append(ips, a.Unmap())
			}
		}
		if d.Serial != "" {
			m[d.Serial] = deviceEntry{id: d.ID, name: d.Name, operational: d.Operational(), hostIPs: ips}
		}
		for _, p := range d.HAPeers {
			if p.Serial != "" && p.Serial != d.Serial {
				m[p.Serial] = deviceEntry{id: d.ID, name: d.Name, operational: p.License.Operational(), hostIPs: ips}
			}
		}
	}
	c.devMu.Lock()
	c.devices = m
	c.devMu.Unlock()
	return nil
}

// Start opens the listeners and the flush loop; it returns once listening.
func (c *Collector) Start(ctx context.Context, l Listeners) error {
	if err := c.RefreshDevices(ctx); err != nil {
		c.log.Warn("loading devices for the log collector", "err", err)
	}
	if l.UDP != "" {
		pc, err := net.ListenPacket("udp", l.UDP)
		if err != nil {
			return err
		}
		c.closers = append(c.closers, pc)
		c.listened["udp"] = pc.LocalAddr().String()
		c.wg.Add(1)
		go c.serveUDP(pc)
	}
	if l.TCP != "" {
		ln, err := net.Listen("tcp", l.TCP)
		if err != nil {
			c.Close()
			return err
		}
		c.closers = append(c.closers, ln)
		c.listened["tcp"] = ln.Addr().String()
		c.wg.Add(1)
		go c.serveStream(ln)
	}
	if l.TLS != "" {
		ln, err := tls.Listen("tcp", l.TLS, l.TLSConfig)
		if err != nil {
			c.Close()
			return err
		}
		c.closers = append(c.closers, ln)
		c.listened["tls"] = ln.Addr().String()
		c.wg.Add(1)
		go c.serveStream(ln)
	}
	go c.loop(ctx)
	return nil
}

// Addrs returns the bound addresses by protocol.
func (c *Collector) Addrs() map[string]string { return c.listened }

// Close stops the listeners.
func (c *Collector) Close() {
	for _, cl := range c.closers {
		_ = cl.Close()
	}
	c.wg.Wait()
}

func (c *Collector) loop(ctx context.Context) {
	flush := time.NewTicker(flushEvery)
	refresh := time.NewTicker(30 * time.Second)
	defer flush.Stop()
	defer refresh.Stop()
	backoff := time.Time{}
	for {
		select {
		case <-ctx.Done():
			fctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			c.Flush(fctx)
			cancel()
			return
		case <-refresh.C:
			if err := c.RefreshDevices(ctx); err != nil {
				c.log.Warn("refreshing devices for the log collector", "err", err)
			}
		case <-flush.C:
		case <-c.kick:
		}
		if c.now().Before(backoff) {
			continue
		}
		if err := c.Flush(ctx); err != nil {
			backoff = c.now().Add(10 * time.Second)
		}
	}
}

// Flush writes buffered rows; rows stay buffered when the store fails.
func (c *Collector) Flush(ctx context.Context) error {
	c.bufMu.Lock()
	batch := c.buf
	c.buf = map[string][]any{}
	c.nBuf = 0
	c.bufMu.Unlock()
	var firstErr error
	for table, rows := range batch {
		if err := c.ch.Insert(ctx, table, rows); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			c.requeue(table, rows)
		}
	}
	c.bufMu.Lock()
	if firstErr != nil {
		c.lastErr = firstErr.Error()
		c.log.Warn("writing logs to the log store", "err", firstErr)
	} else if len(batch) > 0 {
		c.lastErr = ""
	}
	c.bufMu.Unlock()
	return firstErr
}

func (c *Collector) requeue(table string, rows []any) {
	c.bufMu.Lock()
	defer c.bufMu.Unlock()
	room := maxBuffered - c.nBuf
	if room < len(rows) {
		c.drop(DropBufferFull, uint64(len(rows)-max(room, 0)))
		rows = rows[:max(room, 0)]
	}
	c.buf[table] = append(rows, c.buf[table]...)
	c.nBuf += len(rows)
}

func (c *Collector) serveUDP(pc net.PacketConn) {
	defer c.wg.Done()
	buf := make([]byte, maxMessageBytes)
	for {
		n, addr, err := pc.ReadFrom(buf)
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			continue
		}
		ip := netip.Addr{}
		if ua, ok := addr.(*net.UDPAddr); ok {
			ip, _ = netip.AddrFromSlice(ua.IP)
		}
		c.Handle(buf[:n], ip.Unmap())
	}
}

func (c *Collector) serveStream(ln net.Listener) {
	defer c.wg.Done()
	var conns sync.WaitGroup
	defer conns.Wait()
	for {
		conn, err := ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			continue
		}
		if c.conns.Add(1) > maxTCPConns {
			c.conns.Add(-1)
			conn.Close()
			continue
		}
		conns.Add(1)
		go func() {
			defer conns.Done()
			defer c.conns.Add(-1)
			defer conn.Close()
			ip := netip.Addr{}
			if ta, ok := conn.RemoteAddr().(*net.TCPAddr); ok {
				ip, _ = netip.AddrFromSlice(ta.IP)
			}
			c.readStream(conn, ip.Unmap())
		}()
	}
}

// readStream reads RFC 6587 frames: octet-counted ("123 <msg>") or
// newline-terminated.
func (c *Collector) readStream(conn net.Conn, ip netip.Addr) {
	r := bufio.NewReaderSize(conn, maxMessageBytes)
	for {
		_ = conn.SetReadDeadline(time.Now().Add(tcpIdle))
		b, err := r.Peek(1)
		if err != nil {
			return
		}
		if b[0] >= '0' && b[0] <= '9' {
			lenStr, err := r.ReadString(' ')
			if err != nil || len(lenStr) > 7 {
				return
			}
			n, err := strconv.Atoi(lenStr[:len(lenStr)-1])
			if err != nil || n <= 0 || n > maxMessageBytes {
				return
			}
			msg := make([]byte, n)
			if _, err := io.ReadFull(r, msg); err != nil {
				return
			}
			c.Handle(msg, ip)
			continue
		}
		line, err := r.ReadSlice('\n')
		if len(line) > 0 {
			c.Handle(line, ip)
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			// Overlong line: discard the rest of it.
			for errors.Is(err, bufio.ErrBufferFull) {
				_, err = r.ReadSlice('\n')
			}
		}
		if err != nil {
			return
		}
	}
}

// Handle processes one message from ip.
func (c *Collector) Handle(line []byte, ip netip.Addr) {
	c.received.Add(1)
	now := c.now()
	m, err := Parse(line, now)
	if err != nil {
		c.countDrop(DropUnparsable)
		return
	}
	c.devMu.RLock()
	d, ok := c.devices[m.Serial]
	c.devMu.RUnlock()
	switch {
	case !ok:
		c.reject(m.Serial, ip, DropUnknown, now)
		return
	case !d.operational:
		c.reject(m.Serial, ip, DropUnlicensed, now)
		return
	case !c.trustedSource(d, ip):
		c.reject(m.Serial, ip, DropUntrusted, now)
		return
	}
	table, row := Row(m, d.id)
	c.geo.fill(row)
	c.bufMu.Lock()
	if c.nBuf >= maxBuffered {
		c.bufMu.Unlock()
		c.countDrop(DropBufferFull)
		return
	}
	c.buf[table] = append(c.buf[table], row)
	c.nBuf++
	full := c.nBuf >= flushRows
	c.bufMu.Unlock()
	if full {
		select {
		case c.kick <- struct{}{}:
		default:
		}
	}
	c.accepted.Add(1)
	c.statMu.Lock()
	ds := c.perDev[d.id]
	if ds == nil {
		ds = &DeviceStat{DeviceID: d.id}
		c.perDev[d.id] = ds
	}
	ds.Name, ds.LastAt, ds.SourceIP = d.name, now, ip.String()
	ds.Count++
	c.tick(now)
	c.secs[now.Unix()%60]++
	c.statMu.Unlock()
}

// tick clears ring buckets for seconds that passed without messages.
// statMu must be held.
func (c *Collector) tick(now time.Time) {
	sec := now.Unix()
	if c.secAt == 0 || sec-c.secAt >= 60 {
		c.secs = [60]uint64{}
	} else {
		for s := c.secAt + 1; s <= sec; s++ {
			c.secs[s%60] = 0
		}
	}
	c.secAt = sec
}

func (c *Collector) trustedSource(d deviceEntry, ip netip.Addr) bool {
	if !ip.IsValid() {
		return false
	}
	if slices.Contains(d.hostIPs, ip) {
		return true
	}
	if c.trusted != nil {
		for _, p := range c.trusted() {
			if p.Contains(ip) {
				return true
			}
		}
	}
	return false
}

func (c *Collector) countDrop(reason string) {
	c.statMu.Lock()
	c.dropped[reason]++
	c.statMu.Unlock()
}

// drop counts n dropped messages; bufMu may be held.
func (c *Collector) drop(reason string, n uint64) {
	c.statMu.Lock()
	c.dropped[reason] += n
	c.statMu.Unlock()
}

func (c *Collector) reject(serial string, ip netip.Addr, reason string, now time.Time) {
	c.statMu.Lock()
	defer c.statMu.Unlock()
	c.dropped[reason]++
	key := serial + "|" + ip.String() + "|" + reason
	r := c.rejected[key]
	if r == nil {
		if len(c.rejected) >= 200 {
			return
		}
		r = &Rejected{Serial: serial, IP: ip.String(), Reason: reason}
		c.rejected[key] = r
	}
	r.Count++
	r.LastAt = now
}

// Stats returns a snapshot.
func (c *Collector) Stats() Stats {
	c.bufMu.Lock()
	buffered, lastErr := c.nBuf, c.lastErr
	c.bufMu.Unlock()
	c.statMu.Lock()
	defer c.statMu.Unlock()
	c.tick(c.now())
	var sum uint64
	for _, n := range c.secs {
		sum += n
	}
	st := Stats{Received: c.received.Load(), Accepted: c.accepted.Load(), Dropped: map[string]uint64{},
		EPS: float64(sum) / 60, Buffered: buffered, LastError: lastErr, Listeners: c.listened,
		Devices: []DeviceStat{}, Rejected: []Rejected{}}
	for k, v := range c.dropped {
		st.Dropped[k] = v
	}
	for _, d := range c.perDev {
		st.Devices = append(st.Devices, *d)
	}
	sort.Slice(st.Devices, func(i, j int) bool { return st.Devices[i].Name < st.Devices[j].Name })
	for _, r := range c.rejected {
		st.Rejected = append(st.Rejected, *r)
	}
	sort.Slice(st.Rejected, func(i, j int) bool { return st.Rejected[i].LastAt.After(st.Rejected[j].LastAt) })
	return st
}

// ForgetRejected clears the rejected-sender list (after the administrator
// has acted on it).
func (c *Collector) ForgetRejected() {
	c.statMu.Lock()
	c.rejected = map[string]*Rejected{}
	c.statMu.Unlock()
}
