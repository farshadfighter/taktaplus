package logs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"strings"
	"sync/atomic"

	"github.com/farshadfighter/taktaplus/internal/chstore"
	"github.com/farshadfighter/taktaplus/internal/inventory"
	"github.com/farshadfighter/taktaplus/internal/settings"
)

// ErrDisabled means no log store is configured.
var ErrDisabled = errors.New("log storage is not configured (TAKTAPLUS_CLICKHOUSE_URL)")

// InputError is a validation failure to show to the user.
type InputError struct{ msg string }

func (e InputError) Error() string { return e.msg }

func invalidf(format string, a ...any) error { return InputError{fmt.Sprintf(format, a...)} }

const settingsKey = "logs"

// Settings are the panel's log settings.
type Settings struct {
	Retention chstore.Retention `json:"retention"`
	// TrustedSources are extra addresses or networks (CIDR) allowed to send
	// logs, for devices that log from an interface other than the one
	// registered in the panel.
	TrustedSources []string `json:"trusted_sources"`
}

// Service is the log module: settings, collector and queries.
type Service struct {
	ch      *chstore.Client // nil when disabled
	st      *settings.Store
	inv     *inventory.Service
	geo     *GeoIP
	log     *slog.Logger
	trusted atomic.Pointer[[]netip.Prefix]
	col     *Collector
}

// New returns the service; ch may be nil when ClickHouse is not configured.
func New(ch *chstore.Client, st *settings.Store, inv *inventory.Service, geo *GeoIP, log *slog.Logger) *Service {
	s := &Service{ch: ch, st: st, inv: inv, geo: geo, log: log}
	s.trusted.Store(&[]netip.Prefix{})
	if ch != nil {
		s.col = NewCollector(ch, inv, geo, func() []netip.Prefix { return *s.trusted.Load() }, log)
	}
	return s
}

// Enabled reports whether a log store is configured.
func (s *Service) Enabled() bool { return s.ch != nil }

// Collector returns the syslog collector (nil when disabled).
func (s *Service) Collector() *Collector { return s.col }

// GeoIP returns the country database.
func (s *Service) GeoIP() *GeoIP { return s.geo }

// Store returns the ClickHouse client (nil when disabled).
func (s *Service) Store() *chstore.Client { return s.ch }

// Settings returns the stored settings with defaults.
func (s *Service) Settings(ctx context.Context) (Settings, error) {
	st := Settings{Retention: chstore.DefaultRetention, TrustedSources: []string{}}
	if err := s.st.Get(ctx, settingsKey, &st); err != nil {
		return st, err
	}
	if st.TrustedSources == nil {
		st.TrustedSources = []string{}
	}
	return st, nil
}

// Load applies the stored settings at start.
func (s *Service) Load(ctx context.Context) error {
	st, err := s.Settings(ctx)
	if err != nil {
		return err
	}
	prefixes, _ := parseSources(st.TrustedSources)
	s.trusted.Store(&prefixes)
	if s.ch != nil {
		return s.ch.SetRetention(ctx, st.Retention)
	}
	return nil
}

func parseSources(list []string) ([]netip.Prefix, error) {
	out := []netip.Prefix{}
	for _, v := range list {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if p, err := netip.ParsePrefix(v); err == nil {
			out = append(out, p.Masked())
			continue
		}
		a, err := netip.ParseAddr(v)
		if err != nil {
			return nil, invalidf("%q is not an IP address or network", v)
		}
		out = append(out, netip.PrefixFrom(a.Unmap(), a.Unmap().BitLen()))
	}
	return out, nil
}

// SaveSettings validates, applies and stores the settings.
func (s *Service) SaveSettings(ctx context.Context, st Settings) error {
	if err := st.Retention.Validate(); err != nil {
		return InputError{err.Error()}
	}
	prefixes, err := parseSources(st.TrustedSources)
	if err != nil {
		return err
	}
	st.TrustedSources = make([]string, len(prefixes))
	for i, p := range prefixes {
		st.TrustedSources[i] = p.String()
	}
	if s.ch != nil {
		if err := s.ch.SetRetention(ctx, st.Retention); err != nil {
			return err
		}
	}
	if err := s.st.Set(ctx, settingsKey, st); err != nil {
		return err
	}
	s.trusted.Store(&prefixes)
	if s.col != nil {
		s.col.ForgetRejected()
	}
	return nil
}

// Usage reports disk use per table.
func (s *Service) Usage(ctx context.Context) ([]chstore.TableUsage, error) {
	if s.ch == nil {
		return nil, ErrDisabled
	}
	return s.ch.Usage(ctx)
}

// DeviceNames maps device IDs to names for display.
func (s *Service) DeviceNames(ctx context.Context) (map[int64]string, error) {
	list, err := s.inv.List(ctx, inventory.Filter{})
	if err != nil {
		return nil, err
	}
	m := make(map[int64]string, len(list))
	for _, d := range list {
		m[d.ID] = d.Name
	}
	return m, nil
}
