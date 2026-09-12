from datetime import datetime, timedelta, timezone

import pytest

from app.core.security import decrypt_secret
from app.domains.devices import service
from app.domains.devices.models import VendorType
from app.domains.devices.schemas import DeviceCreate
from app.domains.licensing.models import License
from app.domains.licensing.service import LicenseRequiredError


def _grant_license(db_session, **overrides):
    defaults = dict(
        fingerprint="fp",
        customer_key="cust",
        fortigate_max_devices=2,
        fortiweb_max_devices=1,
        sms2fa_max_users=5,
        issued_at=datetime.now(timezone.utc),
        expires_at=datetime.now(timezone.utc) + timedelta(days=300),
        cached_token="tok",
        cached_token_issued_at=datetime.now(timezone.utc),
        last_heartbeat_at=datetime.now(timezone.utc),
        last_heartbeat_ok=True,
    )
    defaults.update(overrides)
    db_session.add(License(**defaults))
    db_session.commit()


def test_create_fortigate_device_encrypts_token(db_session):
    _grant_license(db_session)

    device = service.create_device(
        db_session,
        DeviceCreate(name="fw1", vendor_type=VendorType.FORTIGATE, host="10.0.0.1", api_token="plain-token"),
        actor="tester",
    )

    assert device.encrypted_api_token != "plain-token"
    assert decrypt_secret(device.encrypted_api_token) == "plain-token"


def test_create_fortiweb_device_encrypts_password(db_session):
    _grant_license(db_session)

    device = service.create_device(
        db_session,
        DeviceCreate(
            name="waf1", vendor_type=VendorType.FORTIWEB, host="10.0.0.2", username="admin", password="s3cret"
        ),
        actor="tester",
    )

    assert device.encrypted_password != "s3cret"
    assert decrypt_secret(device.encrypted_password) == "s3cret"


def test_create_device_blocked_without_license(db_session):
    with pytest.raises(LicenseRequiredError):
        service.create_device(
            db_session,
            DeviceCreate(name="fw1", vendor_type=VendorType.FORTIGATE, host="10.0.0.1", api_token="x"),
            actor="tester",
        )


def test_create_device_blocked_at_quota(db_session):
    _grant_license(db_session, fortiweb_max_devices=1)

    service.create_device(
        db_session,
        DeviceCreate(name="waf1", vendor_type=VendorType.FORTIWEB, host="10.0.0.2", username="a", password="b"),
        actor="tester",
    )

    with pytest.raises(LicenseRequiredError):
        service.create_device(
            db_session,
            DeviceCreate(name="waf2", vendor_type=VendorType.FORTIWEB, host="10.0.0.3", username="a", password="b"),
            actor="tester",
        )


def test_delete_device_removes_it(db_session):
    _grant_license(db_session)
    device = service.create_device(
        db_session,
        DeviceCreate(name="fw1", vendor_type=VendorType.FORTIGATE, host="10.0.0.1", api_token="x"),
        actor="tester",
    )

    service.delete_device(db_session, device, actor="tester")

    assert service.list_devices(db_session) == []


def test_device_create_schema_requires_matching_credentials():
    with pytest.raises(ValueError):
        DeviceCreate(name="fw1", vendor_type=VendorType.FORTIGATE, host="10.0.0.1")

    with pytest.raises(ValueError):
        DeviceCreate(name="waf1", vendor_type=VendorType.FORTIWEB, host="10.0.0.2", username="a")
