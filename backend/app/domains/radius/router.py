import uuid

from fastapi import APIRouter, Depends, HTTPException, status
from sqlalchemy.orm import Session

from app.api.v1.deps import require_permission
from app.db.session import get_db
from app.domains.devices.drivers import DeviceConnectionError, get_driver
from app.domains.devices.service import get_device
from app.domains.identity.models import User
from app.domains.radius import admin_service
from app.domains.radius.schemas import (
    LocalUserCandidateOut,
    RadiusClientCreate,
    RadiusClientOut,
    SmsGatewayConfigOut,
    SmsGatewayConfigUpdate,
    TestSmsRequest,
    TwoFactorUserCreate,
    TwoFactorUserOut,
)
from app.domains.radius.service import get_user_by_username
from app.domains.radius.sms_gateway import SmsSendError, send_otp_sms

router = APIRouter(tags=["radius"])


@router.get("/devices/{device_id}/local-users", response_model=list[LocalUserCandidateOut])
def list_device_local_users(
    device_id: uuid.UUID,
    db: Session = Depends(get_db),
    _=Depends(require_permission("radius:read")),
) -> list[LocalUserCandidateOut]:
    """Existing local admin/VPN accounts already configured on the device -
    lets an operator pick a username for 2FA instead of retyping it (and
    risking a typo/drift against what's actually on the device). The
    primary password still can't come from here (Fortinet never exposes
    it), so create_two_factor_user still needs it typed in separately.
    """
    device = get_device(db, device_id)
    if device is None:
        raise HTTPException(status_code=status.HTTP_404_NOT_FOUND, detail="دستگاه یافت نشد")

    driver = get_driver(device)
    try:
        candidates = driver.list_local_users()
    except NotImplementedError as exc:
        raise HTTPException(
            status_code=status.HTTP_400_BAD_REQUEST,
            detail="این قابلیت هنوز برای این نوع تجهیز پیاده‌سازی نشده است",
        ) from exc
    except DeviceConnectionError as exc:
        raise HTTPException(status_code=status.HTTP_502_BAD_GATEWAY, detail=str(exc)) from exc

    return [
        LocalUserCandidateOut(
            username=c.username,
            source=c.source,
            existing_mobile=c.existing_mobile,
            already_linked=get_user_by_username(db, c.username) is not None,
        )
        for c in candidates
    ]


@router.get("/radius/users", response_model=list[TwoFactorUserOut])
def list_two_factor_users(
    db: Session = Depends(get_db),
    _=Depends(require_permission("radius:read")),
) -> list[TwoFactorUserOut]:
    return [TwoFactorUserOut.model_validate(u) for u in admin_service.list_two_factor_users(db)]


@router.post("/radius/users", response_model=TwoFactorUserOut, status_code=status.HTTP_201_CREATED)
def create_two_factor_user(
    payload: TwoFactorUserCreate,
    db: Session = Depends(get_db),
    user: User = Depends(require_permission("radius:manage")),
) -> TwoFactorUserOut:
    created = admin_service.create_two_factor_user(
        db,
        username=payload.username,
        password=payload.password,
        mobile_number=payload.mobile_number,
        actor=user.username,
    )
    return TwoFactorUserOut.model_validate(created)


def _get_or_404(db: Session, user_id: uuid.UUID):
    target = admin_service.get_two_factor_user(db, user_id)
    if target is None:
        raise HTTPException(status_code=status.HTTP_404_NOT_FOUND, detail="کاربر یافت نشد")
    return target


@router.post("/radius/users/{user_id}/enable", response_model=TwoFactorUserOut)
def enable_two_factor_user(
    user_id: uuid.UUID,
    db: Session = Depends(get_db),
    user: User = Depends(require_permission("radius:manage")),
) -> TwoFactorUserOut:
    target = _get_or_404(db, user_id)
    return TwoFactorUserOut.model_validate(
        admin_service.set_two_factor_user_enabled(db, target, True, actor=user.username)
    )


@router.post("/radius/users/{user_id}/disable", response_model=TwoFactorUserOut)
def disable_two_factor_user(
    user_id: uuid.UUID,
    db: Session = Depends(get_db),
    user: User = Depends(require_permission("radius:manage")),
) -> TwoFactorUserOut:
    target = _get_or_404(db, user_id)
    return TwoFactorUserOut.model_validate(
        admin_service.set_two_factor_user_enabled(db, target, False, actor=user.username)
    )


@router.post("/radius/users/{user_id}/unlock", response_model=TwoFactorUserOut)
def unlock_two_factor_user(
    user_id: uuid.UUID,
    db: Session = Depends(get_db),
    user: User = Depends(require_permission("radius:manage")),
) -> TwoFactorUserOut:
    target = _get_or_404(db, user_id)
    return TwoFactorUserOut.model_validate(admin_service.unlock_two_factor_user(db, target, actor=user.username))


