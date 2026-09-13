from __future__ import annotations

from datetime import datetime, timedelta, timezone

from sqlalchemy import select
from sqlalchemy.orm import Session

from app.core.config import get_settings
from app.domains.backups.models import Backup, BackupStatus
from app.domains.devices.service import list_devices
from app.domains.monitoring.models import SnmpAlert, SnmpMetricSample
from app.domains.reporting.schemas import FleetStatusRow


def _latest_backup(db: Session, device_id) -> Backup | None:
    return db.scalar(select(Backup).where(Backup.device_id == device_id).order_by(Backup.taken_at.desc()).limit(1))


def _latest_metric(db: Session, device_id) -> SnmpMetricSample | None:
    return db.scalar(
        select(SnmpMetricSample)
        .where(SnmpMetricSample.device_id == device_id)
        .order_by(SnmpMetricSample.collected_at.desc())
        .limit(1)
    )


def _open_alert_count(db: Session, device_id) -> int:
    return len(list(db.scalars(select(SnmpAlert).where(SnmpAlert.device_id == device_id, SnmpAlert.acknowledged.is_(False)))))


def _is_backup_overdue(latest_backup: Backup | None, threshold_hours: int) -> bool:
    if latest_backup is None or latest_backup.status != BackupStatus.SUCCESS:
        return True
    age = datetime.now(timezone.utc) - _aware(latest_backup.taken_at)
    return age > timedelta(hours=threshold_hours)


def _aware(dt: datetime) -> datetime:
    return dt if dt.tzinfo is not None else dt.replace(tzinfo=timezone.utc)


def build_fleet_status(db: Session) -> list[FleetStatusRow]:
    settings = get_settings()
    rows: list[FleetStatusRow] = []

    for device in list_devices(db):
        backup = _latest_backup(db, device.id)
        metric = _latest_metric(db, device.id)

        rows.append(
            FleetStatusRow(
                device_id=device.id,
                device_name=device.name,
                vendor_type=device.vendor_type,
                status=device.status,
                firmware_version=device.firmware_version,
                last_backup_at=backup.taken_at if backup else None,
                last_backup_status=backup.status if backup else None,
                backup_overdue=_is_backup_overdue(backup, settings.backup_overdue_hours),
                snmp_enabled=device.snmp_enabled,
                latest_cpu_percent=metric.cpu_percent if metric else None,
                latest_memory_percent=metric.memory_percent if metric else None,
                open_alert_count=_open_alert_count(db, device.id),
            )
        )

    return rows
