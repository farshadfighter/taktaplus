import {
  AlertTriangle,
  ArrowLeft,
  BarChart3,
  BellRing,
  CheckCircle2,
  KeyRound,
  MessageSquareText,
  PackageOpen,
  Server,
  ShieldCheck,
} from "lucide-react";
import { useEffect, useState } from "react";
import { Link } from "react-router-dom";

import { EmptyState } from "@/components/ui/EmptyState";
import { LoadingState } from "@/components/ui/LoadingState";
import { PageHeader } from "@/components/ui/PageHeader";
import { StatCard } from "@/components/ui/StatCard";
import { apiClient } from "@/services/apiClient";
import { LicenseStatus } from "@/modules/license/types";
import { FleetStatusRow } from "@/modules/reports/types";

const QUICK_LINKS = [
  { to: "/devices", label: "افزودن دستگاه", desc: "FortiGate یا FortiWeb جدید", icon: Server },
  { to: "/packages", label: "سیگنیچر و فرم‌ور", desc: "مدیریت بسته‌های آفلاین", icon: PackageOpen },
  { to: "/radius/users", label: "احراز هویت پیامکی", desc: "کاربران دومرحله‌ای", icon: KeyRound },
  { to: "/reports", label: "گزارش وضعیت فلیت", desc: "نمای کامل همه دستگاه‌ها", icon: BarChart3 },
];

export function DashboardPage() {
  const [rows, setRows] = useState<FleetStatusRow[]>([]);
  const [license, setLicense] = useState<LicenseStatus | null>(null);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    Promise.all([
      apiClient.get<FleetStatusRow[]>("/reports/fleet-status"),
      apiClient.get<LicenseStatus>("/license/status").catch(() => null),
    ])
      .then(([fleetRes, licenseRes]) => {
        setRows(fleetRes.data);
        if (licenseRes) setLicense(licenseRes.data);
      })
      .finally(() => setLoading(false));
  }, []);

  const total = rows.length;
  const online = rows.filter((r) => r.status === "online").length;
  const overdueBackups = rows.filter((r) => r.backup_overdue).length;
  const openAlerts = rows.reduce((sum, r) => sum + r.open_alert_count, 0);
  const attentionRows = rows
    .filter((r) => r.backup_overdue || r.open_alert_count > 0 || r.status !== "online")
    .slice(0, 6);

  return (
    <div>
      <PageHeader
        title="داشبورد"
        subtitle="نمای کلی از وضعیت فلیت FortiGate/FortiWeb، بکاپ‌ها، هشدارها و لایسنس."
      />

      {loading ? (
        <div className="card">
          <LoadingState />
        </div>
      ) : (
        <>
          <div className="stat-grid">
            <StatCard icon={<Server size={18} />} value={total} label="کل دستگاه‌ها" tone="primary" />
            <StatCard icon={<CheckCircle2 size={18} />} value={online} label="دستگاه‌های آنلاین" tone="success" />
            <StatCard
              icon={<AlertTriangle size={18} />}
              value={overdueBackups}
              label="بکاپ‌های عقب‌افتاده"
              tone={overdueBackups > 0 ? "warning" : "success"}
            />
            <StatCard
              icon={<BellRing size={18} />}
              value={openAlerts}
              label="هشدارهای باز"
              tone={openAlerts > 0 ? "danger" : "success"}
            />
            <StatCard
              icon={<ShieldCheck size={18} />}
              value={
                license?.status === "active"
                  ? "فعال"
                  : license?.status === "grace"
                    ? "مهلت"
                    : license?.status === "unactivated"
                      ? "غیرفعال"
                      : license?.status ?? "-"
              }
              label="وضعیت لایسنس"
              tone={license?.status === "active" ? "success" : license?.status === "unactivated" ? "warning" : "danger"}
            />
          </div>

          <div className="card-grid" style={{ marginBottom: 16 }}>
            <div className="card">
              <div className="card-header">
                <div className="card-title-row">
                  <AlertTriangle size={16} />
                  <span className="card-title">نیازمند توجه</span>
                </div>
                <Link to="/reports" className="pill-link">
                  مشاهده همه
                  <ArrowLeft size={13} />
                </Link>
              </div>
              {attentionRows.length === 0 ? (
                <EmptyState
                  icon={<CheckCircle2 size={30} />}
                  title="همه‌چیز مرتب است"
                  description="هیچ دستگاهی نیاز به توجه فوری ندارد."
                />
              ) : (
                <div className="table-wrap">
                  <table className="data-table">
                    <thead>
                      <tr>
                        <th>دستگاه</th>
                        <th>وضعیت اتصال</th>
                        <th>بکاپ</th>
                        <th>هشدار باز</th>
                      </tr>
                    </thead>
                    <tbody>
                      {attentionRows.map((r) => (
                        <tr key={r.device_id}>
                          <td className="cell-primary">
                            <Link to={`/devices/${r.device_id}/backups`}>{r.device_name}</Link>
                          </td>
                          <td>
                            <span className={`badge badge-${r.status === "online" ? "success" : "danger"}`}>
                              {r.status}
                            </span>
                          </td>
                          <td className={r.backup_overdue ? "" : "cell-muted"}>
                            {r.backup_overdue ? <span className="badge badge-warning">عقب‌افتاده</span> : "به‌روز"}
                          </td>
                          <td>{r.open_alert_count > 0 ? <span className="badge badge-danger">{r.open_alert_count}</span> : "-"}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              )}
            </div>

            <div className="card">
              <div className="card-title-row" style={{ marginBottom: 14 }}>
                <MessageSquareText size={16} />
                <span className="card-title">دسترسی سریع</span>
              </div>
              <div style={{ display: "flex", flexDirection: "column", gap: 8 }}>
                {QUICK_LINKS.map((link) => (
                  <Link
                    key={link.to}
                    to={link.to}
                    style={{
                      display: "flex",
                      alignItems: "center",
                      gap: 10,
                      padding: "10px 12px",
                      borderRadius: 10,
                      border: "1px solid var(--border)",
                      color: "var(--text)",
                    }}
                  >
                    <div
                      style={{
                        width: 32,
                        height: 32,
                        borderRadius: 8,
                        background: "var(--primary-soft)",
                        color: "var(--primary)",
                        display: "flex",
                        alignItems: "center",
                        justifyContent: "center",
                        flexShrink: 0,
                      }}
                    >
                      <link.icon size={16} />
                    </div>
                    <div style={{ flex: 1, minWidth: 0 }}>
                      <div style={{ fontSize: 13, fontWeight: 600 }}>{link.label}</div>
                      <div style={{ fontSize: 11.5, color: "var(--text-muted)" }}>{link.desc}</div>
                    </div>
                    <ArrowLeft size={14} color="var(--text-faint)" />
                  </Link>
                ))}
              </div>
            </div>
          </div>
        </>
      )}
    </div>
  );
}
