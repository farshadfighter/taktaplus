"""Diagnostic bundle export.

Support can't SSH into an air-gapped customer's box, so operators export a
sanitized zip (logs + service status + non-secret config) here and send it
out-of-band. Anything encrypted at rest (device tokens, FTP/SMS passwords,
the license cached token) is deliberately excluded, never redacted-in-place -
excluding by field name is safer than trying to scrub free-text logs.
"""

from __future__ import annotations

import io
import json
import zipfile
from datetime import datetime, timezone

from sqlalchemy import select
from sqlalchemy.orm import Session

from app.domains.licensing import service as licensing_service
from app.domains.licensing.models import License

LOG_PATHS = ["/var/log/taktaplus/app.log", "/var/log/taktaplus/worker.log"]


def _safe_read(path: str, max_bytes: int = 2_000_000) -> str:
    try:
        with open(path, "rb") as fh:
            data = fh.read(max_bytes)
        return data.decode("utf-8", errors="replace")
    except OSError as exc:
        return f"<could not read {path}: {exc}>"


def _license_summary(db: Session) -> dict:
    license_row = db.scalar(select(License).limit(1))
    if license_row is None:
        return {"status": "unactivated"}
    return {
        "status": licensing_service.compute_status(license_row).value,
        "fortigate_max_devices": license_row.fortigate_max_devices,
        "fortiweb_max_devices": license_row.fortiweb_max_devices,
        "sms2fa_max_users": license_row.sms2fa_max_users,
        "expires_at": license_row.expires_at.isoformat() if license_row.expires_at else None,
        "last_heartbeat_at": license_row.last_heartbeat_at.isoformat() if license_row.last_heartbeat_at else None,
        "last_heartbeat_ok": license_row.last_heartbeat_ok,
    }


def build_diagnostic_bundle(db: Session) -> bytes:
    buffer = io.BytesIO()
    with zipfile.ZipFile(buffer, "w", zipfile.ZIP_DEFLATED) as zf:
        manifest = {
            "generated_at": datetime.now(timezone.utc).isoformat(),
            "license": _license_summary(db),
        }
        zf.writestr("manifest.json", json.dumps(manifest, ensure_ascii=False, indent=2))

        for path in LOG_PATHS:
            zf.writestr(f"logs/{path.split('/')[-1]}", _safe_read(path))

    return buffer.getvalue()
