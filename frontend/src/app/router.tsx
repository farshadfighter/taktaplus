import type { ReactElement } from "react";
import { createBrowserRouter, Navigate } from "react-router-dom";

import { Shell } from "@/components/layout/Shell";
import { LoginPage } from "@/modules/auth/LoginPage";
import { DashboardPage } from "@/modules/dashboard/DashboardPage";
import { LicenseActivationPage } from "@/modules/license/LicenseActivationPage";
import { useAuthStore } from "@/stores/authStore";

function RequireAuth({ children }: { children: ReactElement }) {
  const accessToken = useAuthStore((s) => s.accessToken);
  if (!accessToken) return <Navigate to="/login" replace />;
  return children;
}

export const router = createBrowserRouter([
  { path: "/login", element: <LoginPage /> },
  {
    path: "/",
    element: (
      <RequireAuth>
        <Shell />
      </RequireAuth>
    ),
    children: [
      { index: true, element: <DashboardPage /> },
      { path: "license", element: <LicenseActivationPage /> },
    ],
  },
]);
