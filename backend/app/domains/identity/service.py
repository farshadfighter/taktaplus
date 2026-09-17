from datetime import datetime, timedelta, timezone

from sqlalchemy import select
from sqlalchemy.orm import Session, joinedload

from app.core.auth import verify_password
from app.core.config import get_settings
from app.domains.identity.models import User


def _now() -> datetime:
    return datetime.now(timezone.utc)


def _aware(dt: datetime) -> datetime:
    return dt if dt.tzinfo is not None else dt.replace(tzinfo=timezone.utc)


def is_locked(user: User) -> bool:
    return user.locked_until is not None and _aware(user.locked_until) > _now()


def _register_failure(db: Session, user: User) -> None:
    settings = get_settings()
    user.failed_attempts += 1
    if user.failed_attempts >= settings.login_lockout_threshold:
        user.locked_until = _now() + timedelta(minutes=settings.login_lockout_minutes)
        user.failed_attempts = 0
    db.commit()


def _reset_failures(db: Session, user: User) -> None:
    if user.failed_attempts or user.locked_until:
        user.failed_attempts = 0
        user.locked_until = None
        db.commit()


class AccountLockedError(Exception):
    pass


def authenticate_user(db: Session, username: str, password: str) -> User | None:
    user = db.scalar(
        select(User).options(joinedload(User.role)).where(User.username == username)
    )
    if user is None or not user.is_active:
        return None

    if is_locked(user):
        raise AccountLockedError()

    if not verify_password(password, user.password_hash):
        _register_failure(db, user)
        return None

    _reset_failures(db, user)
    return user


def get_user_by_id(db: Session, user_id: str) -> User | None:
    return db.scalar(
        select(User).options(joinedload(User.role)).where(User.id == user_id)
    )
