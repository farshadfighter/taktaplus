import { ArrowRight, History, PackageOpen, Send, Terminal } from "lucide-react";
import { FormEvent, useEffect, useState } from "react";
import { Link, useParams } from "react-router-dom";

import { Badge } from "@/components/ui/Badge";
import { EmptyState } from "@/components/ui/EmptyState";
import { LoadingState } from "@/components/ui/LoadingState";
import { PageHeader } from "@/components/ui/PageHeader";
import { apiClient } from "@/services/apiClient";
import { Device } from "@/modules/devices/types";
import { Package, PushRecord } from "@/modules/distribution/types";

export function DevicePushPage() {
  const { deviceId } = useParams<{ deviceId: string }>();
  const [device, setDevice] = useState<Device | null>(null);
  const [packages, setPackages] = useState<Package[]>([]);
  const [history, setHistory] = useState<PushRecord[]>([]);
  const [loading, setLoading] = useState(true);
  const [pushingId, setPushingId] = useState<string | null>(null);

  const [sshPort, setSshPort] = useState(22);
  const [sshUsername, setSshUsername] = useState("");
  const [sshPassword, setSshPassword] = useState("");
  const [savingSsh, setSavingSsh] = useState(false);

  async function load() {
    setLoading(true);
    try {
      const deviceRes = await apiClient.get<Device>(`/devices/${deviceId}`);
      setDevice(deviceRes.data);
      setSshPort(deviceRes.data.ssh_port);
      setSshUsername(deviceRes.data.ssh_username ?? "");

      const [pkgRes, historyRes] = await Promise.all([
        apiClient.get<Package[]>("/distribution/packages", { params: { vendor_type: deviceRes.data.vendor_type } }),
        apiClient.get<PushRecord[]>(`/devices/${deviceId}/push-history`),
      ]);
      setPackages(pkgRes.data);
      setHistory(historyRes.data);
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => {
    load();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [deviceId]);

  async function handleSaveSsh(event: FormEvent) {
    event.preventDefault();
    setSavingSsh(true);
    try {
      await apiClient.patch(`/devices/${deviceId}/ssh`, { port: sshPort, username: sshUsername, password: sshPassword });
      setSshPassword("");
      await load();
    } finally {
      setSavingSsh(false);
    }
  }

  async function handlePush(pkg: Package) {
    if (!confirm(`بسته «${pkg.filename}» روی این دستگاه پوش شود؟`)) return;
    setPushingId(pkg.id);
    try {
      await apiClient.post(`/devices/${deviceId}/push/${pkg.id}`);
      await load();
    } catch (err: any) {
      alert(err?.response?.data?.detail ?? "پوش ناموفق بود");
    } finally {
      setPushingId(null);
    }
  }

  return (
    <div>
      <Link to="/devices" className="back-link">
        <ArrowRight size={14} />
        بازگشت به دستگاه‌ها
      </Link>
      <PageHeader title={`توزیع سیگنیچر/فرم‌ور - ${device?.name ?? "..."}`} subtitle="پوش بسته‌های سیگنیچر و فرم‌ور به این دستگاه." />

      {device?.vendor_type === "fortigate" && (
        <form className="card" style={{ marginBottom: 16, maxWidth: 420 }} onSubmit={handleSaveSsh}>
          <div className="card-title-row" style={{ marginBottom: 6 }}>
            <Terminal size={16} />
            <span className="card-title">تنظیمات SSH (لازم برای پوش سیگنیچر)</span>
          </div>
          <p className="card-description" style={{ marginBottom: 14 }}>
            چون فورتی‌گیت برای دستور بازیابی سیگنیچر معادل REST API ندارد، این عملیات از طریق SSH انجام می‌شود.
          </p>
          <label>پورت</label>
          <input className="input" type="number" value={sshPort} onChange={(e) => setSshPort(Number(e.target.value))} />
          <label>نام کاربری</label>
          <input className="input" value={sshUsername} onChange={(e) => setSshUsername(e.target.value)} />
          <label>رمز عبور</label>
          <input className="input" type="password" value={sshPassword} onChange={(e) => setSshPassword(e.target.value)} placeholder="برای تغییر وارد کنید" />
          <button className="btn" type="submit" disabled={savingSsh}>
            {savingSsh ? "در حال ذخیره..." : "ذخیره"}
          </button>
        </form>
      )}

      <div className="card" style={{ marginBottom: 16 }}>
        <div className="card-title-row" style={{ marginBottom: 14 }}>
          <PackageOpen size={16} />
          <span className="card-title">بسته‌های قابل‌پوش</span>
        </div>
        {loading ? (
          <LoadingState />
        ) : packages.length === 0 ? (
          <EmptyState
            icon={<PackageOpen size={28} />}
            title="بسته‌ای برای این نوع تجهیز موجود نیست"
            description="از صفحه «سیگنیچر و فرم‌ور» بسته موردنظر را اضافه کنید."
          />
        ) : (
          <div className="table-wrap">
            <table className="data-table">
              <thead>
                <tr>
                  <th>فایل</th>
                  <th>نوع بسته</th>
                  <th>عملیات</th>
                </tr>
              </thead>
              <tbody>
                {packages.map((p) => (
                  <tr key={p.id}>
                    <td className="cell-primary">{p.filename}</td>
                    <td className="cell-muted">{p.package_type}</td>
                    <td>
                      <button className="btn btn-secondary btn-sm" disabled={pushingId === p.id} onClick={() => handlePush(p)}>
                        <Send size={13} />
                        {pushingId === p.id ? "در حال پوش..." : "پوش به این دستگاه"}
                      </button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>

      <div className="card">
        <div className="card-title-row" style={{ marginBottom: 14 }}>
          <History size={16} />
          <span className="card-title">تاریخچه پوش</span>
        </div>
        {history.length === 0 ? (
          <EmptyState icon={<History size={28} />} title="هنوز پوشی انجام نشده است" />
        ) : (
          <div className="table-wrap">
            <table className="data-table">
              <thead>
                <tr>
                  <th>زمان</th>
                  <th>وضعیت</th>
                  <th>سلامت پس از پوش</th>
                  <th>خطا</th>
                </tr>
              </thead>
              <tbody>
                {history.map((h) => (
                  <tr key={h.id}>
                    <td className="cell-muted">{new Date(h.pushed_at).toLocaleString("fa-IR")}</td>
                    <td>
                      <Badge variant={h.status === "success" ? "success" : "danger"}>
                        {h.status === "success" ? "موفق" : "ناموفق"}
                      </Badge>
                    </td>
                    <td>
                      {h.post_push_check_ok === null ? (
                        <span className="cell-muted">-</span>
                      ) : h.post_push_check_ok ? (
                        <Badge variant="success">سالم</Badge>
                      ) : (
                        <Badge variant="warning">نامشخص/خطا</Badge>
                      )}
                    </td>
                    <td className="cell-muted">{h.error_message || "-"}</td>
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
