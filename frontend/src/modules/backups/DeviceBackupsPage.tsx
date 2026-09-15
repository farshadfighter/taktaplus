import { Archive, ArrowRight, Database, Download, RotateCcw, Trash2 } from "lucide-react";
import { useEffect, useState } from "react";
import { useParams, Link } from "react-router-dom";

import { Badge } from "@/components/ui/Badge";
import { EmptyState } from "@/components/ui/EmptyState";
import { LoadingState } from "@/components/ui/LoadingState";
import { PageHeader } from "@/components/ui/PageHeader";
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
      <Link to="/devices" className="back-link">
        <ArrowRight size={14} />
        بازگشت به دستگاه‌ها
      </Link>
      <PageHeader
        title={`بکاپ‌های ${device?.name ?? "..."}`}
        subtitle="بکاپ‌گیری دستی یا زمان‌بندی‌شده، دانلود و ریستور پیکربندی."
        actions={
          <button className="btn" onClick={handleCreateBackup} disabled={taking}>
            <Database size={15} />
            {taking ? "در حال بکاپ‌گیری..." : "بکاپ‌گیری الان"}
          </button>
        }
      />

      <div className="card">
        {loading ? (
          <LoadingState />
        ) : backups.length === 0 ? (
          <EmptyState
            icon={<Archive size={32} />}
            title="هنوز بکاپی گرفته نشده است"
            description="با دکمه «بکاپ‌گیری الان» اولین نسخه از پیکربندی این دستگاه را بگیرید."
          />
        ) : (
          <div className="table-wrap">
            <table className="data-table">
              <thead>
                <tr>
                  <th>تاریخ</th>
                  <th>منبع</th>
                  <th>وضعیت</th>
                  <th>حجم</th>
                  <th>عملیات</th>
                </tr>
              </thead>
              <tbody>
                {backups.map((b) => (
                  <tr key={b.id}>
                    <td className="cell-primary">{new Date(b.taken_at).toLocaleString("fa-IR")}</td>
                    <td className="cell-muted">{SOURCE_LABELS[b.source]}</td>
                    <td>
                      <Badge variant={b.status === "success" ? "success" : "danger"}>{STATUS_LABELS[b.status]}</Badge>
                      {b.status === "failed" && b.error_message && (
                        <div style={{ fontSize: 11, color: "var(--text-faint)", marginTop: 4 }}>{b.error_message}</div>
                      )}
                    </td>
                    <td className="cell-muted">{b.status === "success" ? formatSize(b.size_bytes) : "-"}</td>
                    <td>
                      <div className="row-actions">
                        {b.status === "success" && (
                          <>
                            <button className="btn btn-secondary btn-sm" onClick={() => handleDownload(b)}>
                              <Download size={13} />
                              دانلود
                            </button>
                            <button
                              className="btn btn-secondary btn-sm"
                              disabled={restoringId === b.id}
                              onClick={() => handleRestore(b)}
                            >
                              <RotateCcw size={13} />
                              {restoringId === b.id ? "..." : "ریستور"}
                            </button>
                          </>
                        )}
                        <button className="btn btn-danger-ghost btn-sm btn-icon" onClick={() => handleDelete(b)} title="حذف">
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
