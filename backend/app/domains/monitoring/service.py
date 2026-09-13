from __future__ import annotations

from sqlalchemy import select
from sqlalchemy.orm import Session

from app.core.config import get_settings
from app.core.security import decrypt_secret
from app.domains.alerting.service import notify
from app.domains.devices.models import Device
from app.domains.monitoring.models import AlertSeverity, AlertSource, SnmpAlert, SnmpMetricSample
from app.domains.monitoring.snmp_client import SnmpMetrics, SnmpPollError, poll_device


class SnmpNotConfiguredError(RuntimeError):
    pass


def poll_now(db: Session, device: Device) -> SnmpMetricSample:
    if not device.snmp_enabled or not device.encrypted_snmp_community:
        raise SnmpNotConfiguredError("SNMP برای این دستگاه فعال نشده است")

    settings = get_settings()
    community = decrypt_secret(device.encrypted_snmp_community)
    try:
        metrics = poll_device(device.host, device.snmp_port, community, timeout=settings.snmp_poll_timeout_seconds)
    except SnmpPollError as exc:
        record_poll_failure(db, device, str(exc))
        raise

    return record_metric_sample(db, device, metrics)


def record_metric_sample(db: Session, device: Device, metrics: SnmpMetrics) -> SnmpMetricSample:
    sample = SnmpMetricSample(
        device_id=device.id,
        cpu_percent=metrics.cpu_percent,
        memory_percent=metrics.memory_percent,
        session_count=metrics.session_count,
        sys_uptime_ticks=metrics.sys_uptime_ticks,
    )
    db.add(sample)
    db.commit()

    _evaluate_thresholds(db, device, metrics)
    return sample


def _evaluate_thresholds(db: Session, device: Device, metrics: SnmpMetrics) -> None:
    settings = get_settings()

    if metrics.cpu_percent is not None and metrics.cpu_percent >= settings.snmp_cpu_alert_threshold:
        record_alert(
            db,
            device,
            source=AlertSource.POLL_THRESHOLD,
            severity=AlertSeverity.WARNING,
            message=f"مصرف CPU به {metrics.cpu_percent:.0f}% رسیده (آستانه {settings.snmp_cpu_alert_threshold}%)",
        )

    if metrics.memory_percent is not None and metrics.memory_percent >= settings.snmp_memory_alert_threshold:
        record_alert(
            db,
            device,
            source=AlertSource.POLL_THRESHOLD,
            severity=AlertSeverity.WARNING,
            message=f"مصرف حافظه به {metrics.memory_percent:.0f}% رسیده (آستانه {settings.snmp_memory_alert_threshold}%)",
        )


def record_poll_failure(db: Session, device: Device, error: str) -> SnmpAlert:
    return record_alert(
        db,
        device,
        source=AlertSource.POLL_UNREACHABLE,
        severity=AlertSeverity.CRITICAL,
        message=f"دستگاه از طریق SNMP در دسترس نیست: {error}",
    )


def record_trap_alert(db: Session, device: Device, trap_oid: str, severity: AlertSeverity, message: str) -> SnmpAlert:
    return record_alert(db, device, source=AlertSource.TRAP, severity=severity, message=message, trap_oid=trap_oid)


def record_alert(
    db: Session,
    device: Device,
    *,
    source: AlertSource,
    severity: AlertSeverity,
    message: str,
    trap_oid: str = "",
) -> SnmpAlert:
    alert = SnmpAlert(device_id=device.id, source=source, severity=severity, message=message, trap_oid=trap_oid)
    db.add(alert)
    db.commit()
    db.refresh(alert)

    if severity in (AlertSeverity.WARNING, AlertSeverity.CRITICAL):
        notify(f"{severity.value.upper()} - {device.name}", message)

    return alert


def list_metrics(db: Session, device_id, limit: int = 200) -> list[SnmpMetricSample]:
    return list(
        db.scalars(
            select(SnmpMetricSample)
            .where(SnmpMetricSample.device_id == device_id)
            .order_by(SnmpMetricSample.collected_at.desc())
            .limit(limit)
        )
    )


def list_alerts(db: Session, device_id, limit: int = 200) -> list[SnmpAlert]:
    return list(
        db.scalars(
            select(SnmpAlert)
            .where(SnmpAlert.device_id == device_id)
            .order_by(SnmpAlert.received_at.desc())
            .limit(limit)
        )
    )


def get_alert(db: Session, alert_id) -> SnmpAlert | None:
    return db.get(SnmpAlert, alert_id)


def acknowledge_alert(db: Session, alert: SnmpAlert) -> SnmpAlert:
    alert.acknowledged = True
    db.commit()
    db.refresh(alert)
    return alert
