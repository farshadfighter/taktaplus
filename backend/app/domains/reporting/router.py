from fastapi import APIRouter, Depends
from sqlalchemy.orm import Session

from app.api.v1.deps import require_permission
from app.db.session import get_db
from app.domains.reporting.schemas import FleetStatusRow
from app.domains.reporting.service import build_fleet_status

router = APIRouter(prefix="/reports", tags=["reports"])


@router.get("/fleet-status", response_model=list[FleetStatusRow])
def fleet_status(
    db: Session = Depends(get_db),
    _=Depends(require_permission("reports:read")),
) -> list[FleetStatusRow]:
    return build_fleet_status(db)
