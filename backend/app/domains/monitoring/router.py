import uuid

from fastapi import APIRouter, Depends, HTTPException, status
from sqlalchemy.orm import Session

from app.api.v1.deps import require_permission
from app.db.session import get_db
from app.domains.devices.service import get_device
from app.domains.monitoring import service
from app.domains.monitoring.schemas import SnmpAlertOut, SnmpMetricOut
from app.domains.monitoring.snmp_client import SnmpPollError

router = APIRouter(prefix="/devices/{device_id}", tags=["monitoring"])


def _get_device_or_404(db: Session, device_id: uuid.UUID):
    device = get_device(db, device_id)
    if device is None:
        raise HTTPException(status_code=status.HTTP_404_NOT_FOUND, detail="دستگاه یافت نشد")
    return device


@router.get("/metrics", response_model=list[SnmpMetricOut])
def list_metrics(
    device_id: uuid.UUID,
    db: Session = Depends(get_db),
    _=Depends(require_permission("devices:read")),
) -> list[SnmpMetricOut]:
    _get_device_or_404(db, device_id)
    return [SnmpMetricOut.model_validate(m) for m in service.list_metrics(db, device_id)]


@router.post("/metrics/poll-now", response_model=SnmpMetricOut)
def poll_now(
    device_id: uuid.UUID,
    db: Session = Depends(get_db),
    _=Depends(require_permission("devices:write")),
) -> SnmpMetricOut:
    device = _get_device_or_404(db, device_id)
    try:
        sample = service.poll_now(db, device)
    except service.SnmpNotConfiguredError as exc:
        raise HTTPException(status_code=status.HTTP_400_BAD_REQUEST, detail=str(exc)) from exc
    except SnmpPollError as exc:
        raise HTTPException(status_code=status.HTTP_502_BAD_GATEWAY, detail=str(exc)) from exc
    return SnmpMetricOut.model_validate(sample)


@router.get("/alerts", response_model=list[SnmpAlertOut])
def list_alerts(
    device_id: uuid.UUID,
    db: Session = Depends(get_db),
    _=Depends(require_permission("devices:read")),
) -> list[SnmpAlertOut]:
    _get_device_or_404(db, device_id)
    return [SnmpAlertOut.model_validate(a) for a in service.list_alerts(db, device_id)]


@router.post("/alerts/{alert_id}/ack", response_model=SnmpAlertOut)
def acknowledge_alert(
    device_id: uuid.UUID,
    alert_id: uuid.UUID,
    db: Session = Depends(get_db),
    _=Depends(require_permission("devices:write")),
) -> SnmpAlertOut:
    alert = service.get_alert(db, alert_id)
    if alert is None or alert.device_id != device_id:
        raise HTTPException(status_code=status.HTTP_404_NOT_FOUND, detail="هشدار یافت نشد")
    return SnmpAlertOut.model_validate(service.acknowledge_alert(db, alert))
