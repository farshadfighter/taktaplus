"""CRUD for 2FA users, RADIUS clients (NAS), and SMS gateway config - the
REST-managed side of this domain. The actual authentication algorithm lives
in service.py and is only ever driven by the RADIUS server process.
"""

from __future__ import annotations

from sqlalchemy import func, select
from sqlalchemy.orm import Session

from app.core.security import encrypt_secret
from app.domains.audit.service import record_audit_event
from app.domains.licensing.service import enforce_2fa_seat_quota, lock_license_for_update
from app.domains.radius.models import OtpChallenge, RadiusClient, SmsGatewayConfig, SmsProvider, TwoFactorUser


def count_two_factor_users(db: Session) -> int:
    return db.scalar(select(func.count()).select_from(TwoFactorUser)) or 0


def list_two_factor_users(db: Session) -> list[TwoFactorUser]:
    return list(db.scalars(select(TwoFactorUser).order_by(TwoFactorUser.created_at.asc())))


def get_two_factor_user(db: Session, user_id) -> TwoFactorUser | None:
    return db.get(TwoFactorUser, user_id)


def create_two_factor_user(
    db: Session, *, username: str, password: str, mobile_number: str, actor: str
) -> TwoFactorUser:
    # Lock before counting, not after, so a concurrent create is blocked
    # until this one commits (or rolls back) rather than reading the same
    # stale count - see lock_license_for_update's docstring.
    lock_license_for_update(db)
    enforce_2fa_seat_quota(db, count_two_factor_users(db))

    user = TwoFactorUser(
        username=username,
        encrypted_password=encrypt_secret(password),
        encrypted_mobile_number=encrypt_secret(mobile_number),
    )
    db.add(user)
    db.commit()
    db.refresh(user)
    record_audit_event(db, actor=actor, action="radius_user.create", target=username)
    return user


def set_two_factor_user_enabled(db: Session, user: TwoFactorUser, enabled: bool, *, actor: str) -> TwoFactorUser:
    user.enabled = enabled
    if enabled:
        user.failed_attempts = 0
        user.locked_until = None
    db.commit()
    db.refresh(user)
    record_audit_event(db, actor=actor, action="radius_user.set_enabled", target=user.username, details=str(enabled))
    return user


def unlock_two_factor_user(db: Session, user: TwoFactorUser, *, actor: str) -> TwoFactorUser:
    user.failed_attempts = 0
    user.locked_until = None
    db.commit()
    db.refresh(user)
    record_audit_event(db, actor=actor, action="radius_user.unlock", target=user.username)
    return user


def delete_two_factor_user(db: Session, user: TwoFactorUser, *, actor: str) -> None:
    record_audit_event(db, actor=actor, action="radius_user.delete", target=user.username)
    db.delete(user)
    db.commit()


def list_radius_clients(db: Session) -> list[RadiusClient]:
    return list(db.scalars(select(RadiusClient).order_by(RadiusClient.created_at.asc())))


def create_radius_client(db: Session, *, name: str, nas_ip: str, shared_secret: str, actor: str) -> RadiusClient:
    client = RadiusClient(name=name, nas_ip=nas_ip, encrypted_shared_secret=encrypt_secret(shared_secret))
    db.add(client)
    db.commit()
    db.refresh(client)
    record_audit_event(db, actor=actor, action="radius_client.create", target=nas_ip, details=name)
    return client


def get_radius_client(db: Session, client_id) -> RadiusClient | None:
    return db.get(RadiusClient, client_id)


def delete_radius_client(db: Session, client: RadiusClient, *, actor: str) -> None:
    record_audit_event(db, actor=actor, action="radius_client.delete", target=client.nas_ip)
    db.delete(client)
    db.commit()


def get_sms_config(db: Session) -> SmsGatewayConfig | None:
    return db.scalar(select(SmsGatewayConfig).limit(1))


def set_sms_config(
    db: Session,
    *,
    provider: SmsProvider,
    kavenegar_api_key: str | None,
    kavenegar_sender: str | None,
    generic_method: str,
    generic_url_template: str | None,
    generic_auth_header_name: str | None,
    generic_auth_header_value: str | None,
    actor: str,
) -> SmsGatewayConfig:
    config = get_sms_config(db) or SmsGatewayConfig()
    config.provider = provider
    config.kavenegar_sender = kavenegar_sender
    config.generic_method = generic_method
    config.generic_url_template = generic_url_template
    config.generic_auth_header_name = generic_auth_header_name
    if kavenegar_api_key:
        config.encrypted_kavenegar_api_key = encrypt_secret(kavenegar_api_key)
    if generic_auth_header_value:
        config.encrypted_generic_auth_header_value = encrypt_secret(generic_auth_header_value)

    db.add(config)
    db.commit()
    db.refresh(config)
    record_audit_event(db, actor=actor, action="sms_gateway.update", target=provider.value)
    return config


def prune_expired_challenges(db: Session) -> int:
    """Housekeeping: delete resolved/expired OTP challenges past retention.
    Not security-critical (expired ones are already unusable), just keeps
    the table from growing forever.
    """
    from datetime import datetime, timedelta, timezone

    cutoff = datetime.now(timezone.utc) - timedelta(days=1)
    stale = list(
        db.scalars(select(OtpChallenge).where(OtpChallenge.expires_at < cutoff))
    )
    for challenge in stale:
        db.delete(challenge)
    if stale:
        db.commit()
    return len(stale)
