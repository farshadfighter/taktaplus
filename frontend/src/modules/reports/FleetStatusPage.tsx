import { BarChart3 } from "lucide-react";
import { useEffect, useState } from "react";
import { Link } from "react-router-dom";

import { Badge } from "@/components/ui/Badge";
import { EmptyState } from "@/components/ui/EmptyState";
import { LoadingState } from "@/components/ui/LoadingState";
import { PageHeader } from "@/components/ui/PageHeader";
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
      <PageHeader title="گزارش وضعیت فلیت" subtitle="نمای یکجا از اتصال، بکاپ، متریک‌ها و هشدارهای باز همه دستگاه‌ها." />
      <div className="card">
        {loading ? (
          <LoadingState />
        ) : rows.length === 0 ? (
          <EmptyState icon={<BarChart3 size={32} />} title="هنوز دستگاهی اضافه نشده است" />
        ) : (
          <div className="table-wrap">
            <table className="data-table">
              <thead>
                <tr>
                  <th>دستگاه</th>
                  <th>نوع</th>
                  <th>وضعیت اتصال</th>
                  <th>آخرین بکاپ</th>
                  <th>CPU / حافظه</th>
                  <th>هشدارهای باز</th>
                </tr>
              </thead>
              <tbody>
                {rows.map((r) => (
                  <tr key={r.device_id}>
                    <td className="cell-primary">
                      <Link to={`/devices/${r.device_id}/backups`}>{r.device_name}</Link>
                    </td>
                    <td className="cell-muted">{VENDOR_LABELS[r.vendor_type]}</td>
                    <td>
                      <Badge variant={r.status === "online" ? "success" : "danger"}>{r.status}</Badge>
                    </td>
                    <td>
                      {r.last_backup_at ? new Date(r.last_backup_at).toLocaleString("fa-IR") : "هرگز"}
                      {r.backup_overdue && (
                        <div style={{ marginTop: 4 }}>
                          <Badge variant="warning">عقب‌افتاده</Badge>
                        </div>
                      )}
                    </td>
                    <td className="cell-muted">
                      {r.snmp_enabled
                        ? `${r.latest_cpu_percent?.toFixed(0) ?? "-"}% / ${r.latest_memory_percent?.toFixed(0) ?? "-"}%`
                        : "SNMP غیرفعال"}
                    </td>
                    <td>
                      {r.open_alert_count > 0 ? (
                        <Badge variant="danger">{r.open_alert_count}</Badge>
                      ) : (
                        <span className="cell-muted">0</span>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>
    </div>
  );
}
