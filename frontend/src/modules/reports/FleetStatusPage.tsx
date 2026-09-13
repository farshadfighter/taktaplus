import { useEffect, useState } from "react";
import { Link } from "react-router-dom";

import { apiClient } from "@/services/apiClient";
import { FleetStatusRow } from "@/modules/reports/types";

const VENDOR_LABELS: Record<FleetStatusRow["vendor_type"], string> = {
  fortigate: "FortiGate",
  fortiweb: "FortiWeb",
};

export function FleetStatusPage() {
  const [rows, setRows] = useState<FleetStatusRow[]>([]);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    apiClient
      .get<FleetStatusRow[]>("/reports/fleet-status")
      .then((res) => setRows(res.data))
      .finally(() => setLoading(false));
  }, []);

  return (
    <div>
      <h2 style={{ marginTop: 0 }}>گزارش وضعیت فلیت</h2>
      <div className="card">
        {loading ? (
          <p>در حال بارگذاری...</p>
        ) : rows.length === 0 ? (
          <p>هنوز دستگاهی اضافه نشده است.</p>
        ) : (
          <table style={{ width: "100%", borderCollapse: "collapse" }}>
            <thead>
              <tr style={{ textAlign: "right", borderBottom: "1px solid var(--border)" }}>
                <th style={{ padding: 8 }}>دستگاه</th>
                <th style={{ padding: 8 }}>نوع</th>
                <th style={{ padding: 8 }}>وضعیت اتصال</th>
                <th style={{ padding: 8 }}>آخرین بکاپ</th>
                <th style={{ padding: 8 }}>CPU / حافظه</th>
                <th style={{ padding: 8 }}>هشدارهای باز</th>
              </tr>
            </thead>
            <tbody>
              {rows.map((r) => (
                <tr key={r.device_id} style={{ borderBottom: "1px solid var(--border)" }}>
                  <td style={{ padding: 8 }}>
                    <Link to={`/devices/${r.device_id}/backups`}>{r.device_name}</Link>
                  </td>
                  <td style={{ padding: 8 }}>{VENDOR_LABELS[r.vendor_type]}</td>
                  <td style={{ padding: 8, color: r.status === "online" ? "var(--success)" : "var(--danger)" }}>
                    {r.status}
                  </td>
                  <td style={{ padding: 8, color: r.backup_overdue ? "var(--danger)" : "var(--success)" }}>
                    {r.last_backup_at ? new Date(r.last_backup_at).toLocaleString("fa-IR") : "هرگز"}
                    {r.backup_overdue ? " (عقب‌افتاده)" : ""}
                  </td>
                  <td style={{ padding: 8 }}>
                    {r.snmp_enabled
                      ? `${r.latest_cpu_percent?.toFixed(0) ?? "-"}% / ${r.latest_memory_percent?.toFixed(0) ?? "-"}%`
                      : "SNMP غیرفعال"}
                  </td>
                  <td style={{ padding: 8, color: r.open_alert_count > 0 ? "var(--danger)" : "var(--text-muted)" }}>
                    {r.open_alert_count}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
    </div>
  );
}
