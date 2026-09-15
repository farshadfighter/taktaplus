import { ReactNode } from "react";

export type StatTone = "primary" | "success" | "warning" | "danger" | "info";

const TONE_STYLES: Record<StatTone, { bg: string; color: string }> = {
  primary: { bg: "var(--primary-soft)", color: "var(--primary)" },
  success: { bg: "var(--success-soft)", color: "var(--success)" },
  warning: { bg: "var(--warning-soft)", color: "var(--warning)" },
  danger: { bg: "var(--danger-soft)", color: "var(--danger)" },
  info: { bg: "var(--info-soft)", color: "var(--info)" },
};

export function StatCard({
  icon,
  value,
  label,
  tone = "primary",
}: {
  icon: ReactNode;
  value: ReactNode;
  label: string;
  tone?: StatTone;
}) {
  const style = TONE_STYLES[tone];
  return (
    <div className="stat-card">
      <div className="stat-icon" style={{ background: style.bg, color: style.color }}>
        {icon}
      </div>
      <div>
        <div className="stat-value">{value}</div>
        <div className="stat-label">{label}</div>
      </div>
    </div>
  );
}
