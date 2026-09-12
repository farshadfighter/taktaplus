from datetime import datetime, timedelta, timezone

import pytest

from app.domains.licensing import service
from app.domains.licensing.models import License, LicenseStatus


def _make_license(**overrides) -> License:
    defaults = dict(
        fingerprint="abc123",
        customer_key="cust-1",
        status=LicenseStatus.ACTIVE,
        fortigate_max_devices=5,
        fortiweb_max_devices=2,
        sms2fa_max_users=10,
        issued_at=datetime.now(timezone.utc) - timedelta(days=1),
        expires_at=datetime.now(timezone.utc) + timedelta(days=300),
        cached_token="signed-token",
        cached_token_issued_at=datetime.now(timezone.utc),
        last_heartbeat_at=datetime.now(timezone.utc),
        last_heartbeat_ok=True,
    )
    defaults.update(overrides)
    return License(**defaults)


def test_compute_status_unactivated_when_never_activated():
    license_row = License(fingerprint="x", customer_key="y")

    assert service.compute_status(license_row) == LicenseStatus.UNACTIVATED


def test_compute_status_active_after_successful_heartbeat():
    license_row = _make_license(last_heartbeat_ok=True)

    assert service.compute_status(license_row) == LicenseStatus.ACTIVE


def test_compute_status_grace_when_heartbeat_failing_but_within_grace_period():
    license_row = _make_license(
        last_heartbeat_ok=False,
        cached_token_issued_at=datetime.now(timezone.utc) - timedelta(days=3),
    )

    assert service.compute_status(license_row) == LicenseStatus.ACTIVE  # within default 7-day grace


def test_compute_status_falls_to_grace_after_grace_period_elapses():
    license_row = _make_license(
        last_heartbeat_ok=False,
        cached_token_issued_at=datetime.now(timezone.utc) - timedelta(days=10),
    )

    assert service.compute_status(license_row) == LicenseStatus.GRACE


def test_compute_status_expired_past_expiry_date():
    license_row = _make_license(expires_at=datetime.now(timezone.utc) - timedelta(days=1))

    assert service.compute_status(license_row) == LicenseStatus.EXPIRED


def test_compute_status_suspended_is_sticky():
    license_row = _make_license(status=LicenseStatus.SUSPENDED)

    assert service.compute_status(license_row) == LicenseStatus.SUSPENDED


def test_should_warn_expiry_within_window():
    license_row = _make_license(expires_at=datetime.now(timezone.utc) + timedelta(days=10))

    assert service.should_warn_expiry(license_row) is True


def test_should_warn_expiry_outside_window():
    license_row = _make_license(expires_at=datetime.now(timezone.utc) + timedelta(days=100))

    assert service.should_warn_expiry(license_row) is False


def test_enforce_device_quota_blocks_at_limit(db_session):
    db_session.add(_make_license(fortigate_max_devices=2))
    db_session.commit()

    service.enforce_device_quota(db_session, "fortigate", current_count=1)  # ok, 1 < 2

    with pytest.raises(service.LicenseRequiredError):
        service.enforce_device_quota(db_session, "fortigate", current_count=2)  # at limit


def test_enforce_device_quota_without_license_raises(db_session):
    with pytest.raises(service.LicenseRequiredError):
        service.enforce_device_quota(db_session, "fortigate", current_count=0)


def test_enforce_2fa_seat_quota(db_session):
    db_session.add(_make_license(sms2fa_max_users=3))
    db_session.commit()

    service.enforce_2fa_seat_quota(db_session, current_count=2)

    with pytest.raises(service.LicenseRequiredError):
        service.enforce_2fa_seat_quota(db_session, current_count=3)


def test_enforce_2fa_seat_quota_blocked_when_module_not_purchased(db_session):
    db_session.add(_make_license(sms2fa_max_users=0))
    db_session.commit()

    with pytest.raises(service.LicenseRequiredError):
        service.enforce_2fa_seat_quota(db_session, current_count=0)
