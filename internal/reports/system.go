package reports

import (
	"context"
	"slices"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/farshadfighter/taktaplus-lic/pkg/licformat"

	"github.com/farshadfighter/taktaplus/internal/inventory"
	"github.com/farshadfighter/taktaplus/internal/license"
	"github.com/farshadfighter/taktaplus/internal/logs"
)

// RegisterSystemWidgets adds report widgets about the managed estate rather
// than its logs: device status, alerts, backups and subscriptions. They
// make up the management summary together with log widgets.
func RegisterSystemWidgets(db *pgxpool.Pool, inv *inventory.Service, guard *license.Guard) {
	scoped := func(ids []int64, id int64) bool { return len(ids) == 0 || slices.Contains(ids, id) }
	col := func(key, title, typ string) logs.Column { return logs.Column{Key: key, Title: title, Type: typ} }

	logs.RegisterWidget(logs.Widget{ID: "sys_devices", Title: "وضعیت دستگاه‌ها", Kind: logs.KindKPIs, Group: "system", Snapshot: true,
		Columns: []logs.Column{col("total", "دستگاه‌ها", "number"), col("online", "آنلاین", "number"), col("offline", "قطع ارتباط", "number"), col("unlicensed", "بدون لایسنس فعال", "number")}},
		func(ctx context.Context, r logs.Range) ([]map[string]any, error) {
			list, err := inv.List(ctx, inventory.Filter{})
			if err != nil {
				return nil, err
			}
			var total, online, offline, unlicensed int
			for _, d := range list {
				if !scoped(r.DeviceIDs, d.ID) {
					continue
				}
				total++
				switch d.Status {
				case "online":
					online++
				case "offline", "error":
					offline++
				}
				if !d.Operational() {
					unlicensed++
				}
			}
			return []map[string]any{{"total": total, "online": online, "offline": offline, "unlicensed": unlicensed}}, nil
		})

	logs.RegisterWidget(logs.Widget{ID: "sys_alerts", Title: "هشدارها", Kind: logs.KindKPIs, Group: "system",
		Columns: []logs.Column{col("opened", "هشدارهای دوره", "number"), col("critical", "بحرانی", "number"), col("open", "باز در پایان دوره", "number")}},
		func(ctx context.Context, r logs.Range) ([]map[string]any, error) {
			var opened, critical, open int
			err := db.QueryRow(ctx, `SELECT
					count(*) FILTER (WHERE opened_at >= $1 AND opened_at < $2),
					count(*) FILTER (WHERE opened_at >= $1 AND opened_at < $2 AND severity = 'critical'),
					count(*) FILTER (WHERE opened_at < $2 AND (resolved_at IS NULL OR resolved_at >= $2))
				FROM alerts WHERE (cardinality($3::bigint[]) = 0 OR device_id = ANY($3))`, r.From, r.To, nonNil(r.DeviceIDs)).Scan(&opened, &critical, &open)
			return []map[string]any{{"opened": opened, "critical": critical, "open": open}}, err
		})

	logs.RegisterWidget(logs.Widget{ID: "sys_backups", Title: "بکاپ‌ها", Kind: logs.KindKPIs, Group: "system",
		Columns: []logs.Column{col("ok", "بکاپ موفق", "number"), col("failed", "بکاپ ناموفق", "number"), col("missing", "دستگاه بدون بکاپ موفق در دوره", "number")}},
		func(ctx context.Context, r logs.Range) ([]map[string]any, error) {
			var ok, failed, missing int
			err := db.QueryRow(ctx, `SELECT
					(SELECT count(*) FROM backups b WHERE b.at >= $1 AND b.at < $2 AND b.status = 'ok' AND (cardinality($3::bigint[]) = 0 OR b.device_id = ANY($3))),
					(SELECT count(*) FROM backups b WHERE b.at >= $1 AND b.at < $2 AND b.status = 'failed' AND (cardinality($3::bigint[]) = 0 OR b.device_id = ANY($3))),
					(SELECT count(*) FROM devices d WHERE (cardinality($3::bigint[]) = 0 OR d.id = ANY($3)) AND d.created_at < $2
					   AND NOT EXISTS (SELECT 1 FROM backups b WHERE b.device_id = d.id AND b.status = 'ok' AND b.at >= $1 AND b.at < $2))`,
				r.From, r.To, nonNil(r.DeviceIDs)).Scan(&ok, &failed, &missing)
			return []map[string]any{{"ok": ok, "failed": failed, "missing": missing}}, err
		})

	logs.RegisterWidget(logs.Widget{ID: "sys_expiring", Title: "اشتراک‌های رو به انقضا (۶۰ روز)", Kind: logs.KindTable, Group: "system",
		Columns: []logs.Column{col("device", "دستگاه", "text"), col("serial", "سریال", "text"), col("expires", "انقضا", "date"), col("state", "وضعیت", "text")}},
		func(ctx context.Context, r logs.Range) ([]map[string]any, error) {
			lic, err := guard.License()
			if err != nil {
				return []map[string]any{}, nil
			}
			list, err := inv.List(ctx, inventory.Filter{})
			if err != nil {
				return nil, err
			}
			names := map[string]string{}
			for _, d := range list {
				if scoped(r.DeviceIDs, d.ID) {
					names[d.Serial] = d.Name
				}
			}
			now := guard.Now()
			out := []map[string]any{}
			for _, it := range lic.Devices {
				name, ok := names[it.Serial]
				end := it.Expires.EndOfDay()
				if !ok || end.After(now.AddDate(0, 0, 60)) {
					continue
				}
				state := map[licformat.Status]string{licformat.StatusActive: "فعال", licformat.StatusGrace: "در مهلت", licformat.StatusExpired: "منقضی"}[lic.DeviceStatus(it.Serial, now)]
				out = append(out, map[string]any{"device": name, "serial": it.Serial, "expires": end.Format(time.DateOnly), "state": state})
			}
			slices.SortFunc(out, func(a, b map[string]any) int { return compareStr(a["expires"].(string), b["expires"].(string)) })
			return out, nil
		})
}

func nonNil(ids []int64) []int64 {
	if ids == nil {
		return []int64{}
	}
	return ids
}

func compareStr(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}
