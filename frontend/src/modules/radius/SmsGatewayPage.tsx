import { CheckCircle2, MessageSquareText, Send, Settings2, XCircle } from "lucide-react";
import { FormEvent, useEffect, useState } from "react";

import { LoadingState } from "@/components/ui/LoadingState";
import { PageHeader } from "@/components/ui/PageHeader";
import { apiClient } from "@/services/apiClient";
import { SmsGatewayConfig, SmsProvider } from "@/modules/radius/types";

const EMPTY_CONFIG: SmsGatewayConfig = {
  provider: "generic_http",
  kavenegar_sender: null,
  generic_method: "GET",
  generic_url_template: null,
  generic_auth_header_name: null,
  has_kavenegar_api_key: false,
  has_generic_auth_header_value: false,
};

export function SmsGatewayPage() {
  const [config, setConfig] = useState<SmsGatewayConfig>(EMPTY_CONFIG);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const [kavenegarApiKey, setKavenegarApiKey] = useState("");
  const [genericAuthHeaderValue, setGenericAuthHeaderValue] = useState("");

  const [testMobile, setTestMobile] = useState("");
  const [testing, setTesting] = useState(false);
  const [testResult, setTestResult] = useState<{ ok: boolean; message: string } | null>(null);

  async function load() {
    setLoading(true);
    try {
      const { data } = await apiClient.get<SmsGatewayConfig>("/radius/sms-gateway");
      setConfig(data);
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => {
    load();
  }, []);

  async function handleSave(event: FormEvent) {
    event.preventDefault();
    setError(null);
    setSaving(true);
    try {
      await apiClient.put("/radius/sms-gateway", {
        provider: config.provider,
        kavenegar_api_key: kavenegarApiKey || undefined,
        kavenegar_sender: config.kavenegar_sender,
        generic_method: config.generic_method,
        generic_url_template: config.generic_url_template,
        generic_auth_header_name: config.generic_auth_header_name,
        generic_auth_header_value: genericAuthHeaderValue || undefined,
      });
      setKavenegarApiKey("");
      setGenericAuthHeaderValue("");
      await load();
    } catch (err: any) {
      setError(err?.response?.data?.detail ?? "ذخیره تنظیمات ناموفق بود");
    } finally {
      setSaving(false);
    }
  }

  async function handleTest(event: FormEvent) {
    event.preventDefault();
    setTestResult(null);
    setTesting(true);
    try {
      await apiClient.post("/radius/sms-gateway/test", { mobile_number: testMobile });
      setTestResult({ ok: true, message: "پیامک آزمایشی با موفقیت ارسال شد" });
    } catch (err: any) {
      setTestResult({ ok: false, message: err?.response?.data?.detail ?? "ارسال پیامک آزمایشی ناموفق بود" });
    } finally {
      setTesting(false);
    }
  }

  if (loading) {
    return (
      <div>
        <PageHeader title="سرویس پیامکی" />
        <div className="card">
          <LoadingState />
        </div>
      </div>
    );
  }

  return (
    <div>
      <PageHeader
        title="سرویس پیامکی"
        subtitle="برخلاف منبع FTP سیگنیچر، taktaplus هیچ حساب پیامکی پیش‌فرضی ندارد - هر نصب باید سرویس پیامکی خودش را اینجا معرفی کند."
      />

      <div className="card-grid">
        <form className="card" onSubmit={handleSave}>
          <div className="card-title-row" style={{ marginBottom: 14 }}>
            <Settings2 size={16} />
            <span className="card-title">پیکربندی</span>
          </div>
          {error && <div className="banner banner-danger">{error}</div>}
          <label>ارائه‌دهنده</label>
          <select
            className="input"
            value={config.provider}
            onChange={(e) => setConfig({ ...config, provider: e.target.value as SmsProvider })}
          >
            <option value="kavenegar">کاوه‌نگار</option>
            <option value="generic_http">سرویس سفارشی (HTTP)</option>
          </select>

          {config.provider === "kavenegar" ? (
            <>
              <label>کلید API {config.has_kavenegar_api_key && "(از قبل تنظیم شده - برای تغییر پر کنید)"}</label>
              <input className="input" value={kavenegarApiKey} onChange={(e) => setKavenegarApiKey(e.target.value)} />
              <label>نام فرستنده (اختیاری)</label>
              <input
                className="input"
                value={config.kavenegar_sender ?? ""}
                onChange={(e) => setConfig({ ...config, kavenegar_sender: e.target.value })}
              />
            </>
          ) : (
            <>
              <label>متد</label>
              <select
                className="input"
                value={config.generic_method}
                onChange={(e) => setConfig({ ...config, generic_method: e.target.value })}
              >
                <option value="GET">GET</option>
                <option value="POST">POST</option>
              </select>
              <label>الگوی آدرس (شامل {"{mobile}"} و {"{code}"})</label>
              <input
                className="input"
                value={config.generic_url_template ?? ""}
                onChange={(e) => setConfig({ ...config, generic_url_template: e.target.value })}
                placeholder="https://example.com/send?to={mobile}&text={code}"
              />
              <label>نام هدر احراز هویت (اختیاری)</label>
              <input
                className="input"
                value={config.generic_auth_header_name ?? ""}
                onChange={(e) => setConfig({ ...config, generic_auth_header_name: e.target.value })}
              />
              <label>
                مقدار هدر احراز هویت {config.has_generic_auth_header_value && "(از قبل تنظیم شده - برای تغییر پر کنید)"}
              </label>
              <input className="input" value={genericAuthHeaderValue} onChange={(e) => setGenericAuthHeaderValue(e.target.value)} />
            </>
          )}

          <button className="btn" type="submit" disabled={saving}>
            {saving ? "در حال ذخیره..." : "ذخیره"}
          </button>
        </form>

        <form className="card" onSubmit={handleTest} style={{ alignSelf: "flex-start" }}>
          <div className="card-title-row" style={{ marginBottom: 14 }}>
            <MessageSquareText size={16} />
            <span className="card-title">ارسال پیامک آزمایشی</span>
          </div>
          <label>شماره موبایل</label>
          <input className="input" value={testMobile} onChange={(e) => setTestMobile(e.target.value)} required />
          <button className="btn" type="submit" disabled={testing}>
            <Send size={14} />
            {testing ? "در حال ارسال..." : "ارسال آزمایشی"}
          </button>
          {testResult && (
            <div className={`banner ${testResult.ok ? "banner-success" : "banner-danger"}`} style={{ marginTop: 14, marginBottom: 0 }}>
              {testResult.ok ? <CheckCircle2 size={16} /> : <XCircle size={16} />}
              <span>{testResult.message}</span>
            </div>
          )}
        </form>
      </div>
    </div>
  );
}
