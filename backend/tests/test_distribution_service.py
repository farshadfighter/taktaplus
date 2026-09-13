import pytest

from app.domains.devices.drivers.base import DeviceConnectionError, DeviceStatusInfo, FortinetDriver
from app.domains.devices.models import Device, VendorType
from app.domains.distribution import service
from app.domains.distribution.models import PushStatus


class StubFortiGateDriver(FortinetDriver):
    def __init__(self, *, fail: str | None = None):
        self.fail = fail
        self.pushed_via_ftp: dict | None = None
        self.pushed_firmware: bytes | None = None

    def test_connection(self) -> DeviceStatusInfo:
        return DeviceStatusInfo(firmware_version="v7.2", serial_number="FGT1", hostname="fw1")

    def push_signature_via_ftp(self, **kwargs) -> str:
        if self.fail:
            raise DeviceConnectionError(self.fail)
        self.pushed_via_ftp = kwargs
        return "ok"

    def push_firmware(self, content: bytes) -> None:
        if self.fail:
            raise DeviceConnectionError(self.fail)
        self.pushed_firmware = content


def _make_device(db_session, **overrides) -> Device:
    defaults = dict(name="fw1", vendor_type=VendorType.FORTIGATE, host="10.0.0.1", serial_number="FGT1")
    defaults.update(overrides)
    device = Device(**defaults)
    db_session.add(device)
    db_session.commit()
    return device


def _make_package(db_session, tmp_path, **overrides):
    from app.domains.distribution.models import Package, PackageSource

    local_path = tmp_path / "pkg.bin"
    local_path.write_bytes(b"package-content")
    defaults = dict(
        vendor_type=VendorType.FORTIGATE,
        package_type="ips",
        filename="pkg.bin",
        local_path=str(local_path),
        source=PackageSource.MANUAL_UPLOAD,
    )
    defaults.update(overrides)
    package = Package(**defaults)
    db_session.add(package)
    db_session.commit()
    return package


def test_push_rejects_vendor_mismatch(db_session, tmp_path):
    device = _make_device(db_session, vendor_type=VendorType.FORTIWEB, username="a", encrypted_password=None)
    package = _make_package(db_session, tmp_path, vendor_type=VendorType.FORTIGATE)

    with pytest.raises(service.DistributionError):
        service.push_package_to_device(db_session, device, package, actor="tester")


def test_save_uploaded_package_writes_file_and_row(db_session, tmp_path, monkeypatch):
    monkeypatch.setattr("app.domains.distribution.service.get_settings", lambda: type("S", (), {"packages_root": str(tmp_path)})())

    package = service.save_uploaded_package(
        db_session,
        vendor_type=VendorType.FORTIGATE,
        package_type="ips",
        filename="new.pkg",
        content=b"hello",
        actor="tester",
    )

    assert package.size_bytes == 5
    with open(package.local_path, "rb") as fh:
        assert fh.read() == b"hello"


def test_delete_package_removes_file_and_row(db_session, tmp_path):
    package = _make_package(db_session, tmp_path)
    path = package.local_path

    service.delete_package(db_session, package, actor="tester")

    assert not __import__("os").path.exists(path)
    assert service.get_package(db_session, package.id) is None


def test_push_to_fortigate_signature_success(db_session, tmp_path, monkeypatch):
    device = _make_device(db_session)
    package = _make_package(db_session, tmp_path, package_type="ips")
    stub = StubFortiGateDriver()

    monkeypatch.setattr("app.domains.distribution.service.get_driver", lambda d: stub)
    monkeypatch.setattr(
        "app.domains.distribution.service.get_settings",
        lambda: type(
            "S",
            (),
            {"ftp_relay_advertised_host": "10.0.0.99", "ftp_relay_port": 21, "ftp_relay_username": "relay", "ftp_relay_password": "pw"},
        )(),
    )
    monkeypatch.setattr(
        "app.domains.distribution.service.device_test_connection", lambda db, device, actor: device
    )

    record = service.push_package_to_device(db_session, device, package, actor="tester")

    assert record.status == PushStatus.SUCCESS
    assert stub.pushed_via_ftp["package_type"] == "ips"
    assert stub.pushed_via_ftp["filename"] == "pkg.bin"


def test_push_to_fortigate_records_failure(db_session, tmp_path, monkeypatch):
    device = _make_device(db_session)
    package = _make_package(db_session, tmp_path, package_type="ips")
    stub = StubFortiGateDriver(fail="اتصال قطع شد")

    monkeypatch.setattr("app.domains.distribution.service.get_driver", lambda d: stub)
    monkeypatch.setattr(
        "app.domains.distribution.service.get_settings",
        lambda: type(
            "S",
            (),
            {"ftp_relay_advertised_host": "10.0.0.99", "ftp_relay_port": 21, "ftp_relay_username": "relay", "ftp_relay_password": "pw"},
        )(),
    )

    record = service.push_package_to_device(db_session, device, package, actor="tester")

    assert record.status == PushStatus.FAILED
    assert record.error_message == "اتصال قطع شد"


def test_push_firmware_uses_push_firmware_method(db_session, tmp_path, monkeypatch):
    device = _make_device(db_session)
    package = _make_package(db_session, tmp_path, package_type="firmware")
    stub = StubFortiGateDriver()

    monkeypatch.setattr("app.domains.distribution.service.get_driver", lambda d: stub)
    monkeypatch.setattr(
        "app.domains.distribution.service.device_test_connection", lambda db, device, actor: device
    )

    record = service.push_package_to_device(db_session, device, package, actor="tester")

    assert record.status == PushStatus.SUCCESS
    assert stub.pushed_firmware == b"package-content"
