from __future__ import annotations

from datetime import datetime, timezone

from sqlalchemy import func, select
from sqlalchemy.orm import Session

from app.core.security import encrypt_secret
from app.domains.alerting.service import notify
from app.domains.audit.service import record_audit_event
from app.domains.devices.drivers import DeviceConnectionError, get_driver
from app.domains.devices.models import Device, DeviceStatus, VendorType
from app.domains.devices.schemas import DeviceCreate, SnmpConfigUpdate
from app.domains.licensing.service import enforce_device_quota


def count_devices(db: Session, vendor_type: VendorType) -> int:
    return db.scalar(select(func.count()).select_from(Device).where(Device.vendor_type == vendor_type)) or 0


def create_device(db: Session, payload: DeviceCreate, *, actor: str) -> Device:
    enforce_device_quota(db, payload.vendor_type.value, count_devices(db, payload.vendor_type))

    device = Device(
        name=payload.name,
        vendor_type=payload.vendor_type,
        host=payload.host,
        port=payload.port,
        verify_tls=payload.verify_tls,
        vdom=payload.vdom,
        encrypted_api_token=encrypt_secret(payload.api_token) if payload.api_token else None,
        username=payload.username,
        encrypted_password=encrypt_secret(payload.password) if payload.password else None,
    )
    db.add(device)
    db.commit()
    db.refresh(device)

    record_audit_event(db, actor=actor, action="device.create", target=device.name, details=payload.vendor_type.value)
    return device


def list_devices(db: Session) -> list[Device]:
    return list(db.scalars(select(Device).order_by(Device.created_at.asc())))


def get_device(db: Session, device_id) -> Device | None:
    return db.get(Device, device_id)


def find_device_by_host(db: Session, host: str) -> Device | None:
    return db.scalar(select(Device).where(Device.host == host))


def delete_device(db: Session, device: Device, *, actor: str) -> None:
    record_audit_event(db, actor=actor, action="device.delete", target=device.name, details=device.vendor_type.value)
    db.delete(device)
    db.commit()


def test_connection(db: Session, device: Device, *, actor: str) -> Device:
    driver = get_driver(device)
    device.last_checked_at = datetime.now(timezone.utc)

    try:
        info = driver.test_connection()
    except DeviceConnectionError as exc:
        device.status = DeviceStatus.ERROR
        device.last_error = str(exc)
        db.commit()
        record_audit_event(db, actor=actor, action="device.test_connection.failed", target=device.name, details=str(exc))
        notify(f"اتصال به {device.name} برقرار نشد", str(exc))
        db.refresh(device)
        return device

    device.status = DeviceStatus.ONLINE
    device.last_error = ""
    device.firmware_version = info.firmware_version
    device.serial_number = info.serial_number
    device.reported_hostname = info.hostname
    db.commit()
    record_audit_event(db, actor=actor, action="device.test_connection.success", target=device.name)
    db.refresh(device)
    return device


def set_snmp_config(db: Session, device: Device, payload: SnmpConfigUpdate, *, actor: str) -> Device:
    device.snmp_enabled = payload.enabled
    device.snmp_port = payload.port
    if payload.community:
        device.encrypted_snmp_community = encrypt_secret(payload.community)
    elif not payload.enabled:
        device.encrypted_snmp_community = None
    db.commit()
    record_audit_event(
        db, actor=actor, action="device.snmp_config.update", target=device.name, details=str(payload.enabled)
    )
    db.refresh(device)
    return device
