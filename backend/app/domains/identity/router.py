from fastapi import APIRouter, Depends, HTTPException, status
from sqlalchemy.orm import Session

from app.api.v1.deps import get_current_user
from app.core.auth import create_access_token, create_refresh_token
from app.db.session import get_db
from app.domains.audit.service import record_audit_event
from app.domains.identity.models import User
from app.domains.identity.schemas import LoginRequest, TokenResponse, UserOut
from app.domains.identity.service import authenticate_user

router = APIRouter(prefix="/auth", tags=["auth"])


@router.post("/login", response_model=TokenResponse)
def login(payload: LoginRequest, db: Session = Depends(get_db)) -> TokenResponse:
    user = authenticate_user(db, payload.username, payload.password)
    if user is None:
        record_audit_event(db, actor=payload.username, action="auth.login.failed", target=payload.username)
        raise HTTPException(status_code=status.HTTP_401_UNAUTHORIZED, detail="نام کاربری یا رمز عبور اشتباه است")

    record_audit_event(db, actor=user.username, action="auth.login.success", target=user.username)
    return TokenResponse(
        access_token=create_access_token(str(user.id)),
        refresh_token=create_refresh_token(str(user.id)),
    )


@router.get("/me", response_model=UserOut)
def me(user: User = Depends(get_current_user)) -> UserOut:
    return UserOut(
        id=user.id,
        username=user.username,
        full_name=user.full_name,
        role=user.role.name,
        is_active=user.is_active,
    )
