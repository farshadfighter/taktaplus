"""Unified RADIUS authentication algorithm covering both ways FortiGate can
present this to taktaplus - see docs/radius-2fa.md for the full reasoning.

In short: SSL VPN and admin GUI/SSH logins forward RADIUS Access-Challenge
to the end user, so a clean two-step exchange works (password, then OTP).
IPsec VPN does *not* forward challenges, so its only usable pattern is
"submit password+OTP concatenated in one request" - which requires the OTP
to already have been sent by an *earlier* attempt. Both cases are handled
by the same stateless-per-request algorithm below rather than a per-NAS
"mode" flag, because the same FortiGate can serve both a challenge-capable
service (SSL VPN) and a non-capable one (IPsec) at once.
"""

from __future__ import annotations

import hashlib
import hmac
import secrets
from dataclasses import dataclass
from datetime import datetime, timedelta, timezone

from sqlalchemy import select
from sqlalchemy.orm import Session

from app.core.config import get_settings
from app.core.security import decrypt_secret
from app.domains.radius.models import OtpChallenge, SmsGatewayConfig, TwoFactorUser
from app.domains.radius.sms_gateway import SmsSendError, send_otp_sms


@dataclass
class AuthResult:
    result: str  # "accept" | "challenge" | "reject"
    reply_message: str = ""
    state_token: str | None = None


def _now() -> datetime:
    return datetime.now(timezone.utc)


def _aware(dt: datetime) -> datetime:
    return dt if dt.tzinfo is not None else dt.replace(tzinfo=timezone.utc)


def _hash_code(code: str) -> str:
    return hashlib.sha256(code.encode("utf-8")).hexdigest()


def get_user_by_username(db: Session, username: str) -> TwoFactorUser | None:
    return db.scalar(select(TwoFactorUser).where(TwoFactorUser.username == username))


def get_linked_usernames(db: Session, usernames: list[str]) -> set[str]:
    """Which of these usernames already have a TwoFactorUser - one query
    for the whole batch, used by the device local-users listing instead of
    a per-candidate get_user_by_username lookup (avoids an N+1 when a
    device has many local accounts).
    """
    if not usernames:
        return set()
    return set(db.scalars(select(TwoFactorUser.username).where(TwoFactorUser.username.in_(usernames))))


def get_sms_config(db: Session) -> SmsGatewayConfig | None:
    return db.scalar(select(SmsGatewayConfig).limit(1))


def _is_locked(user: TwoFactorUser) -> bool:
    return user.locked_until is not None and _aware(user.locked_until) > _now()


def _register_failure(db: Session, user: TwoFactorUser) -> None:
    settings = get_settings()
    user.failed_attempts += 1
    if user.failed_attempts >= settings.otp_lockout_threshold:
        user.locked_until = _now() + timedelta(minutes=settings.otp_lockout_minutes)
        user.failed_attempts = 0
    db.commit()


def _reset_failures(db: Session, user: TwoFactorUser) -> None:
    user.failed_attempts = 0
    user.locked_until = None
    db.commit()


def _check_and_consume_rate_limit(db: Session, user: TwoFactorUser) -> bool:
    settings = get_settings()
    now = _now()
    if user.otp_window_started_at is None or now - _aware(user.otp_window_started_at) > timedelta(hours=1):
        user.otp_window_started_at = now
        user.otp_sent_in_window = 0

    if user.otp_sent_in_window >= settings.otp_sms_rate_limit_per_hour:
        db.commit()
        return False

    user.otp_sent_in_window += 1
    db.commit()
    return True


def _find_active_challenge_by_state(db: Session, username: str, state_token: str) -> OtpChallenge | None:
    challenge = db.scalar(
        select(OtpChallenge).where(OtpChallenge.state_token == state_token, OtpChallenge.username == username)
    )
    if challenge is None or challenge.consumed or _aware(challenge.expires_at) <= _now():
        return None
    return challenge


def _find_matching_active_challenge(db: Session, username: str, code: str) -> OtpChallenge | None:
    code_hash = _hash_code(code)
    candidates = db.scalars(
        select(OtpChallenge).where(
            OtpChallenge.username == username,
            OtpChallenge.consumed.is_(False),
        )
    )
    for challenge in candidates:
        if _aware(challenge.expires_at) > _now() and hmac.compare_digest(challenge.code_hash, code_hash):
            return challenge
    return None


def _issue_challenge(db: Session, user: TwoFactorUser) -> AuthResult:
    settings = get_settings()

    if not _check_and_consume_rate_limit(db, user):
        return AuthResult(result="reject", reply_message="تعداد درخواست کد پیامکی بیش از حد مجاز است")

    sms_config = get_sms_config(db)
    if sms_config is None:
        return AuthResult(result="reject", reply_message="سرویس پیامکی برای این نصب پیکربندی نشده است")

    code = "".join(str(secrets.randbelow(10)) for _ in range(settings.otp_length))
    state_token = secrets.token_urlsafe(32)
    challenge = OtpChallenge(
        username=user.username,
        state_token=state_token,
        code_hash=_hash_code(code),
        expires_at=_now() + timedelta(seconds=settings.otp_expiry_seconds),
    )

    try:
        send_otp_sms(sms_config, mobile_number=decrypt_secret(user.encrypted_mobile_number), code=code)
    except SmsSendError as exc:
        return AuthResult(result="reject", reply_message=f"ارسال پیامک ناموفق بود: {exc}")

    db.add(challenge)
    db.commit()
    return AuthResult(result="challenge", reply_message="کد پیامک‌شده را وارد کنید", state_token=state_token)


def authenticate(db: Session, *, username: str, password: str, state_token: str | None = None) -> AuthResult:
    user = get_user_by_username(db, username)
    if user is None or not user.enabled:
        return AuthResult(result="reject", reply_message="کاربر یافت نشد یا غیرفعال است")

    if _is_locked(user):
        return AuthResult(result="reject", reply_message="حساب به دلیل تلاش‌های ناموفق مکرر قفل شده است")

    primary_password = decrypt_secret(user.encrypted_password)

    # Continuing an in-progress challenge-response exchange (SSL VPN, admin login).
    if state_token:
        challenge = _find_active_challenge_by_state(db, username, state_token)
        if challenge is None:
            return AuthResult(result="reject", reply_message="این درخواست منقضی شده یا نامعتبر است")

        if hmac.compare_digest(challenge.code_hash, _hash_code(password)):
            challenge.consumed = True
            db.commit()
            _reset_failures(db, user)
            return AuthResult(result="accept")

        challenge.attempt_count += 1
        if challenge.attempt_count >= get_settings().otp_max_attempts_per_challenge:
            challenge.consumed = True
        db.commit()
        _register_failure(db, user)
        return AuthResult(result="reject", reply_message="کد وارد شده اشتباه است")

    # Fresh request, no state - could be a concatenated password+OTP (IPsec)
    # from a client that already received a code via an earlier attempt.
    if len(password) > len(primary_password) and hmac.compare_digest(
        password[: len(primary_password)], primary_password
    ):
        remainder = password[len(primary_password):]
        challenge = _find_matching_active_challenge(db, username, remainder)
        if challenge is not None:
            challenge.consumed = True
            db.commit()
            _reset_failures(db, user)
            return AuthResult(result="accept")

    # Exact primary password, nothing appended - start a new challenge.
    if hmac.compare_digest(password, primary_password):
        return _issue_challenge(db, user)

    _register_failure(db, user)
    return AuthResult(result="reject", reply_message="نام کاربری یا رمز عبور اشتباه است")
