import { FormEvent, useEffect, useState } from "react";

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
      <h2 style={{ marginTop: 0 }}>کاربران احراز هویت دومرحله‌ای پیامکی</h2>

      <form className="card" style={{ marginBottom: 16, maxWidth: 480 }} onSubmit={handleCreate}>
        <h3 style={{ marginTop: 0 }}>افزودن کاربر</h3>
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
        <h3 style={{ marginTop: 0 }}>کاربران موجود</h3>
        {loading ? (
          <p>در حال بارگذاری...</p>
        ) : users.length === 0 ? (
          <p>هنوز کاربری اضافه نشده است.</p>
        ) : (
          <table style={{ width: "100%", borderCollapse: "collapse" }}>
            <thead>
              <tr style={{ textAlign: "right", borderBottom: "1px solid var(--border)" }}>
                <th style={{ padding: 8 }}>نام کاربری</th>
                <th style={{ padding: 8 }}>وضعیت</th>
                <th style={{ padding: 8 }}>تلاش‌های ناموفق</th>
                <th style={{ padding: 8 }}>عملیات</th>
              </tr>
            </thead>
            <tbody>
              {users.map((u) => (
                <tr key={u.id} style={{ borderBottom: "1px solid var(--border)" }}>
                  <td style={{ padding: 8 }}>{u.username}</td>
                  <td style={{ padding: 8 }}>
                    {!u.enabled ? "غیرفعال" : isLocked(u) ? "قفل‌شده" : "فعال"}
                  </td>
                  <td style={{ padding: 8 }}>{u.failed_attempts}</td>
                  <td style={{ padding: 8, display: "flex", gap: 6 }}>
                    <button className="btn" style={{ padding: "4px 10px", fontSize: 12 }} onClick={() => handleToggle(u)}>
                      {u.enabled ? "غیرفعال‌سازی" : "فعال‌سازی"}
                    </button>
                    {isLocked(u) && (
                      <button className="btn" style={{ padding: "4px 10px", fontSize: 12 }} onClick={() => handleUnlock(u)}>
                        باز کردن قفل
                      </button>
                    )}
                    <button
                      className="btn"
                      style={{ padding: "4px 10px", fontSize: 12, background: "var(--danger)" }}
                      onClick={() => handleDelete(u)}
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
