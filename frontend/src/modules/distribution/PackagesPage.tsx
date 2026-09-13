import { FormEvent, useEffect, useState } from "react";

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
      <h2 style={{ marginTop: 0 }}>سیگنیچر و فرم‌ور</h2>

      <form className="card" style={{ marginBottom: 16, maxWidth: 480 }} onSubmit={handleSaveFtp}>
        <h3 style={{ marginTop: 0 }}>منبع FTP</h3>
        <label style={{ display: "flex", alignItems: "center", gap: 8, marginBottom: 12 }}>
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
        <button className="btn" type="submit" disabled={savingFtp}>
          {savingFtp ? "در حال ذخیره..." : "ذخیره"}
        </button>
        <button type="button" className="btn" style={{ marginInlineStart: 8 }} onClick={handleSyncNow} disabled={syncing}>
          {syncing ? "در حال همگام‌سازی..." : "همگام‌سازی الان"}
        </button>
        {syncResult && <p style={{ fontSize: 13, marginTop: 8 }}>{syncResult}</p>}
      </form>

      <form className="card" style={{ marginBottom: 16, maxWidth: 480 }} onSubmit={handleUpload}>
        <h3 style={{ marginTop: 0 }}>آپلود دستی بسته</h3>
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

      <div className="card">
        <h3 style={{ marginTop: 0 }}>بسته‌های موجود</h3>
        {loading ? (
          <p>در حال بارگذاری...</p>
        ) : packages.length === 0 ? (
          <p>هنوز بسته‌ای اضافه نشده است.</p>
        ) : (
          <table style={{ width: "100%", borderCollapse: "collapse" }}>
            <thead>
              <tr style={{ textAlign: "right", borderBottom: "1px solid var(--border)" }}>
                <th style={{ padding: 8 }}>فایل</th>
                <th style={{ padding: 8 }}>نوع تجهیز</th>
                <th style={{ padding: 8 }}>نوع بسته</th>
                <th style={{ padding: 8 }}>حجم</th>
                <th style={{ padding: 8 }}>منبع</th>
                <th style={{ padding: 8 }}>عملیات</th>
              </tr>
            </thead>
            <tbody>
              {packages.map((p) => (
                <tr key={p.id} style={{ borderBottom: "1px solid var(--border)" }}>
                  <td style={{ padding: 8 }}>{p.filename}</td>
                  <td style={{ padding: 8 }}>{VENDOR_LABELS[p.vendor_type]}</td>
                  <td style={{ padding: 8 }}>{p.package_type}</td>
                  <td style={{ padding: 8 }}>{formatSize(p.size_bytes)}</td>
                  <td style={{ padding: 8 }}>{p.source === "ftp_sync" ? "FTP" : "آپلود دستی"}</td>
                  <td style={{ padding: 8 }}>
                    <button className="btn" style={{ padding: "4px 10px", fontSize: 12, background: "var(--danger)" }} onClick={() => handleDelete(p)}>
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
