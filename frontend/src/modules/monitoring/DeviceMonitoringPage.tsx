import { FormEvent, useEffect, useState } from "react";
import { Link, useParams } from "react-router-dom";

import { apiClient } from "@/services/apiClient";
import { Device } from "@/modules/devices/types";
import { SnmpAlert, SnmpMetric } from "@/modules/monitoring/types";

const SEVERITY_LABELS: Record<SnmpAlert["severity"], string> = {
  info: "اطلاعاتی",
  warning: "هشدار",
  critical: "بحرانی",
};

const SEVERITY_COLORS: Record<SnmpAlert["severity"], string> = {
  info: "var(--text-muted)",
  warning: "var(--warning)",
  critical: "var(--danger)",
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

  return (
    <div>
      <div style={{ marginBottom: 16 }}>
        <Link to="/devices">&larr; بازگشت به دستگاه‌ها</Link>
      </div>
      <h2 style={{ marginTop: 0 }}>مانیتورینگ {device?.name ?? "..."}</h2>

      <form className="card" style={{ marginBottom: 16, maxWidth: 420 }} onSubmit={handleSaveSnmp}>
        <h3 style={{ marginTop: 0 }}>تنظیمات SNMP</h3>
        {snmpError && <div className="banner banner-danger">{snmpError}</div>}
        <label style={{ display: "flex", alignItems: "center", gap: 8, marginBottom: 12 }}>
          <input type="checkbox" checked={snmpEnabled} onChange={(e) => setSnmpEnabled(e.target.checked)} />
          فعال‌سازی مانیتورینگ SNMP (نسخه v2c)
        </label>
        <label>پورت</label>
        <input className="input" type="number" value={snmpPort} onChange={(e) => setSnmpPort(Number(e.target.value))} />
        <label>Community (برای تغییر یا فعال‌سازی وارد کنید)</label>
        <input className="input" value={community} onChange={(e) => setCommunity(e.target.value)} placeholder="مثلاً public" />
        <button className="btn" type="submit" disabled={savingSnmp}>
          {savingSnmp ? "در حال ذخیره..." : "ذخیره تنظیمات"}
        </button>
        {device?.snmp_enabled && (
          <button type="button" className="btn" style={{ marginInlineStart: 8 }} onClick={handlePollNow} disabled={polling}>
            {polling ? "در حال بررسی..." : "بررسی الان"}
          </button>
        )}
      </form>

      <div className="card" style={{ marginBottom: 16 }}>
        <h3 style={{ marginTop: 0 }}>آخرین متریک‌ها</h3>
        {loading ? (
          <p>در حال بارگذاری...</p>
        ) : metrics.length === 0 ? (
          <p>هنوز داده‌ای جمع‌آوری نشده است.</p>
        ) : (
          <table style={{ width: "100%", borderCollapse: "collapse" }}>
            <thead>
              <tr style={{ textAlign: "right", borderBottom: "1px solid var(--border)" }}>
                <th style={{ padding: 8 }}>زمان</th>
                <th style={{ padding: 8 }}>CPU</th>
                <th style={{ padding: 8 }}>حافظه</th>
                <th style={{ padding: 8 }}>تعداد نشست</th>
              </tr>
            </thead>
            <tbody>
              {metrics.slice(0, 20).map((m) => (
                <tr key={m.id} style={{ borderBottom: "1px solid var(--border)" }}>
                  <td style={{ padding: 8 }}>{new Date(m.collected_at).toLocaleString("fa-IR")}</td>
                  <td style={{ padding: 8 }}>{m.cpu_percent !== null ? `${m.cpu_percent.toFixed(0)}%` : "-"}</td>
                  <td style={{ padding: 8 }}>{m.memory_percent !== null ? `${m.memory_percent.toFixed(0)}%` : "-"}</td>
                  <td style={{ padding: 8 }}>{m.session_count ?? "-"}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>

      <div className="card">
        <h3 style={{ marginTop: 0 }}>هشدارها</h3>
        {alerts.length === 0 ? (
          <p>هشداری ثبت نشده است.</p>
        ) : (
          <table style={{ width: "100%", borderCollapse: "collapse" }}>
            <thead>
              <tr style={{ textAlign: "right", borderBottom: "1px solid var(--border)" }}>
                <th style={{ padding: 8 }}>زمان</th>
                <th style={{ padding: 8 }}>شدت</th>
                <th style={{ padding: 8 }}>پیام</th>
                <th style={{ padding: 8 }}>عملیات</th>
              </tr>
            </thead>
            <tbody>
              {alerts.map((a) => (
                <tr key={a.id} style={{ borderBottom: "1px solid var(--border)", opacity: a.acknowledged ? 0.5 : 1 }}>
                  <td style={{ padding: 8 }}>{new Date(a.received_at).toLocaleString("fa-IR")}</td>
                  <td style={{ padding: 8, color: SEVERITY_COLORS[a.severity] }}>{SEVERITY_LABELS[a.severity]}</td>
                  <td style={{ padding: 8 }}>{a.message}</td>
                  <td style={{ padding: 8 }}>
                    {!a.acknowledged && (
                      <button className="btn" style={{ padding: "4px 10px", fontSize: 12 }} onClick={() => handleAck(a)}>
                        تایید
                      </button>
                    )}
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
