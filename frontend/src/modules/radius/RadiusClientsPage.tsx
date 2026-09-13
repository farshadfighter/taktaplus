import { FormEvent, useEffect, useState } from "react";

import { apiClient } from "@/services/apiClient";
import { RadiusClient } from "@/modules/radius/types";

export function RadiusClientsPage() {
  const [clients, setClients] = useState<RadiusClient[]>([]);
  const [loading, setLoading] = useState(true);

  const [name, setName] = useState("");
  const [nasIp, setNasIp] = useState("");
  const [sharedSecret, setSharedSecret] = useState("");
  const [creating, setCreating] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function load() {
    setLoading(true);
    try {
      const { data } = await apiClient.get<RadiusClient[]>("/radius/clients");
      setClients(data);
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => {
    load();
  }, []);

  async function handleCreate(event: FormEvent) {
    event.preventDefault();
    setError(null);
    setCreating(true);
    try {
      await apiClient.post("/radius/clients", { name, nas_ip: nasIp, shared_secret: sharedSecret });
      setName("");
      setNasIp("");
      setSharedSecret("");
      await load();
    } catch (err: any) {
      setError(err?.response?.data?.detail ?? "ایجاد کلاینت رادیوس ناموفق بود");
    } finally {
      setCreating(false);
    }
  }

  async function handleDelete(client: RadiusClient) {
    if (!confirm(`کلاینت رادیوس «${client.name}» حذف شود؟`)) return;
    await apiClient.delete(`/radius/clients/${client.id}`);
    await load();
  }

  return (
    <div>
      <h2 style={{ marginTop: 0 }}>کلاینت‌های RADIUS (فورتی‌گیت‌ها)</h2>
      <p style={{ fontSize: 13, color: "var(--text-muted)", maxWidth: 640 }}>
        هر فورتی‌گیتی که قرار است برای ورود ادمین، SSL VPN یا IPsec به این سرور RADIUS متصل
        شود باید اینجا با آی‌پی و رمز مشترک خودش ثبت شود. تغییرات پس از راه‌اندازی مجدد سرویس
        radius-server اعمال می‌شود.
      </p>

      <form className="card" style={{ marginBottom: 16, maxWidth: 480 }} onSubmit={handleCreate}>
        <h3 style={{ marginTop: 0 }}>افزودن کلاینت</h3>
        {error && <div className="banner banner-danger">{error}</div>}
        <label>نام</label>
        <input className="input" value={name} onChange={(e) => setName(e.target.value)} required />
        <label>آی‌پی NAS</label>
        <input className="input" value={nasIp} onChange={(e) => setNasIp(e.target.value)} required />
        <label>رمز مشترک (Shared Secret)</label>
        <input className="input" type="password" value={sharedSecret} onChange={(e) => setSharedSecret(e.target.value)} required />
        <button className="btn" type="submit" disabled={creating}>
          {creating ? "در حال ایجاد..." : "افزودن"}
        </button>
      </form>

      <div className="card">
        <h3 style={{ marginTop: 0 }}>کلاینت‌های موجود</h3>
        {loading ? (
          <p>در حال بارگذاری...</p>
        ) : clients.length === 0 ? (
          <p>هنوز کلاینتی اضافه نشده است.</p>
        ) : (
          <table style={{ width: "100%", borderCollapse: "collapse" }}>
            <thead>
              <tr style={{ textAlign: "right", borderBottom: "1px solid var(--border)" }}>
                <th style={{ padding: 8 }}>نام</th>
                <th style={{ padding: 8 }}>آی‌پی</th>
                <th style={{ padding: 8 }}>وضعیت</th>
                <th style={{ padding: 8 }}>عملیات</th>
              </tr>
            </thead>
            <tbody>
              {clients.map((c) => (
                <tr key={c.id} style={{ borderBottom: "1px solid var(--border)" }}>
                  <td style={{ padding: 8 }}>{c.name}</td>
                  <td style={{ padding: 8 }}>{c.nas_ip}</td>
                  <td style={{ padding: 8 }}>{c.enabled ? "فعال" : "غیرفعال"}</td>
                  <td style={{ padding: 8 }}>
                    <button
                      className="btn"
                      style={{ padding: "4px 10px", fontSize: 12, background: "var(--danger)" }}
                      onClick={() => handleDelete(c)}
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
