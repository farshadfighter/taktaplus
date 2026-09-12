from collections.abc import Generator

from fastapi import Depends, HTTPException, status
from fastapi.security import OAuth2PasswordBearer
from sqlalchemy.orm import Session

from app.core.auth import decode_token
from app.db.session import get_db
from app.domains.identity.models import User
from app.domains.identity.seed_roles import has_permission
from app.domains.identity.service import get_user_by_id

oauth2_scheme = OAuth2PasswordBearer(tokenUrl="/api/v1/auth/login")


def get_current_user(
    token: str = Depends(oauth2_scheme), db: Session = Depends(get_db)
) -> User:
    credentials_error = HTTPException(
        status_code=status.HTTP_401_UNAUTHORIZED,
        detail="اعتبار ورود نامعتبر است",
        headers={"WWW-Authenticate": "Bearer"},
    )
    try:
        payload = decode_token(token)
    except ValueError as exc:
        raise credentials_error from exc

    if payload.get("type") != "access":
        raise credentials_error

    user = get_user_by_id(db, payload["sub"])
    if user is None or not user.is_active:
        raise credentials_error
    return user


def require_permission(permission: str):
    def _checker(user: User = Depends(get_current_user)) -> User:
        if not has_permission(user.role.permissions, permission):
            raise HTTPException(
                status_code=status.HTTP_403_FORBIDDEN,
                detail=f"دسترسی لازم برای این عملیات ({permission}) را ندارید",
            )
        return user

    return _checker
