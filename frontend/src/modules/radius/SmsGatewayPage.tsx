import { FormEvent, useEffect, useState } from "react";

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
  const [testResult, setTestResult] = useState<string | null>(null);

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
      setTestResult("پیامک آزمایشی با موفقیت ارسال شد");
    } catch (err: any) {
      setTestResult(err?.response?.data?.detail ?? "ارسال پیامک آزمایشی ناموفق بود");
    } finally {
      setTesting(false);
    }
  }

  if (loading) return <p>در حال بارگذاری...</p>;

  return (
    <div>
      <h2 style={{ marginTop: 0 }}>سرویس پیامکی</h2>
      <p style={{ fontSize: 13, color: "var(--text-muted)", maxWidth: 640 }}>
        برخلاف منبع FTP سیگنیچر، taktaplus هیچ حساب پیامکی پیش‌فرضی ندارد - هر نصب باید سرویس
        پیامکی خودش را اینجا معرفی کند.
      </p>

      <form className="card" style={{ marginBottom: 16, maxWidth: 480 }} onSubmit={handleSave}>
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

      <form className="card" style={{ maxWidth: 480 }} onSubmit={handleTest}>
        <h3 style={{ marginTop: 0 }}>ارسال پیامک آزمایشی</h3>
        <label>شماره موبایل</label>
        <input className="input" value={testMobile} onChange={(e) => setTestMobile(e.target.value)} required />
        <button className="btn" type="submit" disabled={testing}>
          {testing ? "در حال ارسال..." : "ارسال آزمایشی"}
        </button>
        {testResult && <p style={{ fontSize: 13, marginTop: 8 }}>{testResult}</p>}
      </form>
    </div>
  );
}
