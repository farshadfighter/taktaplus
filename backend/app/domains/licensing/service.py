from __future__ import annotations

from datetime import datetime, timedelta, timezone

from sqlalchemy import select
from sqlalchemy.orm import Session

from app.core.config import get_settings
from app.domains.licensing.client import LicenseServerClient, LicenseServerError, LicenseSuspended
from app.domains.licensing.fingerprint import get_or_create_fingerprint
from app.domains.licensing.models import License, LicenseEvent, LicenseStatus


class LicenseRequiredError(RuntimeError):
    """Raised by enforcement helpers when an operation is blocked by licensing."""


def get_license(db: Session) -> License | None:
    return db.scalar(select(License).limit(1))


def lock_license_for_update(db: Session) -> License | None:
    """Row-lock the (singleton) license row for the rest of the current
    transaction. Any create flow that checks a quota (enforce_device_quota,
    enforce_2fa_seat_quota) and then inserts a row must call this *before*
    counting, in the same session, and commit before releasing it -
    otherwise two concurrent requests can both pass the count check before
    either commits and together exceed the licensed quota (a
    check-then-insert TOCTOU race). SQLite (used in tests) has no row-level
    locking and silently ignores FOR UPDATE, so this is a no-op there -
    fine, since the test suite doesn't exercise concurrent writers.
    """
    return db.scalar(select(License).limit(1).with_for_update())


def _now() -> datetime:
    return datetime.now(timezone.utc)


def _aware(dt: datetime) -> datetime:
    """SQLite (used in tests) drops tzinfo from DateTime(timezone=True)
    columns on read-back even though Postgres (production) preserves it;
    normalize so status comparisons work the same on both backends.
    """
    return dt if dt.tzinfo is not None else dt.replace(tzinfo=timezone.utc)


def compute_status(license_row: License) -> LicenseStatus:
    """Recompute status from timestamps rather than trusting a stale column,
    so a server that's just been idle for a while reports itself correctly
    even before the next heartbeat/celery beat tick runs.
    """
    if license_row.expires_at is None:
        return LicenseStatus.UNACTIVATED

    settings = get_settings()
    now = _now()

    if license_row.status == LicenseStatus.SUSPENDED:
        return LicenseStatus.SUSPENDED

    if now > _aware(license_row.expires_at):
        return LicenseStatus.EXPIRED

    if license_row.last_heartbeat_ok:
        return LicenseStatus.ACTIVE

    token_issued_at = _aware(license_row.cached_token_issued_at) if license_row.cached_token_issued_at else now
    grace_deadline = token_issued_at + timedelta(days=settings.license_grace_period_days)
    return LicenseStatus.ACTIVE if now < grace_deadline else LicenseStatus.GRACE


def days_until_expiry(license_row: License) -> int | None:
    if license_row.expires_at is None:
        return None
    return (_aware(license_row.expires_at) - _now()).days


def should_warn_expiry(license_row: License) -> bool:
    remaining = days_until_expiry(license_row)
    return remaining is not None and 0 <= remaining <= get_settings().license_expiry_warning_days


def activate(db: Session, customer_key: str) -> License:
    fingerprint = get_or_create_fingerprint()
    client = LicenseServerClient()

    try:
        grant = client.activate(fingerprint=fingerprint, customer_key=customer_key)
    except LicenseServerError as exc:
        db.add(LicenseEvent(event_type="activate", success=False, message=str(exc)))
        db.commit()
        raise

    license_row = get_license(db) or License(fingerprint=fingerprint, customer_key=customer_key)
    _apply_grant(license_row, grant)
    db.add(license_row)
    db.add(LicenseEvent(event_type="activate", success=True, message="activated"))
    db.commit()
    db.refresh(license_row)
    return license_row


def heartbeat(db: Session) -> License | None:
    license_row = get_license(db)
    if license_row is None or not license_row.cached_token:
        return license_row

    client = LicenseServerClient()
    try:
        grant = client.heartbeat(fingerprint=license_row.fingerprint, current_token=license_row.cached_token)
    except LicenseSuspended as exc:
        license_row.status = LicenseStatus.SUSPENDED
        license_row.last_heartbeat_ok = False
        db.add(LicenseEvent(event_type="heartbeat", success=False, message=str(exc)))
        db.commit()
        return license_row
    except LicenseServerError as exc:
        # Server unreachable: keep the cached token, let compute_status()
        # decide ACTIVE-vs-GRACE from the grace period instead of hard-failing.
        license_row.last_heartbeat_ok = False
        license_row.last_heartbeat_at = _now()
        db.add(LicenseEvent(event_type="heartbeat", success=False, message=str(exc)))
        db.commit()
        return license_row

    _apply_grant(license_row, grant)
    db.add(LicenseEvent(event_type="heartbeat", success=True, message="ok"))
    db.commit()
    db.refresh(license_row)
    return license_row


def _apply_grant(license_row: License, grant) -> None:
    license_row.fortigate_max_devices = grant.fortigate_max_devices
    license_row.fortiweb_max_devices = grant.fortiweb_max_devices
    license_row.sms2fa_max_users = grant.sms2fa_max_users
    license_row.issued_at = grant.issued_at
    license_row.expires_at = grant.expires_at
    license_row.cached_token = grant.token
    license_row.cached_token_issued_at = _now()
    license_row.last_heartbeat_at = _now()
    license_row.last_heartbeat_ok = True
    license_row.status = LicenseStatus.ACTIVE


def enforce_device_quota(db: Session, vendor_type: str, current_count: int) -> None:
    """Raise LicenseRequiredError if adding one more device of this vendor
    type would exceed the licensed quota. Call this before inserting a Device
    row, with current_count = today's count for that vendor_type.
    """
    license_row = get_license(db)
    if license_row is None or compute_status(license_row) not in (LicenseStatus.ACTIVE, LicenseStatus.GRACE):
        raise LicenseRequiredError("لایسنس فعال یافت نشد یا منقضی شده است")

    limit = {
        "fortigate": license_row.fortigate_max_devices,
        "fortiweb": license_row.fortiweb_max_devices,
    }.get(vendor_type)

    if limit is None:
        raise LicenseRequiredError(f"نوع دستگاه ناشناخته برای بررسی لایسنس: {vendor_type}")

    if current_count >= limit:
        raise LicenseRequiredError(
            f"سقف لایسنس برای {vendor_type} ({limit} دستگاه) پر شده است. برای افزایش سقف با پشتیبانی تماس بگیرید."
        )


def enforce_2fa_seat_quota(db: Session, current_count: int) -> None:
    license_row = get_license(db)
    if license_row is None or compute_status(license_row) not in (LicenseStatus.ACTIVE, LicenseStatus.GRACE):
        raise LicenseRequiredError("لایسنس فعال یافت نشد یا منقضی شده است")

    if license_row.sms2fa_max_users <= 0:
        raise LicenseRequiredError("ماژول احراز هویت پیامکی برای این لایسنس فعال نشده است")

    if current_count >= license_row.sms2fa_max_users:
        raise LicenseRequiredError(
            f"سقف کاربران احراز هویت پیامکی ({license_row.sms2fa_max_users} کاربر) پر شده است."
        )
