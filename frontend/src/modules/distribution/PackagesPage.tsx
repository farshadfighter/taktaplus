import { CloudDownload, PackageOpen, Trash2, UploadCloud } from "lucide-react";
import { FormEvent, useEffect, useState } from "react";

import { Badge } from "@/components/ui/Badge";
import { EmptyState } from "@/components/ui/EmptyState";
import { LoadingState } from "@/components/ui/LoadingState";
import { PageHeader } from "@/components/ui/PageHeader";
import { apiClient } from "@/services/apiClient";
import { VendorType } from "@/modules/devices/types";
import { FtpSourceConfig, Package } from "@/modules/distribution/types";

const VENDOR_LABELS: Record<VendorType, string> = {
  fortigate: "FortiGate",
  fortiweb: "FortiWeb",
};

function formatSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / 1024 / 1024).toFixed(1)} MB`;
}

export function PackagesPage() {
  const [packages, setPackages] = useState<Package[]>([]);
  const [loading, setLoading] = useState(true);

  const [ftpConfig, setFtpConfig] = useState<FtpSourceConfig>({
    use_custom: false,
    host: "",
    port: 21,
    username: "",
    remote_path: "/",
  });
  const [ftpPassword, setFtpPassword] = useState("");
  const [savingFtp, setSavingFtp] = useState(false);
  const [syncing, setSyncing] = useState(false);
  const [syncResult, setSyncResult] = useState<string | null>(null);

  const [uploadVendor, setUploadVendor] = useState<VendorType>("fortigate");
  const [uploadType, setUploadType] = useState("ips");
  const [uploadFile, setUploadFile] = useState<File | null>(null);
  const [uploading, setUploading] = useState(false);
  const [uploadError, setUploadError] = useState<string | null>(null);

  async function load() {
    setLoading(true);
    try {
      const [pkgRes, ftpRes] = await Promise.all([
        apiClient.get<Package[]>("/distribution/packages"),
        apiClient.get<FtpSourceConfig>("/distribution/ftp-source"),
      ]);
      setPackages(pkgRes.data);
      setFtpConfig(ftpRes.data);
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => {
    load();
  }, []);

  async function handleSaveFtp(event: FormEvent) {
    event.preventDefault();
    setSavingFtp(true);
    try {
      await apiClient.put("/distribution/ftp-source", { ...ftpConfig, password: ftpPassword || undefined });
      setFtpPassword("");
      await load();
    } finally {
      setSavingFtp(false);
    }
  }

  async function handleSyncNow() {
    setSyncing(true);
    setSyncResult(null);
    try {
      const { data } = await apiClient.post("/distribution/ftp-source/sync-now");
      setSyncResult(`${data.imported} بسته جدید وارد شد، ${data.skipped} بسته قبلاً موجود بود`);
      await load();
    } catch (err: any) {
      setSyncResult(err?.response?.data?.detail ?? "همگام‌سازی ناموفق بود");
    } finally {
      setSyncing(false);
    }
  }

  async function handleUpload(event: FormEvent) {
    event.preventDefault();
    if (!uploadFile) return;
    setUploadError(null);
    setUploading(true);
    try {
      const form = new FormData();
      form.append("vendor_type", uploadVendor);
      form.append("package_type", uploadType);
      form.append("file", uploadFile);
      await apiClient.post("/distribution/packages/upload", form, {
        headers: { "Content-Type": "multipart/form-data" },
      });
      setUploadFile(null);
      await load();
    } catch (err: any) {
      setUploadError(err?.response?.data?.detail ?? "آپلود ناموفق بود");
    } finally {
      setUploading(false);
    }
  }

  async function handleDelete(pkg: Package) {
    if (!confirm(`بسته «${pkg.filename}» حذف شود؟`)) return;
    await apiClient.delete(`/distribution/packages/${pkg.id}`);
    await load();
  }

  return (
    <div>
      <PageHeader title="سیگنیچر و فرم‌ور" subtitle="مدیریت منبع FTP و بسته‌های آفلاین سیگنیچر/فرم‌ور." />

      <div className="card-grid" style={{ marginBottom: 16 }}>
        <form className="card" onSubmit={handleSaveFtp}>
          <div className="card-title-row" style={{ marginBottom: 14 }}>
            <CloudDownload size={16} />
            <span className="card-title">منبع FTP</span>
          </div>
          <label className="checkbox-row">
            <input
              type="checkbox"
              checked={ftpConfig.use_custom}
              onChange={(e) => setFtpConfig({ ...ftpConfig, use_custom: e.target.checked })}
            />
            استفاده از FTP سفارشی (به‌جای FTP پیش‌فرض ارائه‌دهنده)
          </label>
          {ftpConfig.use_custom && (
            <>
              <label>آدرس سرور</label>
              <input className="input" value={ftpConfig.host} onChange={(e) => setFtpConfig({ ...ftpConfig, host: e.target.value })} />
              <label>پورت</label>
              <input
                className="input"
                type="number"
                value={ftpConfig.port}
                onChange={(e) => setFtpConfig({ ...ftpConfig, port: Number(e.target.value) })}
              />
              <label>نام کاربری</label>
              <input
                className="input"
                value={ftpConfig.username}
                onChange={(e) => setFtpConfig({ ...ftpConfig, username: e.target.value })}
              />
              <label>رمز عبور</label>
              <input className="input" type="password" value={ftpPassword} onChange={(e) => setFtpPassword(e.target.value)} />
              <label>مسیر</label>
              <input
                className="input"
                value={ftpConfig.remote_path}
                onChange={(e) => setFtpConfig({ ...ftpConfig, remote_path: e.target.value })}
              />
            </>
          )}
          <div className="form-actions">
            <button className="btn" type="submit" disabled={savingFtp}>
              {savingFtp ? "در حال ذخیره..." : "ذخیره"}
            </button>
            <button type="button" className="btn btn-secondary" onClick={handleSyncNow} disabled={syncing}>
              {syncing ? "در حال همگام‌سازی..." : "همگام‌سازی الان"}
            </button>
          </div>
          {syncResult && <p style={{ fontSize: 12.5, marginTop: 12, color: "var(--text-muted)" }}>{syncResult}</p>}
        </form>

        <form className="card" onSubmit={handleUpload}>
          <div className="card-title-row" style={{ marginBottom: 14 }}>
            <UploadCloud size={16} />
            <span className="card-title">آپلود دستی بسته</span>
          </div>
          {uploadError && <div className="banner banner-danger">{uploadError}</div>}
          <label>نوع تجهیز</label>
          <select className="input" value={uploadVendor} onChange={(e) => setUploadVendor(e.target.value as VendorType)}>
            <option value="fortigate">FortiGate</option>
            <option value="fortiweb">FortiWeb</option>
          </select>
          <label>نوع بسته (مثلاً ips, av, firmware)</label>
          <input className="input" value={uploadType} onChange={(e) => setUploadType(e.target.value)} />
          <label>فایل</label>
          <input className="input" type="file" onChange={(e) => setUploadFile(e.target.files?.[0] ?? null)} />
          <button className="btn" type="submit" disabled={uploading || !uploadFile}>
            {uploading ? "در حال آپلود..." : "آپلود"}
          </button>
        </form>
      </div>

      <div className="card">
        <div className="card-title" style={{ marginBottom: 14 }}>بسته‌های موجود</div>
        {loading ? (
          <LoadingState />
        ) : packages.length === 0 ? (
          <EmptyState icon={<PackageOpen size={32} />} title="هنوز بسته‌ای اضافه نشده است" />
        ) : (
          <div className="table-wrap">
            <table className="data-table">
              <thead>
                <tr>
                  <th>فایل</th>
                  <th>نوع تجهیز</th>
                  <th>نوع بسته</th>
                  <th>حجم</th>
                  <th>منبع</th>
                  <th>عملیات</th>
                </tr>
              </thead>
              <tbody>
                {packages.map((p) => (
                  <tr key={p.id}>
                    <td className="cell-primary">{p.filename}</td>
                    <td>{VENDOR_LABELS[p.vendor_type]}</td>
                    <td className="cell-muted">{p.package_type}</td>
                    <td className="cell-muted">{formatSize(p.size_bytes)}</td>
                    <td>
                      <Badge variant={p.source === "ftp_sync" ? "info" : "neutral"}>
                        {p.source === "ftp_sync" ? "FTP" : "آپلود دستی"}
                      </Badge>
                    </td>
                    <td>
                      <button className="btn btn-danger-ghost btn-sm btn-icon" onClick={() => handleDelete(p)} title="حذف">
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
