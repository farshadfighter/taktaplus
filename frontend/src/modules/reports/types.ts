export interface FleetStatusRow {
  device_id: string;
  device_name: string;
  vendor_type: "fortigate" | "fortiweb";
  status: "unknown" | "online" | "offline" | "error";
  firmware_version: string;
  last_backup_at: string | null;
  last_backup_status: "success" | "failed" | null;
  backup_overdue: boolean;
  snmp_enabled: boolean;
  latest_cpu_percent: number | null;
  latest_memory_percent: number | null;
  open_alert_count: number;
}
