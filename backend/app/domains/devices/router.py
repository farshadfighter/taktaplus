import uuid

from fastapi import APIRouter, Depends, HTTPException, status
from sqlalchemy.orm import Session

from app.api.v1.deps import require_permission
from app.db.session import get_db
from app.domains.devices import service
from app.domains.devices.schemas import DeviceCreate, DeviceOut, DeviceTestResult
from app.domains.identity.models import User
from app.domains.licensing.service import LicenseRequiredError

router = APIRouter(prefix="/devices", tags=["devices"])


def _get_device_or_404(db: Session, device_id: uuid.UUID):
    device = service.get_device(db, device_id)
    if device is None:
        raise HTTPException(status_code=status.HTTP_404_NOT_FOUND, detail="دستگاه یافت نشد")
    return device


@router.post("", response_model=DeviceOut, status_code=status.HTTP_201_CREATED)
def create_device(
    payload: DeviceCreate,
    db: Session = Depends(get_db),
    user: User = Depends(require_permission("devices:write")),
) -> DeviceOut:
    try:
        device = service.create_device(db, payload, actor=user.username)
    except LicenseRequiredError as exc:
        raise HTTPException(status_code=status.HTTP_402_PAYMENT_REQUIRED, detail=str(exc)) from exc
    return DeviceOut.model_validate(device)


@router.get("", response_model=list[DeviceOut])
def list_devices(
    db: Session = Depends(get_db),
    _=Depends(require_permission("devices:read")),
) -> list[DeviceOut]:
    return [DeviceOut.model_validate(d) for d in service.list_devices(db)]


@router.get("/{device_id}", response_model=DeviceOut)
def get_device(
    device_id: uuid.UUID,
    db: Session = Depends(get_db),
    _=Depends(require_permission("devices:read")),
) -> DeviceOut:
    return DeviceOut.model_validate(_get_device_or_404(db, device_id))


@router.delete("/{device_id}", status_code=status.HTTP_204_NO_CONTENT)
def delete_device(
    device_id: uuid.UUID,
    db: Session = Depends(get_db),
    user: User = Depends(require_permission("devices:write")),
) -> None:
    device = _get_device_or_404(db, device_id)
    service.delete_device(db, device, actor=user.username)


@router.post("/{device_id}/test-connection", response_model=DeviceTestResult)
def test_connection(
    device_id: uuid.UUID,
    db: Session = Depends(get_db),
    user: User = Depends(require_permission("devices:write")),
) -> DeviceTestResult:
    device = _get_device_or_404(db, device_id)
    device = service.test_connection(db, device, actor=user.username)
    return DeviceTestResult(
        success=device.status.value == "online",
        status=device.status,
        firmware_version=device.firmware_version,
        serial_number=device.serial_number,
        reported_hostname=device.reported_hostname,
        error=device.last_error,
    )
