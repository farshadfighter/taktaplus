from __future__ import annotations

import hashlib
import os

from sqlalchemy import select
from sqlalchemy.orm import Session

from app.core.config import get_settings
from app.core.security import encrypt_secret
from app.domains.audit.service import record_audit_event
from app.domains.devices.drivers import DeviceConnectionError, get_driver
from app.domains.devices.drivers.fortigate import FortiGateDriver
from app.domains.devices.drivers.fortiweb import FortiWebDriver
from app.domains.devices.models import Device, VendorType
from app.domains.devices.service import test_connection as device_test_connection
from app.domains.distribution.models import FtpSourceConfig, Package, PackageSource, PushRecord, PushStatus


class DistributionError(RuntimeError):
    pass


def get_ftp_config(db: Session) -> FtpSourceConfig | None:
    return db.scalar(select(FtpSourceConfig).limit(1))


def set_ftp_config(
    db: Session, *, use_custom: bool, host: str, port: int, username: str, password: str | None, remote_path: str
) -> FtpSourceConfig:
    config = get_ftp_config(db) or FtpSourceConfig()
    config.use_custom = use_custom
    config.host = host
    config.port = port
    config.username = username
    config.remote_path = remote_path
    if password:
        config.encrypted_password = encrypt_secret(password)
    db.add(config)
    db.commit()
    db.refresh(config)
    return config


def list_packages(db: Session, *, vendor_type: VendorType | None = None) -> list[Package]:
    stmt = select(Package).order_by(Package.fetched_at.desc())
    if vendor_type is not None:
        stmt = stmt.where(Package.vendor_type == vendor_type)
    return list(db.scalars(stmt))


def get_package(db: Session, package_id) -> Package | None:
    return db.get(Package, package_id)


def delete_package(db: Session, package: Package, *, actor: str) -> None:
    record_audit_event(db, actor=actor, action="package.delete", target=package.filename)
    try:
        os.remove(package.local_path)
    except OSError:
        pass  # already gone - fine, we're deleting the record either way
    db.delete(package)
    db.commit()


def save_uploaded_package(
    db: Session,
    *,
    vendor_type: VendorType,
    package_type: str,
    filename: str,
    content: bytes,
    actor: str,
) -> Package:
    settings = get_settings()
    local_dir = os.path.join(settings.packages_root, vendor_type.value, package_type)
    os.makedirs(local_dir, exist_ok=True)
    local_path = os.path.join(local_dir, filename)

    with open(local_path, "wb") as fh:
        fh.write(content)

    package = Package(
        vendor_type=vendor_type,
        package_type=package_type,
        filename=filename,
        checksum_sha256=hashlib.sha256(content).hexdigest(),
        size_bytes=len(content),
        local_path=local_path,
        source=PackageSource.MANUAL_UPLOAD,
        uploaded_by=actor,
    )
    db.add(package)
    db.commit()
    db.refresh(package)

    record_audit_event(db, actor=actor, action="package.upload", target=filename, details=package_type)
    return package


def push_package_to_device(db: Session, device: Device, package: Package, *, actor: str) -> PushRecord:
    if device.vendor_type != package.vendor_type:
        raise DistributionError("نوع دستگاه با نوع بسته مطابقت ندارد")

    record = PushRecord(device_id=device.id, package_id=package.id, pushed_by=actor, status=PushStatus.SUCCESS)

    try:
        with open(package.local_path, "rb") as fh:
            content = fh.read()

        driver = get_driver(device)
        if device.vendor_type == VendorType.FORTIGATE:
            _push_to_fortigate(driver, package)
        else:
            _push_to_fortiweb(driver, package, content)
    except (DeviceConnectionError, OSError) as exc:
        record.status = PushStatus.FAILED
        record.error_message = str(exc)
        db.add(record)
        db.commit()
        record_audit_event(db, actor=actor, action="package.push.failed", target=device.name, details=str(exc))
        db.refresh(record)
        return record

    db.add(record)
    db.commit()
    record_audit_event(db, actor=actor, action="package.push.success", target=device.name, details=package.filename)

    # Best-effort post-push health check - a real rollback would need
    # per-package-type verification this codebase can't safely automate
    # yet, but confirming the device still answers at all catches the
    # worst case (a push that bricked the management plane) cheaply.
    try:
        updated_device = device_test_connection(db, device, actor="system(post-push-check)")
        record.post_push_check_ok = updated_device.status.value == "online"
    except Exception:  # noqa: BLE001 - health check must never fail the push itself
        record.post_push_check_ok = None
    db.commit()
    db.refresh(record)
    return record


def _push_to_fortigate(driver: FortiGateDriver, package: Package) -> None:
    settings = get_settings()
    if package.package_type == "firmware":
        with open(package.local_path, "rb") as fh:
            driver.push_firmware(fh.read())
        return

    if not settings.ftp_relay_advertised_host or not settings.ftp_relay_password:
        raise DeviceConnectionError(
            "آدرس یا رمز عبور relay FTP تنظیم نشده است - نمی‌توان بسته را به FortiGate پوش کرد"
        )

    driver.push_signature_via_ftp(
        package_type=package.package_type,
        filename=package.filename,
        relay_host=settings.ftp_relay_advertised_host,
        relay_port=settings.ftp_relay_port,
        relay_username=settings.ftp_relay_username,
        relay_password=settings.ftp_relay_password,
    )


def _push_to_fortiweb(driver: FortiWebDriver, package: Package, content: bytes) -> None:
    if package.package_type == "firmware":
        driver.push_firmware(content, filename=package.filename)
    else:
        driver.push_signature(content, filename=package.filename)


def list_push_history(db: Session, device_id) -> list[PushRecord]:
    return list(
        db.scalars(select(PushRecord).where(PushRecord.device_id == device_id).order_by(PushRecord.pushed_at.desc()))
    )
