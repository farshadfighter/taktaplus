// Package devicetest provides an in-memory device driver for tests.
package devicetest

import (
	"context"
	"errors"
	"sync"

	"github.com/farshadfighter/taktaplus/internal/device"
)

// ErrDown is returned by a Fake marked Down.
var ErrDown = errors.New("connection refused")

// Fake is a scripted device.
type Fake struct {
	mu        sync.Mutex
	Info      device.Info
	HealthV   device.Health
	Sigs      []device.Signature
	Contracts []device.Contract
	Config    []byte
	Down      bool
	Restored  [][]byte
	Calls     map[string]int
}

func (f *Fake) call(name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Calls == nil {
		f.Calls = map[string]int{}
	}
	f.Calls[name]++
	if f.Down {
		return ErrDown
	}
	return nil
}

// Set changes the fake under its lock.
func (f *Fake) Set(fn func(f *Fake)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn(f)
}

func (f *Fake) Probe(context.Context) (device.Info, error) {
	if err := f.call("probe"); err != nil {
		return device.Info{}, err
	}
	return f.Info, nil
}

func (f *Fake) Health(context.Context) (device.Health, error) {
	if err := f.call("health"); err != nil {
		return device.Health{}, err
	}
	return f.HealthV, nil
}

func (f *Fake) Signatures(context.Context) ([]device.Signature, []device.Contract, error) {
	if err := f.call("signatures"); err != nil {
		return nil, nil, err
	}
	return f.Sigs, f.Contracts, nil
}

func (f *Fake) BackupConfig(context.Context) ([]byte, error) {
	if err := f.call("backup"); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]byte(nil), f.Config...), nil
}

func (f *Fake) RestoreConfig(_ context.Context, cfg []byte) error {
	if err := f.call("restore"); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Restored = append(f.Restored, append([]byte(nil), cfg...))
	f.Config = append([]byte(nil), cfg...)
	return nil
}

// Fleet maps host names to fakes; Factory plugs it into the inventory.
type Fleet map[string]*Fake

// Factory returns a driver factory serving the fleet's fakes by host.
// Unknown hosts are unreachable.
func (fl Fleet) Factory() func(device.Config) device.Driver {
	return func(c device.Config) device.Driver {
		if f, ok := fl[c.Host]; ok {
			return f
		}
		return &Fake{Down: true}
	}
}
