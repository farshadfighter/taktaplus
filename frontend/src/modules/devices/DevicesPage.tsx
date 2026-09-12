import { FormEvent, useEffect, useState } from "react";
import { Link } from "react-router-dom";

import { apiClient } from "@/services/apiClient";
import { Device, DeviceCreatePayload, DeviceTestResult, VendorType } from "@/modules/devices/types";

const VENDOR_LABELS: Record<VendorType, string> = {
  fortigate: "FortiGate",
  fortiweb: "FortiWeb",
};

const STATUS_LABELS: Record<Device["status"], string> = {
  unknown: "نامشخص",
  online: "آنلاین",
  offline: "آفلاین",
  error: "خطا",
};

const STATUS_COLORS: Record<Device["status"], string> = {
  unknown: "var(--text-muted)",
  online: "var(--success)",
  offline: "var(--text-muted)",
  error: "var(--danger)",
};

function emptyForm(): DeviceCreatePayload {
  return { name: "", vendor_type: "fortigate", host: "", port: 443, verify_tls: false, vdom: "root" };
}

export function DevicesPage() {
  const [devices, setDevices] = useState<Device[]>([]);
  const [loading, setLoading] = useState(true);
  const [showForm, setShowForm] = useState(false);
  const [form, setForm] = useState<DeviceCreatePayload>(emptyForm());
  const [formError, setFormError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);
  const [testingId, setTestingId] = useState<string | null>(null);

  async function loadDevices() {
    setLoading(true);
    try {
      const { data } = await apiClient.get<Device[]>("/devices");
      setDevices(data);
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => {
    loadDevices();
  }, []);

  async function handleSubmit(event: FormEvent) {
    event.preventDefault();
    setFormError(null);
    setSubmitting(true);
    try {
      await apiClient.post("/devices", form);
      setForm(emptyForm());
      setShowForm(false);
      await loadDevices();
    } catch (err: any) {
      const detail = err?.response?.data?.detail;
      setFormError(typeof detail === "string" ? detail : "افزودن دستگاه ناموفق بود - ورودی‌ها را بررسی کنید");
    } finally {
      setSubmitting(false);
    }
  }

  async function handleTestConnection(id: string) {
    setTestingId(id);
    try {
      const { data } = await apiClient.post<DeviceTestResult>(`/devices/${id}/test-connection`);
      await loadDevices();
      if (!data.success) {
        alert(`تست اتصال ناموفق بود: ${data.error}`);
      }
    } finally {
      setTestingId(null);
    }
  }

  async function handleDelete(id: string) {
    if (!confirm("این دستگاه حذف شود؟")) return;
    await apiClient.delete(`/devices/${id}`);
    await loadDevices();
  }

  return (
    <div>
      <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center", marginBottom: 16 }}>
        <h2 style={{ margin: 0 }}>دستگاه‌ها</h2>
        <button className="btn" onClick={() => setShowForm((v) => !v)}>
          {showForm ? "انصراف" : "افزودن دستگاه"}
        </button>
      </div>

      {showForm && (
        <form className="card" style={{ marginBottom: 16, maxWidth: 480 }} onSubmit={handleSubmit}>
          {formError && <div className="banner banner-danger">{formError}</div>}

          <label>نام دستگاه</label>
          <input
            className="input"
            required
            value={form.name}
            onChange={(e) => setForm({ ...form, name: e.target.value })}
          />

          <label>نوع تجهیز</label>
          <select
            className="input"
            value={form.vendor_type}
            onChange={(e) => setForm({ ...form, vendor_type: e.target.value as VendorType })}
          >
            <option value="fortigate">FortiGate</option>
            <option value="fortiweb">FortiWeb</option>
          </select>

          <label>آدرس (IP یا هاست)</label>
          <input
            className="input"
            required
            value={form.host}
            onChange={(e) => setForm({ ...form, host: e.target.value })}
          />

          <label>پورت</label>
          <input
            className="input"
            type="number"
            value={form.port}
            onChange={(e) => setForm({ ...form, port: Number(e.target.value) })}
          />

          {form.vendor_type === "fortigate" && (
            <>
              <label>VDOM</label>
              <input
                className="input"
                value={form.vdom}
                onChange={(e) => setForm({ ...form, vdom: e.target.value })}
              />
              <label>API Token</label>
              <input
                className="input"
                required
                value={form.api_token ?? ""}
                onChange={(e) => setForm({ ...form, api_token: e.target.value })}
              />
            </>
          )}

          {form.vendor_type === "fortiweb" && (
            <>
              <label>نام کاربری</label>
              <input
                className="input"
                required
                value={form.username ?? ""}
                onChange={(e) => setForm({ ...form, username: e.target.value })}
              />
              <label>رمز عبور</label>
              <input
                className="input"
                type="password"
                required
                value={form.password ?? ""}
                onChange={(e) => setForm({ ...form, password: e.target.value })}
              />
            </>
          )}

          <label style={{ display: "flex", alignItems: "center", gap: 8, marginBottom: 12 }}>
            <input
              type="checkbox"
              checked={form.verify_tls}
              onChange={(e) => setForm({ ...form, verify_tls: e.target.checked })}
            />
            بررسی گواهی SSL (در صورت داشتن گواهی معتبر فعال کنید)
          </label>

          <button className="btn" type="submit" disabled={submitting}>
            {submitting ? "در حال ثبت..." : "ثبت دستگاه"}
          </button>
        </form>
      )}

      <div className="card">
        {loading ? (
          <p>در حال بارگذاری...</p>
        ) : devices.length === 0 ? (
          <p>هنوز دستگاهی اضافه نشده است.</p>
        ) : (
          <table style={{ width: "100%", borderCollapse: "collapse" }}>
            <thead>
              <tr style={{ textAlign: "right", borderBottom: "1px solid var(--border)" }}>
                <th style={{ padding: 8 }}>نام</th>
                <th style={{ padding: 8 }}>نوع</th>
                <th style={{ padding: 8 }}>آدرس</th>
                <th style={{ padding: 8 }}>وضعیت</th>
                <th style={{ padding: 8 }}>فرم‌ور</th>
                <th style={{ padding: 8 }}>عملیات</th>
              </tr>
            </thead>
            <tbody>
              {devices.map((d) => (
                <tr key={d.id} style={{ borderBottom: "1px solid var(--border)" }}>
                  <td style={{ padding: 8 }}>{d.name}</td>
                  <td style={{ padding: 8 }}>{VENDOR_LABELS[d.vendor_type]}</td>
                  <td style={{ padding: 8 }}>
                    {d.host}:{d.port}
                  </td>
                  <td style={{ padding: 8, color: STATUS_COLORS[d.status] }}>{STATUS_LABELS[d.status]}</td>
                  <td style={{ padding: 8 }}>{d.firmware_version || "-"}</td>
                  <td style={{ padding: 8, display: "flex", gap: 8 }}>
                    <button
                      className="btn"
                      style={{ padding: "4px 10px", fontSize: 12 }}
                      disabled={testingId === d.id}
                      onClick={() => handleTestConnection(d.id)}
                    >
                      {testingId === d.id ? "در حال تست..." : "تست اتصال"}
                    </button>
                    <Link to={`/devices/${d.id}/backups`} className="btn" style={{ padding: "4px 10px", fontSize: 12 }}>
                      بکاپ‌ها
                    </Link>
                    <button
                      className="btn"
                      style={{ padding: "4px 10px", fontSize: 12, background: "var(--danger)" }}
                      onClick={() => handleDelete(d.id)}
                    >
                      حذف
                    </button>
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
