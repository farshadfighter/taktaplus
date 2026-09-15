import { ArrowRight, BellRing, Cpu, RadioTower, Zap } from "lucide-react";
import { FormEvent, useEffect, useState } from "react";
import { Link, useParams } from "react-router-dom";

import { Badge, BadgeVariant } from "@/components/ui/Badge";
import { EmptyState } from "@/components/ui/EmptyState";
import { LoadingState } from "@/components/ui/LoadingState";
import { PageHeader } from "@/components/ui/PageHeader";
import { apiClient } from "@/services/apiClient";
import { Device } from "@/modules/devices/types";
import { SnmpAlert, SnmpMetric } from "@/modules/monitoring/types";

const SEVERITY_LABELS: Record<SnmpAlert["severity"], string> = {
  info: "اطلاعاتی",
  warning: "هشدار",
  critical: "بحرانی",
};

const SEVERITY_VARIANTS: Record<SnmpAlert["severity"], BadgeVariant> = {
  info: "info",
  warning: "warning",
  critical: "danger",
};

export function DeviceMonitoringPage() {
  const { deviceId } = useParams<{ deviceId: string }>();
  const [device, setDevice] = useState<Device | null>(null);
  const [metrics, setMetrics] = useState<SnmpMetric[]>([]);
  const [alerts, setAlerts] = useState<SnmpAlert[]>([]);
  const [loading, setLoading] = useState(true);
  const [polling, setPolling] = useState(false);

  const [snmpEnabled, setSnmpEnabled] = useState(false);
  const [snmpPort, setSnmpPort] = useState(161);
  const [community, setCommunity] = useState("");
  const [savingSnmp, setSavingSnmp] = useState(false);
  const [snmpError, setSnmpError] = useState<string | null>(null);

  async function load() {
    setLoading(true);
    try {
      const [deviceRes, metricsRes, alertsRes] = await Promise.all([
        apiClient.get<Device>(`/devices/${deviceId}`),
        apiClient.get<SnmpMetric[]>(`/devices/${deviceId}/metrics`),
        apiClient.get<SnmpAlert[]>(`/devices/${deviceId}/alerts`),
      ]);
      setDevice(deviceRes.data);
      setSnmpEnabled(deviceRes.data.snmp_enabled);
      setSnmpPort(deviceRes.data.snmp_port);
      setMetrics(metricsRes.data);
      setAlerts(alertsRes.data);
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => {
    load();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [deviceId]);

  async function handleSaveSnmp(event: FormEvent) {
    event.preventDefault();
    setSnmpError(null);
    setSavingSnmp(true);
    try {
      await apiClient.patch(`/devices/${deviceId}/snmp`, {
        enabled: snmpEnabled,
        port: snmpPort,
        community: community || undefined,
      });
      setCommunity("");
      await load();
    } catch (err: any) {
      setSnmpError(err?.response?.data?.detail ?? "ذخیره تنظیمات SNMP ناموفق بود");
    } finally {
      setSavingSnmp(false);
    }
  }

  async function handlePollNow() {
    setPolling(true);
    try {
      await apiClient.post(`/devices/${deviceId}/metrics/poll-now`);
      await load();
    } catch (err: any) {
      alert(err?.response?.data?.detail ?? "بررسی SNMP ناموفق بود");
    } finally {
      setPolling(false);
    }
  }

  async function handleAck(alert: SnmpAlert) {
    await apiClient.post(`/devices/${deviceId}/alerts/${alert.id}/ack`);
    await load();
  }

  const latest = metrics[0];

  return (
    <div>
      <Link to="/devices" className="back-link">
        <ArrowRight size={14} />
        بازگشت به دستگاه‌ها
      </Link>
      <PageHeader title={`مانیتورینگ ${device?.name ?? "..."}`} subtitle="پایش زنده CPU/حافظه/نشست از طریق SNMP و مدیریت هشدارها." />

      {device?.snmp_enabled && latest && (
        <div className="stat-grid">
          <div className="stat-card">
            <div className="stat-icon" style={{ background: "var(--primary-soft)", color: "var(--primary)" }}>
              <Cpu size={18} />
            </div>
            <div>
              <div className="stat-value">{latest.cpu_percent !== null ? `${latest.cpu_percent.toFixed(0)}%` : "-"}</div>
              <div className="stat-label">مصرف CPU</div>
            </div>
          </div>
          <div className="stat-card">
            <div className="stat-icon" style={{ background: "var(--info-soft)", color: "var(--info)" }}>
              <Zap size={18} />
            </div>
            <div>
              <div className="stat-value">{latest.memory_percent !== null ? `${latest.memory_percent.toFixed(0)}%` : "-"}</div>
              <div className="stat-label">مصرف حافظه</div>
            </div>
          </div>
          <div className="stat-card">
            <div className="stat-icon" style={{ background: "var(--warning-soft)", color: "var(--warning)" }}>
              <BellRing size={18} />
            </div>
            <div>
              <div className="stat-value">{alerts.filter((a) => !a.acknowledged).length}</div>
              <div className="stat-label">هشدارهای بازتاییدنشده</div>
            </div>
          </div>
        </div>
      )}

      <form className="card" style={{ marginBottom: 16, maxWidth: 420 }} onSubmit={handleSaveSnmp}>
        <div className="card-title-row" style={{ marginBottom: 14 }}>
          <RadioTower size={16} />
          <span className="card-title">تنظیمات SNMP</span>
        </div>
        {snmpError && <div className="banner banner-danger">{snmpError}</div>}
        <label className="checkbox-row">
          <input type="checkbox" checked={snmpEnabled} onChange={(e) => setSnmpEnabled(e.target.checked)} />
          فعال‌سازی مانیتورینگ SNMP (نسخه v2c)
        </label>
        <label>پورت</label>
        <input className="input" type="number" value={snmpPort} onChange={(e) => setSnmpPort(Number(e.target.value))} />
        <label>Community (برای تغییر یا فعال‌سازی وارد کنید)</label>
        <input className="input" value={community} onChange={(e) => setCommunity(e.target.value)} placeholder="مثلاً public" />
        <div className="form-actions">
          <button className="btn" type="submit" disabled={savingSnmp}>
            {savingSnmp ? "در حال ذخیره..." : "ذخیره تنظیمات"}
          </button>
          {device?.snmp_enabled && (
            <button type="button" className="btn btn-secondary" onClick={handlePollNow} disabled={polling}>
              {polling ? "در حال بررسی..." : "بررسی الان"}
            </button>
          )}
        </div>
      </form>

      <div className="card" style={{ marginBottom: 16 }}>
        <div className="card-title" style={{ marginBottom: 14 }}>آخرین متریک‌ها</div>
        {loading ? (
          <LoadingState />
        ) : metrics.length === 0 ? (
          <EmptyState icon={<Cpu size={28} />} title="هنوز داده‌ای جمع‌آوری نشده است" />
        ) : (
          <div className="table-wrap">
            <table className="data-table">
              <thead>
                <tr>
                  <th>زمان</th>
                  <th>CPU</th>
                  <th>حافظه</th>
                  <th>تعداد نشست</th>
                </tr>
              </thead>
              <tbody>
                {metrics.slice(0, 20).map((m) => (
                  <tr key={m.id}>
                    <td className="cell-muted">{new Date(m.collected_at).toLocaleString("fa-IR")}</td>
                    <td>{m.cpu_percent !== null ? `${m.cpu_percent.toFixed(0)}%` : "-"}</td>
                    <td>{m.memory_percent !== null ? `${m.memory_percent.toFixed(0)}%` : "-"}</td>
                    <td>{m.session_count ?? "-"}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>

      <div className="card">
        <div className="card-title" style={{ marginBottom: 14 }}>هشدارها</div>
        {alerts.length === 0 ? (
          <EmptyState icon={<BellRing size={28} />} title="هشداری ثبت نشده است" />
        ) : (
          <div className="table-wrap">
            <table className="data-table">
              <thead>
                <tr>
                  <th>زمان</th>
                  <th>شدت</th>
                  <th>پیام</th>
                  <th>عملیات</th>
                </tr>
              </thead>
              <tbody>
                {alerts.map((a) => (
                  <tr key={a.id} className={a.acknowledged ? "row-muted" : ""}>
                    <td className="cell-muted">{new Date(a.received_at).toLocaleString("fa-IR")}</td>
                    <td>
                      <Badge variant={SEVERITY_VARIANTS[a.severity]}>{SEVERITY_LABELS[a.severity]}</Badge>
                    </td>
                    <td>{a.message}</td>
                    <td>
                      {!a.acknowledged && (
                        <button className="btn btn-secondary btn-sm" onClick={() => handleAck(a)}>
                          تایید
                        </button>
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
