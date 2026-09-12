import uuid

from fastapi import APIRouter, Depends, HTTPException, Response, status
from sqlalchemy.orm import Session

from app.api.v1.deps import require_permission
from app.db.session import get_db
from app.domains.backups import service
from app.domains.backups.schemas import BackupOut, RestoreRequest
from app.domains.devices.drivers.base import DeviceConnectionError
from app.domains.devices.service import get_device
from app.domains.identity.models import User

router = APIRouter(prefix="/devices/{device_id}/backups", tags=["backups"])


def _get_device_or_404(db: Session, device_id: uuid.UUID):
    device = get_device(db, device_id)
    if device is None:
        raise HTTPException(status_code=status.HTTP_404_NOT_FOUND, detail="دستگاه یافت نشد")
    return device


def _get_backup_or_404(db: Session, device_id: uuid.UUID, backup_id: uuid.UUID):
    backup = service.get_backup(db, backup_id)
    if backup is None or backup.device_id != device_id:
        raise HTTPException(status_code=status.HTTP_404_NOT_FOUND, detail="بکاپ یافت نشد")
    return backup


@router.post("", response_model=BackupOut, status_code=status.HTTP_201_CREATED)
def create_backup(
    device_id: uuid.UUID,
    db: Session = Depends(get_db),
    user: User = Depends(require_permission("backups:create")),
) -> BackupOut:
    device = _get_device_or_404(db, device_id)
    backup = service.create_backup(db, device, actor=user.username)
    return BackupOut.model_validate(backup)


@router.get("", response_model=list[BackupOut])
def list_backups(
    device_id: uuid.UUID,
    db: Session = Depends(get_db),
    _=Depends(require_permission("backups:read")),
) -> list[BackupOut]:
    _get_device_or_404(db, device_id)
    return [BackupOut.model_validate(b) for b in service.list_backups(db, device_id)]


@router.get("/{backup_id}/download")
def download_backup(
    device_id: uuid.UUID,
    backup_id: uuid.UUID,
    db: Session = Depends(get_db),
    _=Depends(require_permission("backups:read")),
) -> Response:
    backup = _get_backup_or_404(db, device_id, backup_id)
    try:
        content = service.get_decrypted_content(backup)
    except ValueError as exc:
        raise HTTPException(status_code=status.HTTP_400_BAD_REQUEST, detail=str(exc)) from exc

    return Response(
        content=content,
        media_type="application/octet-stream",
        headers={"Content-Disposition": f"attachment; filename=backup-{backup.taken_at.date()}.conf"},
    )


@router.delete("/{backup_id}", status_code=status.HTTP_204_NO_CONTENT)
def delete_backup(
    device_id: uuid.UUID,
    backup_id: uuid.UUID,
    db: Session = Depends(get_db),
    user: User = Depends(require_permission("backups:create")),
) -> None:
    backup = _get_backup_or_404(db, device_id, backup_id)
    service.delete_backup(db, backup, actor=user.username)


@router.post("/{backup_id}/restore")
def restore_backup(
    device_id: uuid.UUID,
    backup_id: uuid.UUID,
    payload: RestoreRequest,
    db: Session = Depends(get_db),
    user: User = Depends(require_permission("backups:restore")),
) -> dict:
    device = _get_device_or_404(db, device_id)
    backup = _get_backup_or_404(db, device_id, backup_id)

    try:
        service.restore_backup(db, device, backup, actor=user.username, force=payload.force)
    except service.RestoreBlockedError as exc:
        raise HTTPException(status_code=status.HTTP_409_CONFLICT, detail=str(exc)) from exc
    except DeviceConnectionError as exc:
        raise HTTPException(status_code=status.HTTP_502_BAD_GATEWAY, detail=str(exc)) from exc
    except ValueError as exc:
        raise HTTPException(status_code=status.HTTP_400_BAD_REQUEST, detail=str(exc)) from exc

    return {"success": True}