@router.delete("/radius/users/{user_id}", status_code=status.HTTP_204_NO_CONTENT)
def delete_two_factor_user(
    user_id: uuid.UUID,
    db: Session = Depends(get_db),
    user: User = Depends(require_permission("radius:manage")),
) -> None:
    target = _get_or_404(db, user_id)
    admin_service.delete_two_factor_user(db, target, actor=user.username)


@router.get("/radius/clients", response_model=list[RadiusClientOut])
def list_radius_clients(
    db: Session = Depends(get_db),
    _=Depends(require_permission("radius:read")),
) -> list[RadiusClientOut]:
    return [RadiusClientOut.model_validate(c) for c in admin_service.list_radius_clients(db)]


@router.post("/radius/clients", response_model=RadiusClientOut, status_code=status.HTTP_201_CREATED)
def create_radius_client(
    payload: RadiusClientCreate,
    db: Session = Depends(get_db),
    user: User = Depends(require_permission("radius:manage")),
) -> RadiusClientOut:
    client = admin_service.create_radius_client(
        db, name=payload.name, nas_ip=payload.nas_ip, shared_secret=payload.shared_secret, actor=user.username
    )
    return RadiusClientOut.model_validate(client)


@router.delete("/radius/clients/{client_id}", status_code=status.HTTP_204_NO_CONTENT)
def delete_radius_client(
    client_id: uuid.UUID,
    db: Session = Depends(get_db),
    user: User = Depends(require_permission("radius:manage")),
) -> None:
    client = admin_service.get_radius_client(db, client_id)
    if client is None:
        raise HTTPException(status_code=status.HTTP_404_NOT_FOUND, detail="کلاینت رادیوس یافت نشد")
    admin_service.delete_radius_client(db, client, actor=user.username)


@router.get("/radius/sms-gateway", response_model=SmsGatewayConfigOut)
def get_sms_gateway(
    db: Session = Depends(get_db),
    _=Depends(require_permission("radius:read")),
) -> SmsGatewayConfigOut:
    config = admin_service.get_sms_config(db)
    if config is None:
        return SmsGatewayConfigOut(
            provider="generic_http",
            kavenegar_sender=None,
            smsir_line_number=None,
            generic_method="GET",
            generic_url_template=None,
            generic_auth_header_name=None,
            has_kavenegar_api_key=False,
            has_smsir_api_key=False,
            has_generic_auth_header_value=False,
        )
    return SmsGatewayConfigOut(
        provider=config.provider,
        kavenegar_sender=config.kavenegar_sender,
        smsir_line_number=config.smsir_line_number,
        generic_method=config.generic_method,
        generic_url_template=config.generic_url_template,
        generic_auth_header_name=config.generic_auth_header_name,
        has_kavenegar_api_key=bool(config.encrypted_kavenegar_api_key),
        has_smsir_api_key=bool(config.encrypted_smsir_api_key),
        has_generic_auth_header_value=bool(config.encrypted_generic_auth_header_value),
    )


@router.put("/radius/sms-gateway", response_model=SmsGatewayConfigOut)
def update_sms_gateway(
    payload: SmsGatewayConfigUpdate,
    db: Session = Depends(get_db),
    user: User = Depends(require_permission("radius:manage")),
) -> SmsGatewayConfigOut:
    config = admin_service.set_sms_config(
        db,
        provider=payload.provider,
        kavenegar_api_key=payload.kavenegar_api_key,
        kavenegar_sender=payload.kavenegar_sender,
        smsir_api_key=payload.smsir_api_key,
        smsir_line_number=payload.smsir_line_number,
        generic_method=payload.generic_method,
        generic_url_template=payload.generic_url_template,
        generic_auth_header_name=payload.generic_auth_header_name,
        generic_auth_header_value=payload.generic_auth_header_value,
        actor=user.username,
    )
    return SmsGatewayConfigOut(
        provider=config.provider,
        kavenegar_sender=config.kavenegar_sender,
        smsir_line_number=config.smsir_line_number,
        generic_method=config.generic_method,
        generic_url_template=config.generic_url_template,
        generic_auth_header_name=config.generic_auth_header_name,
        has_kavenegar_api_key=bool(config.encrypted_kavenegar_api_key),
        has_smsir_api_key=bool(config.encrypted_smsir_api_key),
        has_generic_auth_header_value=bool(config.encrypted_generic_auth_header_value),
    )


@router.post("/radius/sms-gateway/test", status_code=status.HTTP_204_NO_CONTENT)
def test_sms_gateway(
    payload: TestSmsRequest,
    db: Session = Depends(get_db),
    _=Depends(require_permission("radius:manage")),
) -> None:
    config = admin_service.get_sms_config(db)
    if config is None:
        raise HTTPException(status_code=status.HTTP_400_BAD_REQUEST, detail="سرویس پیامکی پیکربندی نشده است")
    try:
        send_otp_sms(config, mobile_number=payload.mobile_number, code="12345")
    except SmsSendError as exc:
        raise HTTPException(status_code=status.HTTP_502_BAD_GATEWAY, detail=str(exc)) from exc
