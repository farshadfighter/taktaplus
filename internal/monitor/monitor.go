// Package monitor polls licensed devices for health and signature versions
// and drives alert evaluation.
package monitor

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/farshadfighter/taktaplus/internal/alerts"
	"github.com/farshadfighter/taktaplus/internal/inventory"
)

// Options tune the poller. Zero values take the defaults.
type Options struct {
	HealthEvery   time.Duration // default 60s
	ProbeEvery    time.Duration // re-probe and refresh signatures, default 6h
	DiscoverEvery time.Duration // retry devices whose serial is unknown, default 10m
	Concurrency   int           // default 8
	MetricsKeep   time.Duration // default 30 days
	PollTimeout   time.Duration // per device and operation, default 45s
}

// Monitor is the background poller.
type Monitor struct {
	inv    *inventory.Service
	alerts *alerts.Service
	log    *slog.Logger
	opt    Options

	mu         sync.Mutex
	lastHealth map[int64]time.Time
	lastProbe  map[int64]time.Time
	running    map[int64]bool
}

// New returns a monitor.
func New(inv *inventory.Service, al *alerts.Service, log *slog.Logger, opt Options) *Monitor {
	def := func(v *time.Duration, d time.Duration) {
		if *v == 0 {
			*v = d
		}
	}
	def(&opt.HealthEvery, time.Minute)
	def(&opt.ProbeEvery, 6*time.Hour)
	def(&opt.DiscoverEvery, 10*time.Minute)
	def(&opt.MetricsKeep, 30*24*time.Hour)
	def(&opt.PollTimeout, 45*time.Second)
	if opt.Concurrency <= 0 {
		opt.Concurrency = 8
	}
	return &Monitor{inv: inv, alerts: al, log: log, opt: opt,
		lastHealth: map[int64]time.Time{}, lastProbe: map[int64]time.Time{}, running: map[int64]bool{}}
}

// Run polls until ctx ends.
func (m *Monitor) Run(ctx context.Context) {
	tick := time.NewTicker(10 * time.Second)
	defer tick.Stop()
	var lastEval, lastPrune time.Time
	sem := make(chan struct{}, m.opt.Concurrency)
	var wg sync.WaitGroup
	defer wg.Wait()
	for {
		m.schedule(ctx, sem, &wg)
		if time.Since(lastEval) >= time.Minute {
			lastEval = time.Now()
			if err := m.alerts.Evaluate(ctx); err != nil && ctx.Err() == nil {
				m.log.Error("evaluate alerts", "err", err)
			}
		}
		if time.Since(lastPrune) >= 24*time.Hour {
			lastPrune = time.Now()
			if err := m.inv.PruneMetrics(ctx, m.opt.MetricsKeep); err != nil && ctx.Err() == nil {
				m.log.Error("prune metrics", "err", err)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// schedule starts a poll for every device that is due.
func (m *Monitor) schedule(ctx context.Context, sem chan struct{}, wg *sync.WaitGroup) {
	devices, err := m.inv.List(ctx, inventory.Filter{})
	if err != nil {
		if ctx.Err() == nil {
			m.log.Error("list devices", "err", err)
		}
		return
	}
	now := time.Now()
	for _, d := range devices {
		m.mu.Lock()
		busy := m.running[d.ID]
		sinceProbe := now.Sub(m.lastProbe[d.ID])
		probeDue := sinceProbe >= m.opt.ProbeEvery
		healthDue := now.Sub(m.lastHealth[d.ID]) >= m.opt.HealthEvery
		m.mu.Unlock()
		switch {
		case busy:
			continue
		case d.Serial == "":
			// Never probed successfully: retry discovery now and then.
			if sinceProbe < m.opt.DiscoverEvery {
				continue
			}
			healthDue = false
			probeDue = true
		case !d.Operational():
			// Expired or not in the license: no polling.
			continue
		case !probeDue && !healthDue:
			continue
		}
		m.mu.Lock()
		m.running[d.ID] = true
		if probeDue {
			m.lastProbe[d.ID] = now
		}
		if healthDue || probeDue {
			m.lastHealth[d.ID] = now
		}
		m.mu.Unlock()
		wg.Add(1)
		go func(d *inventory.Device, probe bool) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-sem }()
			defer func() {
				m.mu.Lock()
				delete(m.running, d.ID)
				m.mu.Unlock()
			}()
			m.poll(ctx, d, probe)
		}(d, probeDue)
	}
}

// poll gathers health (and with probe, discovery and signatures) for one
// device.
func (m *Monitor) poll(ctx context.Context, d *inventory.Device, probe bool) {
	if probe {
		pctx, cancel := context.WithTimeout(ctx, m.opt.PollTimeout)
		nd, err := m.inv.Probe(pctx, d.ID)
		cancel()
		if err != nil {
			m.log.Info("probe failed", "device", d.Name, "err", err)
			return
		}
		d = nd
		if !d.Operational() {
			return
		}
		if err := m.refreshSignatures(ctx, d); err != nil {
			m.log.Info("signatures failed", "device", d.Name, "err", err)
		}
	}
	if err := m.refreshHealth(ctx, d); err != nil {
		m.log.Info("health poll failed", "device", d.Name, "err", err)
	}
}

func (m *Monitor) refreshHealth(ctx context.Context, d *inventory.Device) error {
	ctx, cancel := context.WithTimeout(ctx, m.opt.PollTimeout)
	defer cancel()
	h, err := m.inv.Driver(d).Health(ctx)
	if err != nil {
		if ctx.Err() == nil || ctx.Err() == context.DeadlineExceeded {
			_ = m.inv.MarkUnreachable(context.WithoutCancel(ctx), d.ID, err)
		}
		return err
	}
	return m.inv.SaveHealth(ctx, d.ID, h)
}

func (m *Monitor) refreshSignatures(ctx context.Context, d *inventory.Device) error {
	ctx, cancel := context.WithTimeout(ctx, m.opt.PollTimeout)
	defer cancel()
	sigs, contracts, err := m.inv.Driver(d).Signatures(ctx)
	if err != nil {
		return err
	}
	return m.inv.SaveSignatures(ctx, d.ID, sigs, contracts)
}

// Refresh polls one device now (probe, signatures and health), for the
// panel's refresh button.
func (m *Monitor) Refresh(ctx context.Context, id int64) error {
	d, err := m.inv.Probe(ctx, id)
	if err != nil {
		return err
	}
	m.mu.Lock()
	m.lastProbe[id], m.lastHealth[id] = time.Now(), time.Now()
	m.mu.Unlock()
	if !d.Operational() {
		return nil
	}
	if err := m.refreshSignatures(ctx, d); err != nil {
		m.log.Info("signatures failed", "device", d.Name, "err", err)
	}
	return m.refreshHealth(ctx, d)
}
