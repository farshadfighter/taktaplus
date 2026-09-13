from __future__ import annotations

import uuid
from datetime import datetime, timedelta, timezone

from sqlalchemy import func, select
from sqlalchemy.orm import Session, aliased

from app.core.config import get_settings
from app.domains.backups.models import Backup, BackupStatus
from app.domains.devices.service import list_devices
from app.domains.monitoring.models import SnmpAlert, SnmpMetricSample
from app.domains.reporting.schemas import FleetStatusRow


def _latest_backups_by_device(db: Session, device_ids: list[uuid.UUID]) -> dict[uuid.UUID, Backup]:
    """One query for every device's latest backup instead of one query per
    device - the per-device version made build_fleet_status issue 1+3N
    queries for an N-device fleet (this, the metrics, and the alert-count
    lookup below).
    """
    if not device_ids:
        return {}
    rn = func.row_number().over(partition_by=Backup.device_id, order_by=Backup.taken_at.desc())
    ranked = select(Backup, rn.label("rn")).where(Backup.device_id.in_(device_ids)).subquery()
    latest = aliased(Backup, ranked)
    rows = db.scalars(select(latest).where(ranked.c.rn == 1))
    return {b.device_id: b for b in rows}


def _latest_metrics_by_device(db: Session, device_ids: list[uuid.UUID]) -> dict[uuid.UUID, SnmpMetricSample]:
    if not device_ids:
        return {}
    rn = func.row_number().over(partition_by=SnmpMetricSample.device_id, order_by=SnmpMetricSample.collected_at.desc())
    ranked = select(SnmpMetricSample, rn.label("rn")).where(SnmpMetricSample.device_id.in_(device_ids)).subquery()
    latest = aliased(SnmpMetricSample, ranked)
    rows = db.scalars(select(latest).where(ranked.c.rn == 1))
    return {m.device_id: m for m in rows}


def _open_alert_counts_by_device(db: Session, device_ids: list[uuid.UUID]) -> dict[uuid.UUID, int]:
    if not device_ids:
        return {}
    rows = db.execute(
        select(SnmpAlert.device_id, func.count())
        .where(SnmpAlert.device_id.in_(device_ids), SnmpAlert.acknowledged.is_(False))
        .group_by(SnmpAlert.device_id)
    ).all()
    return {device_id: count for device_id, count in rows}


def _is_backup_overdue(latest_backup: Backup | None, threshold_hours: int) -> bool:
    if latest_backup is None or latest_backup.status != BackupStatus.SUCCESS:
        return True
    age = datetime.now(timezone.utc) - _aware(latest_backup.taken_at)
    return age > timedelta(hours=threshold_hours)


def _aware(dt: datetime) -> datetime:
    return dt if dt.tzinfo is not None else dt.replace(tzinfo=timezone.utc)


def build_fleet_status(db: Session) -> list[FleetStatusRow]:
    settings = get_settings()
    devices = list_devices(db)
    device_ids = [d.id for d in devices]

    backups_by_device = _latest_backups_by_device(db, device_ids)
    metrics_by_device = _latest_metrics_by_device(db, device_ids)
    alert_counts_by_device = _open_alert_counts_by_device(db, device_ids)

    rows: list[FleetStatusRow] = []
    for device in devices:
        backup = backups_by_device.get(device.id)
        metric = metrics_by_device.get(device.id)

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
                open_alert_count=alert_counts_by_device.get(device.id, 0),
            )
        )

    return rows
