import pytest

from app.domains.backups import service
from app.domains.backups.models import BackupStatus
from app.domains.devices.drivers.base import DeviceConnectionError, DeviceStatusInfo, FortinetDriver
from app.domains.devices.models import Device, VendorType


class StubDriver(FortinetDriver):
    def __init__(self, *, backup_content: bytes | None = None, backup_error: str | None = None, restore_error: str | None = None):
        self._backup_content = backup_content
        self._backup_error = backup_error
        self._restore_error = restore_error
        self.restored_content: bytes | None = None

    def test_connection(self) -> DeviceStatusInfo:
        raise NotImplementedError

    def backup(self) -> bytes:
        if self._backup_error:
            raise DeviceConnectionError(self._backup_error)
        return self._backup_content

    def restore(self, content: bytes) -> None:
        if self._restore_error:
            raise DeviceConnectionError(self._restore_error)
        self.restored_content = content


def _make_device(**overrides) -> Device:
    defaults = dict(name="fw1", vendor_type=VendorType.FORTIGATE, host="10.0.0.1", serial_number="FGT100")
    defaults.update(overrides)
    return Device(**defaults)


def test_create_backup_success_stores_encrypted_content(db_session):
    device = _make_device()
    db_session.add(device)
    db_session.commit()

    backup = service.create_backup(
        db_session, device, actor="tester", driver=StubDriver(backup_content=b"config system global\nend\n")
    )

    assert backup.status == BackupStatus.SUCCESS
    assert backup.size_bytes == len(b"config system global\nend\n")
    assert backup.encrypted_content is not None
    assert service.get_decrypted_content(backup) == b"config system global\nend\n"


def test_create_backup_failure_records_failed_status(db_session):
    device = _make_device()
    db_session.add(device)
    db_session.commit()

    backup = service.create_backup(
        db_session, device, actor="tester", driver=StubDriver(backup_error="اتصال قطع شد")
    )

    assert backup.status == BackupStatus.FAILED
    assert backup.error_message == "اتصال قطع شد"
    assert backup.encrypted_content is None


def test_create_backup_round_trips_binary_content(db_session):
    device = _make_device()
    db_session.add(device)
    db_session.commit()

    weird_bytes = bytes(range(256))  # not valid UTF-8 - must still round-trip
    backup = service.create_backup(db_session, device, actor="tester", driver=StubDriver(backup_content=weird_bytes))

    assert service.get_decrypted_content(backup) == weird_bytes


def test_validate_restore_compatibility_flags_serial_mismatch(db_session):
    device = _make_device(serial_number="FGT200")
    db_session.add(device)
    db_session.commit()
    backup = service.create_backup(db_session, device, actor="tester", driver=StubDriver(backup_content=b"x"))
    backup.device_serial_snapshot = "FGT999"  # simulate a backup taken from different hardware

    warnings = service.validate_restore_compatibility(device, backup)

    assert warnings


def test_restore_blocked_without_force_on_mismatch(db_session):
    device = _make_device(serial_number="FGT200")
    db_session.add(device)
    db_session.commit()
    backup = service.create_backup(db_session, device, actor="tester", driver=StubDriver(backup_content=b"x"))
    backup.device_serial_snapshot = "FGT999"

    with pytest.raises(service.RestoreBlockedError):
        service.restore_backup(db_session, device, backup, actor="tester", driver=StubDriver())


def test_restore_allowed_with_force_despite_mismatch(db_session):
    device = _make_device(serial_number="FGT200")
    db_session.add(device)
    db_session.commit()
    backup = service.create_backup(db_session, device, actor="tester", driver=StubDriver(backup_content=b"cfg"))
    backup.device_serial_snapshot = "FGT999"

    stub = StubDriver()
    service.restore_backup(db_session, device, backup, actor="tester", force=True, driver=stub)

    assert stub.restored_content == b"cfg"


def test_restore_rejects_failed_backup(db_session):
    device = _make_device()
    db_session.add(device)
    db_session.commit()
    backup = service.create_backup(db_session, device, actor="tester", driver=StubDriver(backup_error="x"))

    with pytest.raises(ValueError):
        service.restore_backup(db_session, device, backup, actor="tester", driver=StubDriver())


def test_prune_old_backups_keeps_latest_n(db_session):
    device = _make_device()
    db_session.add(device)
    db_session.commit()

    for _ in range(5):
        service.create_backup(db_session, device, actor="tester", driver=StubDriver(backup_content=b"x"))

    deleted = service.prune_old_backups(db_session, device.id, keep_count=2)

    assert deleted == 3
    assert len(service.list_backups(db_session, device.id)) == 2
