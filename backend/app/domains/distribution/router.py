import uuid

from fastapi import APIRouter, Depends, File, Form, HTTPException, UploadFile, status
from sqlalchemy.orm import Session

from app.api.v1.deps import require_permission
from app.core.config import get_settings
from app.db.session import get_db
from app.domains.devices.models import VendorType
from app.domains.devices.service import get_device
from app.domains.distribution import service
from app.domains.distribution.ftp_source import FtpSyncError, sync_from_ftp
from app.domains.distribution.schemas import FtpSourceConfigOut, FtpSourceConfigUpdate, PackageOut, PushRecordOut
from app.domains.identity.models import User

router = APIRouter(tags=["distribution"])


@router.get("/distribution/ftp-source", response_model=FtpSourceConfigOut)
def get_ftp_source(
    db: Session = Depends(get_db),
    _=Depends(require_permission("packages:read")),
) -> FtpSourceConfigOut:
    config = service.get_ftp_config(db)
    if config is None:
        return FtpSourceConfigOut(use_custom=False, host="", port=21, username="", remote_path="/")
    return FtpSourceConfigOut(
        use_custom=config.use_custom,
        host=config.host,
        port=config.port,
        username=config.username,
        remote_path=config.remote_path,
    )


@router.put("/distribution/ftp-source", response_model=FtpSourceConfigOut)
def update_ftp_source(
    payload: FtpSourceConfigUpdate,
    db: Session = Depends(get_db),
    _=Depends(require_permission("packages:manage")),
) -> FtpSourceConfigOut:
    config = service.set_ftp_config(
        db,
        use_custom=payload.use_custom,
        host=payload.host,
        port=payload.port,
        username=payload.username,
        password=payload.password,
        remote_path=payload.remote_path,
    )
    return FtpSourceConfigOut(
        use_custom=config.use_custom,
        host=config.host,
        port=config.port,
        username=config.username,
        remote_path=config.remote_path,
    )


@router.post("/distribution/ftp-source/sync-now")
def sync_now(
    db: Session = Depends(get_db),
    _=Depends(require_permission("packages:manage")),
) -> dict:
    try:
        return sync_from_ftp(db, packages_root=get_settings().packages_root)
    except FtpSyncError as exc:
        raise HTTPException(status_code=status.HTTP_502_BAD_GATEWAY, detail=str(exc)) from exc


@router.get("/distribution/packages", response_model=list[PackageOut])
def list_packages(
    vendor_type: VendorType | None = None,
    db: Session = Depends(get_db),
    _=Depends(require_permission("packages:read")),
) -> list[PackageOut]:
    return [PackageOut.model_validate(p) for p in service.list_packages(db, vendor_type=vendor_type)]


@router.post("/distribution/packages/upload", response_model=PackageOut, status_code=status.HTTP_201_CREATED)
async def upload_package(
    vendor_type: VendorType = Form(...),
    package_type: str = Form(...),
    file: UploadFile = File(...),
    db: Session = Depends(get_db),
    user: User = Depends(require_permission("packages:manage")),
) -> PackageOut:
    content = await file.read()
    package = service.save_uploaded_package(
        db,
        vendor_type=vendor_type,
        package_type=package_type,
        filename=file.filename or "package.bin",
        content=content,
        actor=user.username,
    )
    return PackageOut.model_validate(package)


@router.delete("/distribution/packages/{package_id}", status_code=status.HTTP_204_NO_CONTENT)
def delete_package(
    package_id: uuid.UUID,
    db: Session = Depends(get_db),
    user: User = Depends(require_permission("packages:manage")),
) -> None:
    package = service.get_package(db, package_id)
    if package is None:
        raise HTTPException(status_code=status.HTTP_404_NOT_FOUND, detail="بسته یافت نشد")
    service.delete_package(db, package, actor=user.username)


@router.post("/devices/{device_id}/push/{package_id}", response_model=PushRecordOut)
def push_package(
    device_id: uuid.UUID,
    package_id: uuid.UUID,
    db: Session = Depends(get_db),
    user: User = Depends(require_permission("packages:manage")),
) -> PushRecordOut:
    device = get_device(db, device_id)
    if device is None:
        raise HTTPException(status_code=status.HTTP_404_NOT_FOUND, detail="دستگاه یافت نشد")
    package = service.get_package(db, package_id)
    if package is None:
        raise HTTPException(status_code=status.HTTP_404_NOT_FOUND, detail="بسته یافت نشد")

    try:
        record = service.push_package_to_device(db, device, package, actor=user.username)
    except service.DistributionError as exc:
        raise HTTPException(status_code=status.HTTP_400_BAD_REQUEST, detail=str(exc)) from exc
    return PushRecordOut.model_validate(record)


@router.get("/devices/{device_id}/push-history", response_model=list[PushRecordOut])
def push_history(
    device_id: uuid.UUID,
    db: Session = Depends(get_db),
    _=Depends(require_permission("packages:read")),
) -> list[PushRecordOut]:
    return [PushRecordOut.model_validate(r) for r in service.list_push_history(db, device_id)]
