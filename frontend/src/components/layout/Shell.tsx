import { Outlet, Link, useNavigate } from "react-router-dom";

import { LicenseStatusBanner } from "@/modules/license/LicenseStatusBanner";
import { useAuthStore } from "@/stores/authStore";

export function Shell() {
  const navigate = useNavigate();
  const { username, logout } = useAuthStore();

  function handleLogout() {
    logout();
    navigate("/login");
  }

  return (
    <div className="layout">
      <aside className="sidebar">
        <h3>taktaplus</h3>
        <nav>
          <Link to="/">داشبورد</Link>
          <Link to="/devices">دستگاه‌ها</Link>
          <Link to="/packages">سیگنیچر و فرم‌ور</Link>
          <Link to="/radius/users">کاربران احراز هویت پیامکی</Link>
          <Link to="/radius/clients">کلاینت‌های RADIUS</Link>
          <Link to="/radius/sms-gateway">سرویس پیامکی</Link>
          <Link to="/reports">گزارش وضعیت</Link>
          <Link to="/license">لایسنس</Link>
        </nav>
        <div style={{ marginTop: 24, fontSize: 12, color: "var(--text-muted)" }}>
          <div>{username}</div>
          <button className="btn" style={{ marginTop: 8, background: "var(--danger)" }} onClick={handleLogout}>
            خروج
          </button>
        </div>
      </aside>
      <main className="content">
        <LicenseStatusBanner />
        <Outlet />
      </main>
    </div>
  );
}
