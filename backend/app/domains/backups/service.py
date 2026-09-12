from __future__ import annotations

import base64
import hashlib

from sqlalchemy import select
from sqlalchemy.orm import Session

from app.core.security import decrypt_secret, encrypt_secret
from app.domains.audit.service import record_audit_event
from app.domains.backups.models import Backup, BackupSource, BackupStatus
from app.domains.devices.drivers import DeviceConnectionError, get_driver
from app.domains.devices.drivers.base import FortinetDriver
from app.domains.devices.models import Device


class RestoreBlockedError(RuntimeError):
    """Raised when a restore looks unsafe (device identity mismatch) and the
    caller didn't pass force=True to acknowledge it.
    """


def create_backup(
    db: Session,
    device: Device,
    *,
    actor: str,
    source: BackupSource = BackupSource.MANUAL,
    driver: FortinetDriver | None = None,
) -> Backup:
    driver = driver or get_driver(device)

    backup = Backup(
        device_id=device.id,
        taken_by=actor,
        source=source,
        device_serial_snapshot=device.serial_number,
        device_firmware_snapshot=device.firmware_version,
    )

    try:
        content = driver.backup()
    except DeviceConnectionError as exc:
        backup.status = BackupStatus.FAILED
        backup.error_message = str(exc)
        db.add(backup)
        db.commit()
        record_audit_event(db, actor=actor, action="backup.create.failed", target=device.name, details=str(exc))
        db.refresh(backup)
        return backup

    backup.status = BackupStatus.SUCCESS
    backup.size_bytes = len(content)
    backup.checksum_sha256 = hashlib.sha256(content).hexdigest()
    # Base64 first so arbitrary bytes survive the round trip through
    # encrypt_secret/decrypt_secret, which operate on text.
    backup.encrypted_content = encrypt_secret(base64.b64encode(content).decode("ascii"))
    db.add(backup)
    db.commit()
    record_audit_event(db, actor=actor, action="backup.create.success", target=device.name)
    db.refresh(backup)
    return backup


def list_backups(db: Session, device_id) -> list[Backup]:
    return list(db.scalars(select(Backup).where(Backup.device_id == device_id).order_by(Backup.taken_at.desc())))


def get_backup(db: Session, backup_id) -> Backup | None:
    return db.get(Backup, backup_id)


def get_decrypted_content(backup: Backup) -> bytes:
    if backup.encrypted_content is None:
        raise ValueError("این بکاپ محتوایی ندارد (احتمالاً ناموفق بوده است)")
    return base64.b64decode(decrypt_secret(backup.encrypted_content))


def delete_backup(db: Session, backup: Backup, *, actor: str) -> None:
    record_audit_event(db, actor=actor, action="backup.delete", target=str(backup.id))
    db.delete(backup)
    db.commit()


def validate_restore_compatibility(device: Device, backup: Backup) -> list[str]:
    warnings: list[str] = []
    if (
        backup.device_serial_snapshot
        and device.serial_number
        and backup.device_serial_snapshot != device.serial_number
    ):
        warnings.append(
            f"این بکاپ از دستگاهی با سریال {backup.device_serial_snapshot} گرفته شده، "
            f"ولی سریال فعلی دستگاه {device.serial_number} است."
        )
    if (
        backup.device_firmware_snapshot
        and device.firmware_version
        and backup.device_firmware_snapshot.split(",")[0] != device.firmware_version.split(",")[0]
    ):
        warnings.append(
            f"نسخه فرم‌ور زمان بکاپ‌گیری ({backup.device_firmware_snapshot}) با نسخه فعلی دستگاه "
            f"({device.firmware_version}) متفاوت است."
        )
    return warnings


def restore_backup(
    db: Session,
    device: Device,
    backup: Backup,
    *,
    actor: str,
    force: bool = False,
    driver: FortinetDriver | None = None,
) -> None:
    if backup.status != BackupStatus.SUCCESS:
        raise ValueError("نمی‌توان از یک بکاپ ناموفق ریستور کرد")

    warnings = validate_restore_compatibility(device, backup)
    if warnings and not force:
        raise RestoreBlockedError(" ".join(warnings))

    driver = driver or get_driver(device)
    content = get_decrypted_content(backup)

    try:
        driver.restore(content)
    except DeviceConnectionError as exc:
        record_audit_event(db, actor=actor, action="backup.restore.failed", target=device.name, details=str(exc))
        raise

    record_audit_event(
        db, actor=actor, action="backup.restore.success", target=device.name, details=str(backup.id)
    )


def prune_old_backups(db: Session, device_id, keep_count: int) -> int:
    backups = list_backups(db, device_id)
    to_delete = backups[keep_count:]
    for backup in to_delete:
        db.delete(backup)
    if to_delete:
        db.commit()
    return len(to_delete)
