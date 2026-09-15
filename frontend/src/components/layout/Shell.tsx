import {
  BarChart3,
  KeyRound,
  LayoutDashboard,
  LogOut,
  MessageSquareText,
  PackageOpen,
  Radio,
  ShieldCheck,
  Server,
} from "lucide-react";
import { NavLink, Outlet, useNavigate } from "react-router-dom";

import { LicenseStatusBanner } from "@/modules/license/LicenseStatusBanner";
import { useAuthStore } from "@/stores/authStore";

interface NavItem {
  to: string;
  label: string;
  icon: typeof LayoutDashboard;
  end?: boolean;
}

interface NavGroup {
  label?: string;
  items: NavItem[];
}

const NAV_GROUPS: NavGroup[] = [
  {
    items: [
      { to: "/", label: "داشبورد", icon: LayoutDashboard, end: true },
      { to: "/devices", label: "دستگاه‌ها", icon: Server },
      { to: "/reports", label: "گزارش وضعیت فلیت", icon: BarChart3 },
    ],
  },
  {
    label: "امنیت و احراز هویت",
    items: [
      { to: "/radius/users", label: "کاربران احراز هویت پیامکی", icon: KeyRound },
      { to: "/radius/clients", label: "کلاینت‌های RADIUS", icon: Radio },
      { to: "/radius/sms-gateway", label: "سرویس پیامکی", icon: MessageSquareText },
    ],
  },
  {
    label: "توزیع",
    items: [{ to: "/packages", label: "سیگنیچر و فرم‌ور", icon: PackageOpen }],
  },
  {
    label: "سیستم",
    items: [{ to: "/license", label: "لایسنس", icon: ShieldCheck }],
  },
];

function initialsOf(name: string | null): string {
  if (!name) return "?";
  return name.trim().slice(0, 2).toUpperCase();
}

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
        <div className="sidebar-brand">
          <div className="sidebar-brand-mark">tp</div>
          <div>
            <div className="sidebar-brand-text">taktaplus</div>
            <div className="sidebar-brand-sub">مدیریت متمرکز Fortinet</div>
          </div>
        </div>

        {NAV_GROUPS.map((group, index) => (
          <div key={group.label ?? `group-${index}`}>
            {group.label && <div className="sidebar-group-label">{group.label}</div>}
            <nav>
              {group.items.map((item) => (
                <NavLink
                  key={item.to}
                  to={item.to}
                  end={item.end}
                  className={({ isActive }) => `sidebar-link${isActive ? " active" : ""}`}
                >
                  <item.icon size={17} />
                  {item.label}
                </NavLink>
              ))}
            </nav>
          </div>
        ))}

        <div className="sidebar-footer">
          <div className="sidebar-avatar">{initialsOf(username)}</div>
          <div className="sidebar-username">{username}</div>
          <button className="sidebar-logout" onClick={handleLogout} title="خروج">
            <LogOut size={16} />
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
