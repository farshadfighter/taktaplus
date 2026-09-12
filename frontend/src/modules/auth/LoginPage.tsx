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
      setAuth(data.access_token, username);
      navigate("/");
    } catch {
      setError("نام کاربری یا رمز عبور اشتباه است");
    } finally {
      setLoading(false);
    }
  }

  return (
    <div style={{ display: "flex", justifyContent: "center", alignItems: "center", height: "100%" }}>
      <form className="card" style={{ width: 320 }} onSubmit={handleSubmit}>
        <h2>ورود به taktaplus</h2>
        {error && <div className="banner banner-danger">{error}</div>}
        <input
          className="input"
          placeholder="نام کاربری"
          value={username}
          onChange={(e) => setUsername(e.target.value)}
          autoFocus
        />
        <input
          className="input"
          placeholder="رمز عبور"
          type="password"
          value={password}
          onChange={(e) => setPassword(e.target.value)}
        />
        <button className="btn" type="submit" disabled={loading} style={{ width: "100%" }}>
          {loading ? "در حال ورود..." : "ورود"}
        </button>
      </form>
    </div>
  );
}
