export interface LicenseStatus {
  status: "unactivated" | "active" | "grace" | "expired" | "suspended";
  fortigate_max_devices: number;
  fortiweb_max_devices: number;
  sms2fa_max_users: number;
  expires_at: string | null;
  days_until_expiry: number | null;
  expiry_warning: boolean;
  last_heartbeat_at: string | null;
  last_heartbeat_ok: boolean;
}
