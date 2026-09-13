from fastapi import APIRouter, Depends, HTTPException, status
from sqlalchemy.orm import Session

from app.api.v1.deps import get_current_user, require_permission
from app.db.session import get_db
from app.domains.licensing import service
from app.domains.licensing.client import LicenseNotFound, LicenseServerError, LicenseSuspended
from app.domains.licensing.models import LicenseStatus
from app.domains.licensing.schemas import ActivateRequest, LicenseStatusOut

router = APIRouter(prefix="/license", tags=["license"])


@router.post("/activate", response_model=LicenseStatusOut)
def activate_license(
    payload: ActivateRequest,
    db: Session = Depends(get_db),
    _=Depends(require_permission("license:manage")),
) -> LicenseStatusOut:
    try:
        license_row = service.activate(db, payload.customer_key)
    except LicenseNotFound as exc:
        raise HTTPException(status_code=status.HTTP_404_NOT_FOUND, detail=str(exc)) from exc
    except LicenseSuspended as exc:
        raise HTTPException(status_code=status.HTTP_403_FORBIDDEN, detail=str(exc)) from exc
    except LicenseServerError as exc:
        raise HTTPException(status_code=status.HTTP_502_BAD_GATEWAY, detail=str(exc)) from exc
    return _to_out(license_row)


@router.get("/status", response_model=LicenseStatusOut)
def license_status(db: Session = Depends(get_db), _=Depends(get_current_user)) -> LicenseStatusOut:
    license_row = service.get_license(db)
    if license_row is None:
        return LicenseStatusOut(
            status=LicenseStatus.UNACTIVATED.value,
            fortigate_max_devices=0,
            fortiweb_max_devices=0,
            sms2fa_max_users=0,
            expires_at=None,
            days_until_expiry=None,
            expiry_warning=False,
            last_heartbeat_at=None,
            last_heartbeat_ok=False,
        )
    return _to_out(license_row)


def _to_out(license_row) -> LicenseStatusOut:
    return LicenseStatusOut(
        status=service.compute_status(license_row).value,
        fortigate_max_devices=license_row.fortigate_max_devices,
        fortiweb_max_devices=license_row.fortiweb_max_devices,
        sms2fa_max_users=license_row.sms2fa_max_users,
        expires_at=license_row.expires_at,
        days_until_expiry=service.days_until_expiry(license_row),
        expiry_warning=service.should_warn_expiry(license_row),
        last_heartbeat_at=license_row.last_heartbeat_at,
        last_heartbeat_ok=license_row.last_heartbeat_ok,
    )
