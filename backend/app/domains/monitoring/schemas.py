import uuid
from datetime import datetime

from pydantic import BaseModel

from app.domains.monitoring.models import AlertSeverity, AlertSource


class SnmpMetricOut(BaseModel):
    id: uuid.UUID
    collected_at: datetime
    cpu_percent: float | None
    memory_percent: float | None
    session_count: int | None
    sys_uptime_ticks: int | None

    model_config = {"from_attributes": True}


class SnmpAlertOut(BaseModel):
    id: uuid.UUID
    device_id: uuid.UUID
    received_at: datetime
    source: AlertSource
    severity: AlertSeverity
    trap_oid: str
    message: str
    acknowledged: bool

    model_config = {"from_attributes": True}
