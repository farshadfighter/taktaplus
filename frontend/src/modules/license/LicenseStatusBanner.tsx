import { AlertTriangle, Clock, ShieldAlert } from "lucide-react";
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
        <ShieldAlert size={16} />
        <span>
          لایسنس taktaplus فعال نشده است. <Link to="/license">فعال‌سازی لایسنس</Link>
        </span>
      </div>
    );
  }

  if (license.status === "expired" || license.status === "suspended") {
    return (
      <div className="banner banner-danger">
        <AlertTriangle size={16} />
        <span>لایسنس {license.status === "expired" ? "منقضی شده" : "معلق شده"} است. برای تمدید با پشتیبانی تماس بگیرید.</span>
      </div>
    );
  }

  if (license.status === "grace") {
    return (
      <div className="banner banner-warning">
        <Clock size={16} />
        <span>ارتباط با License Server برقرار نیست؛ نرم‌افزار در حالت مهلت (Grace) کار می‌کند.</span>
      </div>
    );
  }

  if (license.expiry_warning && license.days_until_expiry !== null) {
    return (
      <div className="banner banner-warning">
        <Clock size={16} />
        <span>لایسنس تا {license.days_until_expiry} روز دیگر منقضی می‌شود.</span>
      </div>
    );
  }

  return null;
}
