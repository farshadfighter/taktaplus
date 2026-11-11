package logs

import (
	"context"
	"time"

	"github.com/farshadfighter/taktaplus/internal/alerts"
	"github.com/farshadfighter/taktaplus/internal/chstore"
)

// LastSeen returns, per device, the time of its newest log in the last
// week. Devices that sent nothing in that week are absent.
func (s *Service) LastSeen(ctx context.Context) (map[int64]time.Time, error) {
	if s.ch == nil {
		return nil, ErrDisabled
	}
	q := `SELECT device_id, toString(max(last)) AS last FROM (`
	for i, t := range chstore.RawTables {
		if i > 0 {
			q += " UNION ALL "
		}
		q += `SELECT device_id, max(ts) AS last FROM ` + t + ` WHERE ts > now64() - INTERVAL 7 DAY GROUP BY device_id`
	}
	q += `) GROUP BY device_id`
	type row struct {
		DeviceID int64  `json:"device_id"`
		Last     string `json:"last"`
	}
	rows, err := chstore.Select[row](ctx, s.ch, q, nil)
	if err != nil {
		return nil, err
	}
	out := make(map[int64]time.Time, len(rows))
	for _, r := range rows {
		if t, err := time.Parse("2006-01-02 15:04:05.000", r.Last); err == nil {
			out[r.DeviceID] = t.UTC()
		}
	}
	return out, nil
}

// Hits returns the sources that reached min events of a log alert kind
// within the window, per device (at most 50 per device).
func (s *Service) Hits(ctx context.Context, kind string, window time.Duration, min int) (map[int64][]alerts.LogHit, error) {
	if s.ch == nil {
		return nil, ErrDisabled
	}
	since := "ts >= now64() - toIntervalSecond({w:UInt32})"
	var q string
	switch kind {
	case alerts.KindLogAttacks:
		q = `SELECT device_id, subject, sum(c) AS n, argMax(detail, c) AS detail FROM (
			SELECT device_id, srcip AS subject, count() AS c, topK(1)(attack)[1] AS detail FROM log_waf
			WHERE ` + since + ` AND srcip != '' GROUP BY device_id, srcip
			UNION ALL
			SELECT device_id, srcip, count(), topK(1)(threat)[1] FROM log_threat
			WHERE ` + since + ` AND subtype IN ('ips', 'anomaly') AND srcip != '' GROUP BY device_id, srcip)
			GROUP BY device_id, subject HAVING n >= {min:UInt32} ORDER BY n DESC LIMIT 50 BY device_id`
	case alerts.KindVPNFailures:
		q = `SELECT device_id, concat(if(username = '', '?', username), ' @ ', srcip) AS subject, count() AS n, topK(1)(logdesc)[1] AS detail
			FROM log_event WHERE ` + since + ` AND subtype = 'vpn' AND positionCaseInsensitive(action, 'fail') > 0
			GROUP BY device_id, subject HAVING n >= {min:UInt32} ORDER BY n DESC LIMIT 50 BY device_id`
	default:
		return nil, nil
	}
	type row struct {
		DeviceID int64  `json:"device_id"`
		Subject  string `json:"subject"`
		N        uint64 `json:"n"`
		Detail   string `json:"detail"`
	}
	rows, err := chstore.Select[row](ctx, s.ch, q, chstore.Params{"w": int(window.Seconds()), "min": min})
	if err != nil {
		return nil, err
	}
	out := map[int64][]alerts.LogHit{}
	for _, r := range rows {
		out[r.DeviceID] = append(out[r.DeviceID], alerts.LogHit{Subject: r.Subject, Count: r.N, Detail: r.Detail})
	}
	return out, nil
}
