// Package events fans out live events to the panel's Server-Sent Events
// stream. TaktaPlus runs as one api process, so an in-memory broker is
// enough; Default is the broker the services publish to.
package events

import (
	"sync"
)

// Event types.
const (
	Alert   = "alert"   // an alert opened, changed, was acknowledged or resolved
	Change  = "change"  // a change request or one of its devices moved on
	Device  = "device"  // a device's status after a poll
	Backup  = "backup"  // a device backup or a product backup finished
	Report  = "report"  // a report run finished
	Upgrade = "upgrade" // a product upgrade moved to another step
	Resync  = "resync"  // the subscriber fell behind: reload everything
)

// Event is one message; Perm is the permission a subscriber needs to see it
// (empty: every signed-in user).
type Event struct {
	Type string `json:"type"`
	Data any    `json:"data"`
	Perm string `json:"-"`
}

// Broker delivers events to subscribers.
type Broker struct {
	mu   sync.Mutex
	subs map[*sub]struct{}
}

type sub struct {
	ch     chan Event
	can    func(perm string) bool
	behind bool
}

// queue is how many events a slow subscriber may lag before it gets a resync.
const queue = 64

// New returns an empty broker.
func New() *Broker { return &Broker{subs: map[*sub]struct{}{}} }

// Default is the process-wide broker.
var Default = New()

// Publish sends e to Default.
func Publish(typ, perm string, data any) { Default.Publish(Event{Type: typ, Perm: perm, Data: data}) }

// Publish delivers e to every subscriber allowed to see it, without
// blocking: a subscriber whose queue is full gets one resync instead.
func (b *Broker) Publish(e Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for s := range b.subs {
		if e.Perm != "" && !s.can(e.Perm) {
			continue
		}
		if s.behind {
			continue
		}
		select {
		case s.ch <- e:
		default:
			s.behind = true
		}
	}
}

// Subscribe registers a subscriber; can reports whether it holds a
// permission. The returned function unsubscribes.
func (b *Broker) Subscribe(can func(perm string) bool) (<-chan Event, func() bool, func()) {
	s := &sub{ch: make(chan Event, queue), can: can}
	b.mu.Lock()
	b.subs[s] = struct{}{}
	b.mu.Unlock()
	// behind reports and clears the lag flag once the queue has drained.
	behind := func() bool {
		b.mu.Lock()
		defer b.mu.Unlock()
		if s.behind && len(s.ch) == 0 {
			s.behind = false
			return true
		}
		return false
	}
	return s.ch, behind, func() {
		b.mu.Lock()
		delete(b.subs, s)
		b.mu.Unlock()
	}
}

// Subscribers returns the number of open streams (for tests and metrics).
func (b *Broker) Subscribers() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.subs)
}
