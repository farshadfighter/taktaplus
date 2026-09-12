import { useEffect, useState } from "react";
import { useParams, Link } from "react-router-dom";

import { apiClient } from "@/services/apiClient";
import { Device } from "@/modules/devices/types";
import { Backup } from "@/modules/backups/types";

const SOURCE_LABELS: Record<Backup["source"], string> = {
  manual: "دستی",
  scheduled: "زمان‌بندی‌شده",
};

const STATUS_LABELS: Record<Backup["status"], string> = {
  success: "موفق",
  failed: "ناموفق",
};

function formatSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  return `${(bytes / 1024).toFixed(1)} KB`;
}

export function DeviceBackupsPage() {
  const { deviceId } = useParams<{ deviceId: string }>();
  const [device, setDevice] = useState<Device | null>(null);
  const [backups, setBackups] = useState<Backup[]>([]);
  const [loading, setLoading] = useState(true);
  const [taking, setTaking] = useState(false);
  const [restoringId, setRestoringId] = useState<string | null>(null);

  async function load() {
    setLoading(true);
    try {
      const [deviceRes, backupsRes] = await Promise.all([
        apiClient.get<Device>(`/devices/${deviceId}`),
        apiClient.get<Backup[]>(`/devices/${deviceId}/backups`),
      ]);
      setDevice(deviceRes.data);
      setBackups(backupsRes.data);
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => {
    load();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [deviceId]);

  async function handleCreateBackup() {
    setTaking(true);
    try {
      await apiClient.post(`/devices/${deviceId}/backups`);
      await load();
    } finally {
      setTaking(false);
    }
  }

  async function handleDownload(backup: Backup) {
    const response = await apiClient.get(`/devices/${deviceId}/backups/${backup.id}/download`, {
      responseType: "blob",
    });
    const url = URL.createObjectURL(response.data);
    const a = document.createElement("a");
    a.href = url;
    a.download = `backup-${backup.taken_at.slice(0, 10)}.conf`;
    a.click();
    URL.revokeObjectURL(url);
  }

  async function handleRestore(backup: Backup, force = false) {
    if (!confirm(force ? "با وجود هشدار، ریستور با اجبار انجام شود؟" : "این بکاپ روی دستگاه ریستور شود؟")) return;
    setRestoringId(backup.id);
    try {
      await apiClient.post(`/devices/${deviceId}/backups/${backup.id}/restore`, { force });
      alert("ریستور با موفقیت انجام شد");
    } catch (err: any) {
      if (err?.response?.status === 409) {
        const detail = err.response.data?.detail ?? "";
        if (confirm(`${detail}\n\nآیا با وجود این هشدار می‌خواهید ادامه دهید؟`)) {
          await handleRestore(backup, true);
          return;
        }
      } else {
        alert(err?.response?.data?.detail ?? "ریستور ناموفق بود");
      }
    } finally {
      setRestoringId(null);
    }
  }

  async function handleDelete(backup: Backup) {
    if (!confirm("این بکاپ حذف شود؟")) return;
    await apiClient.delete(`/devices/${deviceId}/backups/${backup.id}`);
    await load();
  }

  return (
    <div>
      <div style={{ marginBottom: 16 }}>
        <Link to="/devices">&larr; بازگشت به دستگاه‌ها</Link>
      </div>
      <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center", marginBottom: 16 }}>
        <h2 style={{ margin: 0 }}>بکاپ‌های {device?.name ?? "..."}</h2>
        <button className="btn" onClick={handleCreateBackup} disabled={taking}>
          {taking ? "در حال بکاپ‌گیری..." : "بکاپ‌گیری الان"}
        </button>
      </div>

      <div className="card">
        {loading ? (
          <p>در حال بارگذاری...</p>
        ) : backups.length === 0 ? (
          <p>هنوز بکاپی گرفته نشده است.</p>
        ) : (
          <table style={{ width: "100%", borderCollapse: "collapse" }}>
            <thead>
              <tr style={{ textAlign: "right", borderBottom: "1px solid var(--border)" }}>
                <th style={{ padding: 8 }}>تاریخ</th>
                <th style={{ padding: 8 }}>منبع</th>
                <th style={{ padding: 8 }}>وضعیت</th>
                <th style={{ padding: 8 }}>حجم</th>
                <th style={{ padding: 8 }}>عملیات</th>
              </tr>
            </thead>
            <tbody>
              {backups.map((b) => (
                <tr key={b.id} style={{ borderBottom: "1px solid var(--border)" }}>
                  <td style={{ padding: 8 }}>{new Date(b.taken_at).toLocaleString("fa-IR")}</td>
                  <td style={{ padding: 8 }}>{SOURCE_LABELS[b.source]}</td>
                  <td style={{ padding: 8, color: b.status === "success" ? "var(--success)" : "var(--danger)" }}>
                    {STATUS_LABELS[b.status]}
                    {b.status === "failed" && b.error_message ? ` (${b.error_message})` : ""}
                  </td>
                  <td style={{ padding: 8 }}>{b.status === "success" ? formatSize(b.size_bytes) : "-"}</td>
                  <td style={{ padding: 8, display: "flex", gap: 8 }}>
                    {b.status === "success" && (
                      <>
                        <button className="btn" style={{ padding: "4px 10px", fontSize: 12 }} onClick={() => handleDownload(b)}>
                          دانلود
                        </button>
                        <button
                          className="btn"
                          style={{ padding: "4px 10px", fontSize: 12 }}
                          disabled={restoringId === b.id}
                          onClick={() => handleRestore(b)}
                        >
                          {restoringId === b.id ? "..." : "ریستور"}
                        </button>
                      </>
                    )}
                    <button
                      className="btn"
                      style={{ padding: "4px 10px", fontSize: 12, background: "var(--danger)" }}
                      onClick={() => handleDelete(b)}
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
