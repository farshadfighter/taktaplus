import { Activity, Archive, Plus, RadioTower, Server, Trash2, UploadCloud, X } from "lucide-react";
import { FormEvent, useEffect, useState } from "react";
import { Link } from "react-router-dom";

import { Badge, BadgeVariant } from "@/components/ui/Badge";
import { EmptyState } from "@/components/ui/EmptyState";
import { LoadingState } from "@/components/ui/LoadingState";
import { PageHeader } from "@/components/ui/PageHeader";
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

const STATUS_VARIANTS: Record<Device["status"], BadgeVariant> = {
  unknown: "neutral",
  online: "success",
  offline: "neutral",
  error: "danger",
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
      <PageHeader
        title="دستگاه‌ها"
        subtitle="مدیریت متمرکز دستگاه‌های FortiGate و FortiWeb."
        actions={
          <button className="btn" onClick={() => setShowForm((v) => !v)}>
            {showForm ? <X size={15} /> : <Plus size={15} />}
            {showForm ? "انصراف" : "افزودن دستگاه"}
          </button>
        }
      />

      {showForm && (
        <form className="card" style={{ marginBottom: 16, maxWidth: 480 }} onSubmit={handleSubmit}>
          <div className="card-title-row" style={{ marginBottom: 14 }}>
            <Server size={16} />
            <span className="card-title">دستگاه جدید</span>
          </div>
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

          <label className="checkbox-row">
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
          <LoadingState />
        ) : devices.length === 0 ? (
          <EmptyState
            icon={<Server size={32} />}
            title="هنوز دستگاهی اضافه نشده است"
            description="با دکمه «افزودن دستگاه» اولین FortiGate یا FortiWeb خود را ثبت کنید."
          />
        ) : (
          <div className="table-wrap">
            <table className="data-table">
              <thead>
                <tr>
                  <th>نام</th>
                  <th>نوع</th>
                  <th>آدرس</th>
                  <th>وضعیت</th>
                  <th>فرم‌ور</th>
                  <th>عملیات</th>
                </tr>
              </thead>
              <tbody>
                {devices.map((d) => (
                  <tr key={d.id}>
                    <td className="cell-primary">{d.name}</td>
                    <td>{VENDOR_LABELS[d.vendor_type]}</td>
                    <td className="cell-mono">
                      {d.host}:{d.port}
                    </td>
                    <td>
                      <Badge variant={STATUS_VARIANTS[d.status]}>{STATUS_LABELS[d.status]}</Badge>
                    </td>
                    <td className="cell-muted">{d.firmware_version || "-"}</td>
                    <td>
                      <div className="row-actions">
                        <button
                          className="btn btn-secondary btn-sm"
                          disabled={testingId === d.id}
                          onClick={() => handleTestConnection(d.id)}
                          title="تست اتصال"
                        >
                          <Activity size={13} />
                          {testingId === d.id ? "..." : "تست اتصال"}
                        </button>
                        <Link to={`/devices/${d.id}/backups`} className="btn btn-secondary btn-sm" title="بکاپ‌ها">
                          <Archive size={13} />
                          بکاپ‌ها
                        </Link>
                        <Link to={`/devices/${d.id}/monitoring`} className="btn btn-secondary btn-sm" title="مانیتورینگ">
                          <RadioTower size={13} />
                          مانیتورینگ
                        </Link>
                        <Link to={`/devices/${d.id}/push`} className="btn btn-secondary btn-sm" title="سیگنیچر/فرم‌ور">
                          <UploadCloud size={13} />
                          پوش
                        </Link>
                        <button
                          className="btn btn-danger-ghost btn-sm btn-icon"
                          onClick={() => handleDelete(d.id)}
                          title="حذف دستگاه"
                        >
                          <Trash2 size={13} />
                        </button>
                      </div>
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
