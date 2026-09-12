import enum
import uuid
from datetime import datetime

from sqlalchemy import Boolean, DateTime, Enum, Integer, String, Text, func
from sqlalchemy.orm import Mapped, mapped_column

from app.db.base import Base
from app.db.types import GUID


class LicenseStatus(str, enum.Enum):
    UNACTIVATED = "unactivated"
    ACTIVE = "active"
    GRACE = "grace"  # license server unreachable, running on cached token
    EXPIRED = "expired"
    SUSPENDED = "suspended"  # license server explicitly revoked/suspended it


class License(Base):
    """Singleton-ish table: one row per taktaplus installation holding the
    latest entitlement state synced from the License Server. History of past
    activations/renewals is kept in LicenseEvent, not here.
    """

    __tablename__ = "licenses"

    id: Mapped[uuid.UUID] = mapped_column(GUID(), primary_key=True, default=uuid.uuid4)

    fingerprint: Mapped[str] = mapped_column(String(64))
    customer_key: Mapped[str] = mapped_column(String(128))
    status: Mapped[LicenseStatus] = mapped_column(Enum(LicenseStatus), default=LicenseStatus.UNACTIVATED)

    fortigate_max_devices: Mapped[int] = mapped_column(Integer, default=0)
    fortiweb_max_devices: Mapped[int] = mapped_column(Integer, default=0)
    sms2fa_max_users: Mapped[int] = mapped_column(Integer, default=0)

    issued_at: Mapped[datetime | None] = mapped_column(DateTime(timezone=True), nullable=True)
    expires_at: Mapped[datetime | None] = mapped_column(DateTime(timezone=True), nullable=True)

    # The signed token from the License Server, cached so the installation can
    # keep running through a temporary network outage (see license_grace_period_days).
    cached_token: Mapped[str] = mapped_column(Text, default="")
    cached_token_issued_at: Mapped[datetime | None] = mapped_column(DateTime(timezone=True), nullable=True)

    last_heartbeat_at: Mapped[datetime | None] = mapped_column(DateTime(timezone=True), nullable=True)
    last_heartbeat_ok: Mapped[bool] = mapped_column(Boolean, default=False)

    updated_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), server_default=func.now(), onupdate=func.now())


class LicenseEvent(Base):
    """Audit trail of activation attempts / heartbeats, separate from the
    general AuditLog since these are frequent and system-generated.
    """

    __tablename__ = "license_events"

    id: Mapped[uuid.UUID] = mapped_column(GUID(), primary_key=True, default=uuid.uuid4)
    created_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), server_default=func.now())
    event_type: Mapped[str] = mapped_column(String(32))  # activate | heartbeat
    success: Mapped[bool] = mapped_column(Boolean)
    message: Mapped[str] = mapped_column(Text, default="")
