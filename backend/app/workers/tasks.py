from __future__ import annotations

import glob
import os
import subprocess
from datetime import datetime, timedelta, timezone
from urllib.parse import urlparse

from app.core.config import get_settings
from app.db.session import SessionLocal
from app.domains.licensing import service as licensing_service
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
    """pg_dump taktaplus's own database (not device backups - see
    domains for device backup/restore, added in phase 2) to a local
    directory with retention cleanup. This protects the license binding,
    device credentials, and history if the management server's disk fails.
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
