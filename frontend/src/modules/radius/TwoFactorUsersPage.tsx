import { DownloadCloud, KeyRound, LockOpen, Power, Trash2, UserPlus } from "lucide-react";
import { FormEvent, useEffect, useRef, useState } from "react";

import { Badge } from "@/components/ui/Badge";
import { EmptyState } from "@/components/ui/EmptyState";
import { LoadingState } from "@/components/ui/LoadingState";
import { PageHeader } from "@/components/ui/PageHeader";
import { apiClient } from "@/services/apiClient";
import { Device } from "@/modules/devices/types";
import { LocalUserCandidate, TwoFactorUser } from "@/modules/radius/types";

const SOURCE_LABELS: Record<LocalUserCandidate["source"], string> = {
  admin: "کاربر ادمین",
  local_user: "کاربر محلی VPN",
};

export function TwoFactorUsersPage() {
  const [users, setUsers] = useState<TwoFactorUser[]>([]);
  const [loading, setLoading] = useState(true);

  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [mobileNumber, setMobileNumber] = useState("");
  const [creating, setCreating] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const passwordRef = useRef<HTMLInputElement>(null);

  const [devices, setDevices] = useState<Device[]>([]);
  const [selectedDeviceId, setSelectedDeviceId] = useState("");
  const [candidates, setCandidates] = useState<LocalUserCandidate[] | null>(null);
  const [loadingCandidates, setLoadingCandidates] = useState(false);
  const [candidatesError, setCandidatesError] = useState<string | null>(null);

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
    apiClient.get<Device[]>("/devices").then((res) => {
      setDevices(res.data);
      if (res.data.length > 0) setSelectedDeviceId(res.data[0].id);
    });
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
      if (candidates) await loadCandidates(selectedDeviceId);
    } catch (err: any) {
      setError(err?.response?.data?.detail ?? "ایجاد کاربر ناموفق بود");
    } finally {
      setCreating(false);
    }
  }

  async function loadCandidates(deviceId: string) {
    if (!deviceId) return;
    setCandidatesError(null);
    setLoadingCandidates(true);
    try {
      const { data } = await apiClient.get<LocalUserCandidate[]>(`/devices/${deviceId}/local-users`);
      setCandidates(data);
    } catch (err: any) {
      setCandidates(null);
      setCandidatesError(err?.response?.data?.detail ?? "دریافت لیست کاربران از دستگاه ناموفق بود");
    } finally {
      setLoadingCandidates(false);
    }
  }

  function handlePickCandidate(candidate: LocalUserCandidate) {
    setUsername(candidate.username);
    setMobileNumber(candidate.existing_mobile ?? "");
    setPassword("");
    passwordRef.current?.focus();
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

      <div className="card" style={{ marginBottom: 16, maxWidth: 640 }}>
        <div className="card-title-row" style={{ marginBottom: 6 }}>
          <DownloadCloud size={16} />
          <span className="card-title">دریافت کاربران از دستگاه</span>
        </div>
        <p className="card-description" style={{ marginBottom: 14 }}>
          به‌جای تایپ دستی نام کاربری، لیست کاربران ادمین و VPN محلی که از قبل روی خودِ دستگاه
          تعریف شده‌اند را بگیرید و از بین آن‌ها انتخاب کنید - فقط شماره موبایل و رمز عبور (که
          باید با رمز واقعی روی دستگاه یکی باشد) را وارد می‌کنید.
        </p>
        <div style={{ display: "flex", gap: 8, marginBottom: candidates || candidatesError ? 14 : 0 }}>
          <select
            className="input"
            style={{ marginBottom: 0, flex: 1 }}
            value={selectedDeviceId}
            onChange={(e) => {
              setSelectedDeviceId(e.target.value);
              setCandidates(null);
              setCandidatesError(null);
            }}
          >
            {devices.length === 0 && <option value="">دستگاهی موجود نیست</option>}
            {devices.map((d) => (
              <option key={d.id} value={d.id}>
                {d.name}
              </option>
            ))}
          </select>
          <button
            type="button"
            className="btn btn-secondary"
            disabled={!selectedDeviceId || loadingCandidates}
            onClick={() => loadCandidates(selectedDeviceId)}
          >
            {loadingCandidates ? "در حال دریافت..." : "دریافت لیست کاربران"}
          </button>
        </div>

        {candidatesError && (
          <div className="banner banner-danger" style={{ marginBottom: 0 }}>
            {candidatesError}
          </div>
        )}

        {candidates && (
          candidates.length === 0 ? (
            <EmptyState icon={<KeyRound size={26} />} title="کاربری روی این دستگاه یافت نشد" />
          ) : (
            <div className="table-wrap">
              <table className="data-table">
                <thead>
                  <tr>
                    <th>نام کاربری</th>
                    <th>نوع</th>
                    <th>موبایل (در صورت وجود)</th>
                    <th>عملیات</th>
                  </tr>
                </thead>
                <tbody>
                  {candidates.map((c) => (
                    <tr key={c.username} className={c.already_linked ? "row-muted" : ""}>
                      <td className="cell-primary">{c.username}</td>
                      <td className="cell-muted">{SOURCE_LABELS[c.source]}</td>
                      <td className="cell-muted">{c.existing_mobile || "-"}</td>
                      <td>
                        {c.already_linked ? (
                          <Badge variant="neutral">قبلاً افزوده شده</Badge>
                        ) : (
                          <button className="btn btn-secondary btn-sm" onClick={() => handlePickCandidate(c)}>
                            انتخاب
                          </button>
                        )}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )
        )}
      </div>

      <form className="card" style={{ marginBottom: 16, maxWidth: 480 }} onSubmit={handleCreate}>
        <div className="card-title-row" style={{ marginBottom: 14 }}>
          <UserPlus size={16} />
          <span className="card-title">افزودن کاربر</span>
        </div>
        {error && <div className="banner banner-danger">{error}</div>}
        <label>نام کاربری (باید با نام کاربری تنظیم‌شده در FortiGate یکسان باشد)</label>
        <input className="input" value={username} onChange={(e) => setUsername(e.target.value)} required />
        <label>رمز عبور اصلی (باید با رمز واقعی روی دستگاه یکی باشد)</label>
        <input
          ref={passwordRef}
          className="input"
          type="password"
          value={password}
          onChange={(e) => setPassword(e.target.value)}
          required
        />
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
