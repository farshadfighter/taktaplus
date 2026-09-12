import { useEffect, useState } from "react";
import { Link } from "react-router-dom";

import { apiClient } from "@/services/apiClient";
import { LicenseStatus } from "@/modules/license/types";

export function LicenseStatusBanner() {
  const [license, setLicense] = useState<LicenseStatus | null>(null);

  useEffect(() => {
    apiClient
      .get<LicenseStatus>("/license/status")
      .then((res) => setLicense(res.data))
      .catch(() => setLicense(null));
  }, []);

  if (!license) return null;

  if (license.status === "unactivated") {
    return (
      <div className="banner banner-warning">
        لایسنس taktaplus فعال نشده است. <Link to="/license">فعال‌سازی لایسنس</Link>
      </div>
    );
  }

  if (license.status === "expired" || license.status === "suspended") {
    return (
      <div className="banner banner-danger">
        لایسنس {license.status === "expired" ? "منقضی شده" : "معلق شده"} است. برای تمدید با پشتیبانی تماس بگیرید.
      </div>
    );
  }

  if (license.status === "grace") {
    return (
      <div className="banner banner-warning">
        ارتباط با License Server برقرار نیست؛ نرم‌افزار در حالت مهلت (Grace) کار می‌کند.
      </div>
    );
  }

  if (license.expiry_warning && license.days_until_expiry !== null) {
    return (
      <div className="banner banner-warning">
        لایسنس تا {license.days_until_expiry} روز دیگر منقضی می‌شود.
      </div>
    );
  }

  return null;
}
