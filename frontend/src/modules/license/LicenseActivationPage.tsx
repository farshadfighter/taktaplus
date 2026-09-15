import { AlertCircle, CheckCircle2, KeyRound } from "lucide-react";
import { FormEvent, useState } from "react";

import { PageHeader } from "@/components/ui/PageHeader";
import { apiClient } from "@/services/apiClient";
import { LicenseStatus } from "@/modules/license/types";

export function LicenseActivationPage() {
  const [customerKey, setCustomerKey] = useState("");
  const [result, setResult] = useState<LicenseStatus | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);

  async function handleSubmit(event: FormEvent) {
    event.preventDefault();
    setError(null);
    setLoading(true);
    try {
      const { data } = await apiClient.post<LicenseStatus>("/license/activate", {
        customer_key: customerKey,
      });
      setResult(data);
    } catch (err: any) {
      setError(err?.response?.data?.detail ?? "فعال‌سازی لایسنس ناموفق بود");
    } finally {
      setLoading(false);
    }
  }

  return (
    <div>
      <PageHeader title="فعال‌سازی لایسنس" subtitle="کد لایسنسی که از ارائه‌دهنده نرم‌افزار دریافت کرده‌اید را وارد کنید." />
      <div className="card" style={{ maxWidth: 420 }}>
        <div className="card-title-row" style={{ marginBottom: 14 }}>
          <KeyRound size={16} />
          <span className="card-title">کد لایسنس</span>
        </div>
        <form onSubmit={handleSubmit}>
          <input
            className="input"
            placeholder="کد لایسنس (مثلاً DEMO-0001)"
            value={customerKey}
            onChange={(e) => setCustomerKey(e.target.value)}
          />
          <button className="btn" type="submit" disabled={loading}>
            {loading ? "در حال فعال‌سازی..." : "فعال‌سازی"}
          </button>
        </form>
        {error && (
          <div className="banner banner-danger" style={{ marginTop: 16, marginBottom: 0 }}>
            <AlertCircle size={16} />
            <span>{error}</span>
          </div>
        )}
        {result && (
          <div className="banner banner-success" style={{ marginTop: 16, marginBottom: 0 }}>
            <CheckCircle2 size={16} />
            <span>
              لایسنس با موفقیت فعال شد — سقف {result.fortigate_max_devices} دستگاه FortiGate،{" "}
              {result.fortiweb_max_devices} دستگاه FortiWeb، {result.sms2fa_max_users} کاربر 2FA.
            </span>
          </div>
        )}
      </div>
    </div>
  );
}
