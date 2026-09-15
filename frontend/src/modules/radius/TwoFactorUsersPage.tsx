import { KeyRound, LockOpen, Power, Trash2, UserPlus } from "lucide-react";
import { FormEvent, useEffect, useState } from "react";

import { Badge } from "@/components/ui/Badge";
import { EmptyState } from "@/components/ui/EmptyState";
import { LoadingState } from "@/components/ui/LoadingState";
import { PageHeader } from "@/components/ui/PageHeader";
import { apiClient } from "@/services/apiClient";
import { TwoFactorUser } from "@/modules/radius/types";

export function TwoFactorUsersPage() {
  const [users, setUsers] = useState<TwoFactorUser[]>([]);
  const [loading, setLoading] = useState(true);

  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [mobileNumber, setMobileNumber] = useState("");
  const [creating, setCreating] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function load() {
    setLoading(true);
    try {
      const { data } = await apiClient.get<TwoFactorUser[]>("/radius/users");
      setUsers(data);
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
      await apiClient.post("/radius/users", { username, password, mobile_number: mobileNumber });
      setUsername("");
      setPassword("");
      setMobileNumber("");
      await load();
    } catch (err: any) {
      setError(err?.response?.data?.detail ?? "ایجاد کاربر ناموفق بود");
    } finally {
      setCreating(false);
    }
  }

  async function handleToggle(user: TwoFactorUser) {
    const action = user.enabled ? "disable" : "enable";
    await apiClient.post(`/radius/users/${user.id}/${action}`);
    await load();
  }

  async function handleUnlock(user: TwoFactorUser) {
    await apiClient.post(`/radius/users/${user.id}/unlock`);
    await load();
  }

  async function handleDelete(user: TwoFactorUser) {
    if (!confirm(`کاربر «${user.username}» حذف شود؟`)) return;
    await apiClient.delete(`/radius/users/${user.id}`);
    await load();
  }

  function isLocked(user: TwoFactorUser): boolean {
    return !!user.locked_until && new Date(user.locked_until).getTime() > Date.now();
  }

  return (
    <div>
      <PageHeader
        title="کاربران احراز هویت دومرحله‌ای پیامکی"
        subtitle="مدیریت کاربرانی که از طریق RADIUS و کد پیامکی وارد فورتی‌گیت می‌شوند."
      />

      <form className="card" style={{ marginBottom: 16, maxWidth: 480 }} onSubmit={handleCreate}>
        <div className="card-title-row" style={{ marginBottom: 14 }}>
          <UserPlus size={16} />
          <span className="card-title">افزودن کاربر</span>
        </div>
        {error && <div className="banner banner-danger">{error}</div>}
        <label>نام کاربری (باید با نام کاربری تنظیم‌شده در FortiGate یکسان باشد)</label>
        <input className="input" value={username} onChange={(e) => setUsername(e.target.value)} required />
        <label>رمز عبور اصلی</label>
        <input className="input" type="password" value={password} onChange={(e) => setPassword(e.target.value)} required />
        <label>شماره موبایل</label>
        <input className="input" value={mobileNumber} onChange={(e) => setMobileNumber(e.target.value)} required />
        <button className="btn" type="submit" disabled={creating}>
          {creating ? "در حال ایجاد..." : "ایجاد کاربر"}
        </button>
      </form>

      <div className="card">
        <div className="card-title" style={{ marginBottom: 14 }}>کاربران موجود</div>
        {loading ? (
          <LoadingState />
        ) : users.length === 0 ? (
          <EmptyState icon={<KeyRound size={32} />} title="هنوز کاربری اضافه نشده است" />
        ) : (
          <div className="table-wrap">
            <table className="data-table">
              <thead>
                <tr>
                  <th>نام کاربری</th>
                  <th>وضعیت</th>
                  <th>تلاش‌های ناموفق</th>
                  <th>عملیات</th>
                </tr>
              </thead>
              <tbody>
                {users.map((u) => (
                  <tr key={u.id}>
                    <td className="cell-primary">{u.username}</td>
                    <td>
                      {!u.enabled ? (
                        <Badge variant="neutral">غیرفعال</Badge>
                      ) : isLocked(u) ? (
                        <Badge variant="danger">قفل‌شده</Badge>
                      ) : (
                        <Badge variant="success">فعال</Badge>
                      )}
                    </td>
                    <td className="cell-muted">{u.failed_attempts}</td>
                    <td>
                      <div className="row-actions">
                        <button className="btn btn-secondary btn-sm" onClick={() => handleToggle(u)}>
                          <Power size={13} />
                          {u.enabled ? "غیرفعال‌سازی" : "فعال‌سازی"}
                        </button>
                        {isLocked(u) && (
                          <button className="btn btn-secondary btn-sm" onClick={() => handleUnlock(u)}>
                            <LockOpen size={13} />
                            باز کردن قفل
                          </button>
                        )}
                        <button className="btn btn-danger-ghost btn-sm btn-icon" onClick={() => handleDelete(u)} title="حذف">
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
