import { Plus, Radio, Trash2 } from "lucide-react";
import { FormEvent, useEffect, useState } from "react";

import { Badge } from "@/components/ui/Badge";
import { EmptyState } from "@/components/ui/EmptyState";
import { LoadingState } from "@/components/ui/LoadingState";
import { PageHeader } from "@/components/ui/PageHeader";
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
      <PageHeader
        title="کلاینت‌های RADIUS (فورتی‌گیت‌ها)"
        subtitle="هر فورتی‌گیتی که برای ورود ادمین، SSL VPN یا IPsec به این سرور RADIUS متصل می‌شود باید اینجا با آی‌پی و رمز مشترک خودش ثبت شود. تغییرات حداکثر تا ۳۰ ثانیه بعد به‌صورت خودکار اعمال می‌شود."
      />

      <form className="card" style={{ marginBottom: 16, maxWidth: 480 }} onSubmit={handleCreate}>
        <div className="card-title-row" style={{ marginBottom: 14 }}>
          <Plus size={16} />
          <span className="card-title">افزودن کلاینت</span>
        </div>
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
        <div className="card-title" style={{ marginBottom: 14 }}>کلاینت‌های موجود</div>
        {loading ? (
          <LoadingState />
        ) : clients.length === 0 ? (
          <EmptyState icon={<Radio size={32} />} title="هنوز کلاینتی اضافه نشده است" />
        ) : (
          <div className="table-wrap">
            <table className="data-table">
              <thead>
                <tr>
                  <th>نام</th>
                  <th>آی‌پی</th>
                  <th>وضعیت</th>
                  <th>عملیات</th>
                </tr>
              </thead>
              <tbody>
                {clients.map((c) => (
                  <tr key={c.id}>
                    <td className="cell-primary">{c.name}</td>
                    <td className="cell-mono">{c.nas_ip}</td>
                    <td>
                      <Badge variant={c.enabled ? "success" : "neutral"}>{c.enabled ? "فعال" : "غیرفعال"}</Badge>
                    </td>
                    <td>
                      <button className="btn btn-danger-ghost btn-sm btn-icon" onClick={() => handleDelete(c)} title="حذف">
                        <Trash2 size={13} />
                      </button>
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
