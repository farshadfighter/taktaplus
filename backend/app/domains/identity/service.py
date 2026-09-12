from sqlalchemy import select
from sqlalchemy.orm import Session, joinedload

from app.core.auth import verify_password
from app.domains.identity.models import User


def authenticate_user(db: Session, username: str, password: str) -> User | None:
    user = db.scalar(
        select(User).options(joinedload(User.role)).where(User.username == username)
    )
    if user is None or not user.is_active:
        return None
    if not verify_password(password, user.password_hash):
        return None
    return user


def get_user_by_id(db: Session, user_id: str) -> User | None:
    return db.scalar(
        select(User).options(joinedload(User.role)).where(User.id == user_id)
    )
