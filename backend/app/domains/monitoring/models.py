import enum
import uuid
from datetime import datetime

from sqlalchemy import Boolean, DateTime, Enum, ForeignKey, Float, Integer, String, Text, func
from sqlalchemy.orm import Mapped, mapped_column

from app.db.base import Base
from app.db.types import GUID


class AlertSeverity(str, enum.Enum):
    INFO = "info"
    WARNING = "warning"
    CRITICAL = "critical"


class AlertSource(str, enum.Enum):
    TRAP = "trap"
    POLL_THRESHOLD = "poll_threshold"
    POLL_UNREACHABLE = "poll_unreachable"


class SnmpMetricSample(Base):
    """One row per successful poll. Wide-table (not EAV) since the metric
    set is small and fixed for phase 3 - simpler to query for a dashboard
    than a generic metric_name/value table at this scale.
    """

    __tablename__ = "snmp_metric_samples"

    id: Mapped[uuid.UUID] = mapped_column(GUID(), primary_key=True, default=uuid.uuid4)
    device_id: Mapped[uuid.UUID] = mapped_column(GUID(), ForeignKey("devices.id", ondelete="CASCADE"))
    collected_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), server_default=func.now())

    cpu_percent: Mapped[float | None] = mapped_column(Float, nullable=True)
    memory_percent: Mapped[float | None] = mapped_column(Float, nullable=True)
    session_count: Mapped[int | None] = mapped_column(Integer, nullable=True)
    sys_uptime_ticks: Mapped[int | None] = mapped_column(Integer, nullable=True)


class SnmpAlert(Base):
    __tablename__ = "snmp_alerts"

    id: Mapped[uuid.UUID] = mapped_column(GUID(), primary_key=True, default=uuid.uuid4)
    device_id: Mapped[uuid.UUID] = mapped_column(GUID(), ForeignKey("devices.id", ondelete="CASCADE"))
    received_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), server_default=func.now())

    source: Mapped[AlertSource] = mapped_column(Enum(AlertSource))
    severity: Mapped[AlertSeverity] = mapped_column(Enum(AlertSeverity))
    trap_oid: Mapped[str] = mapped_column(String(128), default="")
    message: Mapped[str] = mapped_column(Text, default="")
    acknowledged: Mapped[bool] = mapped_column(Boolean, default=False)
