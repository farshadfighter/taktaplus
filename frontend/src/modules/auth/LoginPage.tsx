import { AlertCircle, Lock, ShieldCheck, User } from "lucide-react";
import { FormEvent, useState } from "react";
import { useNavigate } from "react-router-dom";

import { apiClient } from "@/services/apiClient";
import { useAuthStore } from "@/stores/authStore";

export function LoginPage() {
  const navigate = useNavigate();
  const setAuth = useAuthStore((s) => s.setAuth);
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);

  async function handleSubmit(event: FormEvent) {
    event.preventDefault();
    setError(null);
    setLoading(true);
    try {
      const { data } = await apiClient.post("/auth/login", { username, password });
      setAuth(data.access_token, data.refresh_token, username);
      navigate("/");
    } catch {
      setError("نام کاربری یا رمز عبور اشتباه است");
    } finally {
      setLoading(false);
    }
  }

  return (
    <div
      style={{
        display: "flex",
        justifyContent: "center",
        alignItems: "center",
        height: "100%",
        background: "linear-gradient(160deg, #14162b 0%, #1e2049 45%, #3730a3 100%)",
        padding: 20,
      }}
    >
      <div style={{ width: "100%", maxWidth: 380 }}>
        <div style={{ display: "flex", flexDirection: "column", alignItems: "center", marginBottom: 22 }}>
          <div
            style={{
              width: 52,
              height: 52,
              borderRadius: 14,
              background: "linear-gradient(135deg, #4f46e5, #7c73f0)",
              display: "flex",
              alignItems: "center",
              justifyContent: "center",
              boxShadow: "0 8px 24px rgba(79,70,229,0.5)",
              marginBottom: 12,
            }}
          >
            <ShieldCheck color="#fff" size={26} />
          </div>
          <div style={{ color: "#fff", fontWeight: 800, fontSize: 19 }}>taktaplus</div>
          <div style={{ color: "#a3a6d1", fontSize: 12.5, marginTop: 2 }}>پلتفرم مدیریت متمرکز Fortinet</div>
        </div>

        <form className="card" style={{ boxShadow: "var(--shadow-lg)" }} onSubmit={handleSubmit}>
          <h2 style={{ fontSize: 16, marginBottom: 4 }}>ورود به حساب</h2>
          <p style={{ fontSize: 12.5, color: "var(--text-muted)", marginTop: 0, marginBottom: 18 }}>
            برای دسترسی به پنل مدیریت وارد شوید.
          </p>

          {error && (
            <div className="banner banner-danger">
              <AlertCircle size={16} />
              <span>{error}</span>
            </div>
          )}

          <label>نام کاربری</label>
          <div style={{ position: "relative" }}>
            <User size={15} style={{ position: "absolute", insetInlineStart: 12, top: 12, color: "var(--text-faint)" }} />
            <input
              className="input"
              style={{ paddingInlineStart: 34 }}
              placeholder="مثلاً admin"
              value={username}
              onChange={(e) => setUsername(e.target.value)}
              autoFocus
            />
          </div>

          <label>رمز عبور</label>
          <div style={{ position: "relative" }}>
            <Lock size={15} style={{ position: "absolute", insetInlineStart: 12, top: 12, color: "var(--text-faint)" }} />
            <input
              className="input"
              style={{ paddingInlineStart: 34 }}
              placeholder="••••••••"
              type="password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
            />
          </div>

          <button className="btn btn-block" type="submit" disabled={loading} style={{ marginTop: 4 }}>
            {loading ? "در حال ورود..." : "ورود"}
          </button>
        </form>
      </div>
    </div>
  );
}
