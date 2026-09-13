import { FormEvent, useEffect, useState } from "react";
import { Link, useParams } from "react-router-dom";

import { apiClient } from "@/services/apiClient";
import { Device } from "@/modules/devices/types";
import { Package, PushRecord } from "@/modules/distribution/types";

export function DevicePushPage() {
  const { deviceId } = useParams<{ deviceId: string }>();
  const [device, setDevice] = useState<Device | null>(null);
  const [packages, setPackages] = useState<Package[]>([]);
  const [history, setHistory] = useState<PushRecord[]>([]);
  const [loading, setLoading] = useState(true);
  const [pushingId, setPushingId] = useState<string | null>(null);

  const [sshPort, setSshPort] = useState(22);
  const [sshUsername, setSshUsername] = useState("");
  const [sshPassword, setSshPassword] = useState("");
  const [savingSsh, setSavingSsh] = useState(false);

  async function load() {
    setLoading(true);
    try {
      const deviceRes = await apiClient.get<Device>(`/devices/${deviceId}`);
      setDevice(deviceRes.data);
      setSshPort(deviceRes.data.ssh_port);
      setSshUsername(deviceRes.data.ssh_username ?? "");

      const [pkgRes, historyRes] = await Promise.all([
        apiClient.get<Package[]>("/distribution/packages", { params: { vendor_type: deviceRes.data.vendor_type } }),
        apiClient.get<PushRecord[]>(`/devices/${deviceId}/push-history`),
      ]);
      setPackages(pkgRes.data);
      setHistory(historyRes.data);
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => {
    load();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [deviceId]);

  async function handleSaveSsh(event: FormEvent) {
    event.preventDefault();
    setSavingSsh(true);
    try {
      await apiClient.patch(`/devices/${deviceId}/ssh`, { port: sshPort, username: sshUsername, password: sshPassword });
      setSshPassword("");
      await load();
    } finally {
      setSavingSsh(false);
    }
  }

  async function handlePush(pkg: Package) {
    if (!confirm(`بسته «${pkg.filename}» روی این دستگاه پوش شود؟`)) return;
    setPushingId(pkg.id);
    try {
      await apiClient.post(`/devices/${deviceId}/push/${pkg.id}`);
      await load();
    } catch (err: any) {
      alert(err?.response?.data?.detail ?? "پوش ناموفق بود");
    } finally {
      setPushingId(null);
    }
  }

  return (
    <div>
      <div style={{ marginBottom: 16 }}>
        <Link to="/devices">&larr; بازگشت به دستگاه‌ها</Link>
      </div>
      <h2 style={{ marginTop: 0 }}>توزیع سیگنیچر/فرم‌ور - {device?.name ?? "..."}</h2>

      {device?.vendor_type === "fortigate" && (
        <form className="card" style={{ marginBottom: 16, maxWidth: 420 }} onSubmit={handleSaveSsh}>
          <h3 style={{ marginTop: 0 }}>تنظیمات SSH (لازم برای پوش سیگنیچر)</h3>
          <p style={{ fontSize: 12, color: "var(--text-muted)" }}>
            چون فورتی‌گیت برای دستور بازیابی سیگنیچر معادل REST API ندارد، این عملیات از طریق SSH انجام می‌شود.
          </p>
          <label>پورت</label>
          <input className="input" type="number" value={sshPort} onChange={(e) => setSshPort(Number(e.target.value))} />
          <label>نام کاربری</label>
          <input className="input" value={sshUsername} onChange={(e) => setSshUsername(e.target.value)} />
          <label>رمز عبور</label>
          <input className="input" type="password" value={sshPassword} onChange={(e) => setSshPassword(e.target.value)} placeholder="برای تغییر وارد کنید" />
          <button className="btn" type="submit" disabled={savingSsh}>
            {savingSsh ? "در حال ذخیره..." : "ذخیره"}
          </button>
        </form>
      )}

      <div className="card" style={{ marginBottom: 16 }}>
        <h3 style={{ marginTop: 0 }}>بسته‌های قابل‌پوش</h3>
        {loading ? (
          <p>در حال بارگذاری...</p>
        ) : packages.length === 0 ? (
          <p>بسته‌ای برای این نوع تجهیز موجود نیست. از صفحه «سیگنیچر و فرم‌ور» اضافه کنید.</p>
        ) : (
          <table style={{ width: "100%", borderCollapse: "collapse" }}>
            <thead>
              <tr style={{ textAlign: "right", borderBottom: "1px solid var(--border)" }}>
                <th style={{ padding: 8 }}>فایل</th>
                <th style={{ padding: 8 }}>نوع بسته</th>
                <th style={{ padding: 8 }}>عملیات</th>
              </tr>
            </thead>
            <tbody>
              {packages.map((p) => (
                <tr key={p.id} style={{ borderBottom: "1px solid var(--border)" }}>
                  <td style={{ padding: 8 }}>{p.filename}</td>
                  <td style={{ padding: 8 }}>{p.package_type}</td>
                  <td style={{ padding: 8 }}>
                    <button className="btn" style={{ padding: "4px 10px", fontSize: 12 }} disabled={pushingId === p.id} onClick={() => handlePush(p)}>
                      {pushingId === p.id ? "در حال پوش..." : "پوش به این دستگاه"}
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>

      <div className="card">
        <h3 style={{ marginTop: 0 }}>تاریخچه پوش</h3>
        {history.length === 0 ? (
          <p>هنوز پوشی انجام نشده است.</p>
        ) : (
          <table style={{ width: "100%", borderCollapse: "collapse" }}>
            <thead>
              <tr style={{ textAlign: "right", borderBottom: "1px solid var(--border)" }}>
                <th style={{ padding: 8 }}>زمان</th>
                <th style={{ padding: 8 }}>وضعیت</th>
                <th style={{ padding: 8 }}>سلامت پس از پوش</th>
                <th style={{ padding: 8 }}>خطا</th>
              </tr>
            </thead>
            <tbody>
              {history.map((h) => (
                <tr key={h.id} style={{ borderBottom: "1px solid var(--border)" }}>
                  <td style={{ padding: 8 }}>{new Date(h.pushed_at).toLocaleString("fa-IR")}</td>
                  <td style={{ padding: 8, color: h.status === "success" ? "var(--success)" : "var(--danger)" }}>
                    {h.status === "success" ? "موفق" : "ناموفق"}
                  </td>
                  <td style={{ padding: 8 }}>
                    {h.post_push_check_ok === null ? "-" : h.post_push_check_ok ? "سالم" : "نامشخص/خطا"}
                  </td>
                  <td style={{ padding: 8 }}>{h.error_message || "-"}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
    </div>
  );
}
