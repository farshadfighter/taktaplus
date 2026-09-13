import type { ReactElement } from "react";
import { createBrowserRouter, Navigate } from "react-router-dom";

import { Shell } from "@/components/layout/Shell";
import { LoginPage } from "@/modules/auth/LoginPage";
import { DeviceBackupsPage } from "@/modules/backups/DeviceBackupsPage";
import { DashboardPage } from "@/modules/dashboard/DashboardPage";
import { DevicePushPage } from "@/modules/distribution/DevicePushPage";
import { PackagesPage } from "@/modules/distribution/PackagesPage";
import { DevicesPage } from "@/modules/devices/DevicesPage";
import { LicenseActivationPage } from "@/modules/license/LicenseActivationPage";
import { DeviceMonitoringPage } from "@/modules/monitoring/DeviceMonitoringPage";
import { FleetStatusPage } from "@/modules/reports/FleetStatusPage";
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
      { path: "devices", element: <DevicesPage /> },
      { path: "devices/:deviceId/backups", element: <DeviceBackupsPage /> },
      { path: "devices/:deviceId/monitoring", element: <DeviceMonitoringPage /> },
      { path: "devices/:deviceId/push", element: <DevicePushPage /> },
      { path: "packages", element: <PackagesPage /> },
      { path: "reports", element: <FleetStatusPage /> },
      { path: "license", element: <LicenseActivationPage /> },
    ],
  },
]);
