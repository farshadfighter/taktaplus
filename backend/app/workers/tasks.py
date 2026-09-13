from __future__ import annotations

import glob
import os
import subprocess
from datetime import datetime, timedelta, timezone
from urllib.parse import urlparse

from app.core.config import get_settings
from app.core.security import decrypt_secret
from app.db.session import SessionLocal
from app.domains.backups.models import BackupSource
from app.domains.backups.service import create_backup, prune_old_backups
from app.domains.devices.service import list_devices
from app.domains.distribution.ftp_source import FtpSyncError, sync_from_ftp
from app.domains.licensing import service as licensing_service
from app.domains.monitoring.service import record_metric_sample, record_poll_failure
from app.domains.monitoring.snmp_client import SnmpPollError, poll_device
from app.domains.radius.admin_service import prune_expired_challenges
from app.workers.celery_app import celery_app


@celery_app.task(name="app.workers.tasks.run_license_heartbeat")
def run_license_heartbeat() -> None:
    db = SessionLocal()
    try:
        licensing_service.heartbeat(db)
    finally:
        db.close()


@celery_app.task(name="app.workers.tasks.run_self_db_backup")
def run_self_db_backup() -> str:
    """pg_dump taktaplus's own database (not device config backups - see
    run_scheduled_device_backups for those) to a local directory with
    retention cleanup. This protects the license binding, device
    credentials, and history if the management server's disk fails.
    """
    settings = get_settings()
    os.makedirs(settings.db_backup_dir, exist_ok=True)

    parsed = urlparse(settings.database_url.replace("+psycopg", ""))
    timestamp = datetime.now(timezone.utc).strftime("%Y%m%dT%H%M%SZ")
    dest = os.path.join(settings.db_backup_dir, f"taktaplus-db-{timestamp}.sql.gz")

    env = os.environ.copy()
    if parsed.password:
        env["PGPASSWORD"] = parsed.password

    dump_cmd = [
        "pg_dump",
        "-h", parsed.hostname or "localhost",
        "-p", str(parsed.port or 5432),
        "-U", parsed.username or "taktaplus",
        parsed.path.lstrip("/"),
    ]
    with open(dest, "wb") as out:
        dump = subprocess.run(dump_cmd, env=env, stdout=subprocess.PIPE, check=True)
        subprocess.run(["gzip"], input=dump.stdout, stdout=out, check=True)

    _cleanup_old_backups(settings.db_backup_dir, settings.db_backup_retention_days)
    return dest


def _cleanup_old_backups(directory: str, retention_days: int) -> None:
    cutoff = datetime.now(timezone.utc) - timedelta(days=retention_days)
    for path in glob.glob(os.path.join(directory, "taktaplus-db-*.sql.gz")):
        mtime = datetime.fromtimestamp(os.path.getmtime(path), tz=timezone.utc)
        if mtime < cutoff:
            os.remove(path)


@celery_app.task(name="app.workers.tasks.run_scheduled_device_backups")
def run_scheduled_device_backups() -> dict:
    """Nightly config backup for every device, with per-device retention.
    Failures for one device (unreachable, bad credentials) don't stop the
    rest - each is recorded as a failed Backup row via create_backup, same
    as a manual backup would be.
    """
    settings = get_settings()
    db = SessionLocal()
    results = {"succeeded": 0, "failed": 0}
    try:
        for device in list_devices(db):
            backup = create_backup(db, device, actor="scheduler", source=BackupSource.SCHEDULED)
            if backup.status.value == "success":
                results["succeeded"] += 1
            else:
                results["failed"] += 1
            prune_old_backups(db, device.id, settings.device_backup_retention_count)
    finally:
        db.close()
    return results


@celery_app.task(name="app.workers.tasks.run_snmp_poll_all_devices")
def run_snmp_poll_all_devices() -> dict:
    """Poll every SNMP-enabled device for CPU/memory/session metrics. A
    device that doesn't respond gets a critical alert (not just a skipped
    row) since an unreachable device is itself worth knowing about,
    independent of the REST-API-based connectivity check in phase 1.
    """
    settings = get_settings()
    db = SessionLocal()
    results = {"polled": 0, "failed": 0}
    try:
        for device in list_devices(db):
            if not device.snmp_enabled or not device.encrypted_snmp_community:
                continue

            community = decrypt_secret(device.encrypted_snmp_community)
            try:
                metrics = poll_device(
                    device.host, device.snmp_port, community, timeout=settings.snmp_poll_timeout_seconds
                )
            except SnmpPollError as exc:
                record_poll_failure(db, device, str(exc))
                results["failed"] += 1
                continue

            record_metric_sample(db, device, metrics)
            results["polled"] += 1
    finally:
        db.close()
    return results


@celery_app.task(name="app.workers.tasks.run_ftp_package_sync")
def run_ftp_package_sync() -> dict:
    settings = get_settings()
    db = SessionLocal()
    try:
        return sync_from_ftp(db, packages_root=settings.packages_root)
    except FtpSyncError as exc:
        return {"error": str(exc)}
    finally:
        db.close()


@celery_app.task(name="app.workers.tasks.run_prune_expired_otp_challenges")
def run_prune_expired_otp_challenges() -> int:
    db = SessionLocal()
    try:
        return prune_expired_challenges(db)
    finally:
        db.close()
