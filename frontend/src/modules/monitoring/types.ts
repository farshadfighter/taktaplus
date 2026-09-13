export interface SnmpMetric {
  id: string;
  collected_at: string;
  cpu_percent: number | null;
  memory_percent: number | null;
  session_count: number | null;
  sys_uptime_ticks: number | null;
}

export type AlertSeverity = "info" | "warning" | "critical";
export type AlertSource = "trap" | "poll_threshold" | "poll_unreachable";

export interface SnmpAlert {
  id: string;
  device_id: string;
  received_at: string;
  source: AlertSource;
  severity: AlertSeverity;
  trap_oid: string;
  message: string;
  acknowledged: boolean;
}
