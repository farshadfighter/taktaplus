package compliance

import (
	"context"
	"slices"

	"github.com/farshadfighter/taktaplus/internal/logs"
)

// RegisterWidgets adds compliance widgets for dashboards and report
// templates. They show the latest evaluation, not the report period.
func (s *Service) RegisterWidgets() {
	col := func(key, title, typ string) logs.Column { return logs.Column{Key: key, Title: title, Type: typ} }
	scoped := func(r logs.Range, id int64) bool { return len(r.DeviceIDs) == 0 || slices.Contains(r.DeviceIDs, id) }

	logs.RegisterWidget(logs.Widget{ID: "sys_compliance_kpi", Title: "انطباق پیکربندی", Kind: logs.KindKPIs, Group: "system", Snapshot: true,
		Columns: []logs.Column{col("score", "میانگین امتیاز انطباق", "number"), col("drifting", "دستگاه‌های دارای انحراف", "number"), col("broken", "قوانین نقض‌شده", "number")}},
		func(ctx context.Context, r logs.Range) ([]map[string]any, error) {
			list, err := s.Results(ctx)
			if err != nil {
				return nil, err
			}
			total, n, drifting := 0, 0, 0
			broken := map[int64]bool{}
			for _, res := range list {
				if res.Error != "" || !scoped(r, res.DeviceID) {
					continue
				}
				total += res.Score
				n++
				if d, _, _ := res.Counts(); d > 0 {
					drifting++
				}
				for _, v := range res.Violations {
					if !v.Excepted {
						broken[v.RuleID] = true
					}
				}
			}
			avg := 0
			if n > 0 {
				avg = (total + n/2) / n
			}
			return []map[string]any{{"score": avg, "drifting": drifting, "broken": len(broken)}}, nil
		})

	logs.RegisterWidget(logs.Widget{ID: "sys_compliance", Title: "امتیاز انطباق دستگاه‌ها", Kind: logs.KindTable, Group: "system", Snapshot: true,
		Columns: []logs.Column{col("device", "دستگاه", "text"), col("score", "امتیاز", "number"), col("drift", "قالب‌های دارای انحراف", "number"), col("rules", "قوانین نقض‌شده", "number")}},
		func(ctx context.Context, r logs.Range) ([]map[string]any, error) {
			list, err := s.Results(ctx)
			if err != nil {
				return nil, err
			}
			out := []map[string]any{}
			for _, res := range list {
				if res.Error != "" || !scoped(r, res.DeviceID) {
					continue
				}
				d, _, b := res.Counts()
				out = append(out, map[string]any{"device": res.DeviceName, "score": res.Score, "drift": d, "rules": b})
			}
			return out, nil
		})
}
