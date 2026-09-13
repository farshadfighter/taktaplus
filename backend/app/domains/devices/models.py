import enum
import uuid
from datetime import datetime

from sqlalchemy import DateTime, Enum, Integer, String, Text, func
from sqlalchemy.orm import Mapped, mapped_column

from app.db.base import Base
from app.db.types import GUID


class VendorType(str, enum.Enum):
    FORTIGATE = "fortigate"
    FORTIWEB = "fortiweb"


class DeviceStatus(str, enum.Enum):
    UNKNOWN = "unknown"
    ONLINE = "online"
    OFFLINE = "offline"
    ERROR = "error"


class Device(Base):
    __tablename__ = "devices"

    id: Mapped[uuid.UUID] = mapped_column(GUID(), primary_key=True, default=uuid.uuid4)
    name: Mapped[str] = mapped_column(String(128), nullable=False)
    vendor_type: Mapped[VendorType] = mapped_column(Enum(VendorType), nullable=False)

    host: Mapped[str] = mapped_column(String(256), nullable=False)
    port: Mapped[int] = mapped_column(Integer, default=443)
    verify_tls: Mapped[bool] = mapped_column(default=False)
    # FortiGate only; ignored for FortiWeb. Defaults to the global VDOM.
    vdom: Mapped[str] = mapped_column(String(64), default="root")

    # FortiGate: API token auth. Encrypted with app.core.security.encrypt_secret.
    encrypted_api_token: Mapped[str | None] = mapped_column(Text, nullable=True)

    # FortiWeb: username/password auth (session + CSRF login - see
    # drivers/fortiweb.py for why this isn't a bearer token like FortiGate).
    username: Mapped[str | None] = mapped_column(String(128), nullable=True)
    encrypted_password: Mapped[str | None] = mapped_column(Text, nullable=True)

    status: Mapped[DeviceStatus] = mapped_column(Enum(DeviceStatus), default=DeviceStatus.UNKNOWN)
    last_checked_at: Mapped[datetime | None] = mapped_column(DateTime(timezone=True), nullable=True)
    last_error: Mapped[str] = mapped_column(Text, default="")

    firmware_version: Mapped[str] = mapped_column(String(64), default="")
    serial_number: Mapped[str] = mapped_column(String(64), default="")
    reported_hostname: Mapped[str] = mapped_column(String(256), default="")

    # SNMPv2c only for phase 3 - see docs/monitoring.md for why v3 is a
    # documented fast-follow rather than half-implemented here.
    snmp_enabled: Mapped[bool] = mapped_column(default=False)
    snmp_port: Mapped[int] = mapped_column(Integer, default=161)
    encrypted_snmp_community: Mapped[str | None] = mapped_column(Text, nullable=True)

    created_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), server_default=func.now())
    updated_at: Mapped[datetime] = mapped_column(
        DateTime(timezone=True), server_default=func.now(), onupdate=func.now()
    )
