import enum
import uuid
from datetime import datetime

from sqlalchemy import Boolean, DateTime, Enum, ForeignKey, Integer, String, Text, func
from sqlalchemy.orm import Mapped, mapped_column

from app.db.base import Base
from app.db.types import GUID
from app.domains.devices.models import VendorType


class PackageSource(str, enum.Enum):
    FTP_SYNC = "ftp_sync"
    MANUAL_UPLOAD = "manual_upload"


class PushStatus(str, enum.Enum):
    SUCCESS = "success"
    FAILED = "failed"


class FtpSourceConfig(Base):
    """Singleton-ish table (one row) for the customer's override of the
    default vendor FTP source. When use_custom is False (or no row exists),
    the default TAKTAPLUS_DEFAULT_FTP_* settings are used instead - see
    docs/signature-distribution.md.
    """

    __tablename__ = "ftp_source_config"

    id: Mapped[uuid.UUID] = mapped_column(GUID(), primary_key=True, default=uuid.uuid4)
    use_custom: Mapped[bool] = mapped_column(Boolean, default=False)
    host: Mapped[str] = mapped_column(String(256), default="")
    port: Mapped[int] = mapped_column(Integer, default=21)
    username: Mapped[str] = mapped_column(String(128), default="")
    encrypted_password: Mapped[str | None] = mapped_column(Text, nullable=True)
    remote_path: Mapped[str] = mapped_column(String(512), default="/")
    updated_at: Mapped[datetime] = mapped_column(
        DateTime(timezone=True), server_default=func.now(), onupdate=func.now()
    )


class Package(Base):
    """A signature/firmware file taktaplus knows about, stored on local
    disk (not in the database - see docs/signature-distribution.md for why
    this differs from the phase 2 backup approach: firmware images can be
    hundreds of MB, config backups are tens of KB).
    """

    __tablename__ = "packages"

    id: Mapped[uuid.UUID] = mapped_column(GUID(), primary_key=True, default=uuid.uuid4)
    vendor_type: Mapped[VendorType] = mapped_column(Enum(VendorType), nullable=False)
    # Free-form, not an enum: Fortinet has many package types (ips, av,
    # ips-engine, app-ctrl, firmware, ...) and this list will grow - see
    # docs/signature-distribution.md for the ones actually wired up.
    package_type: Mapped[str] = mapped_column(String(32), nullable=False)

    filename: Mapped[str] = mapped_column(String(256), nullable=False)
    version: Mapped[str] = mapped_column(String(64), default="")
    checksum_sha256: Mapped[str] = mapped_column(String(64), default="")
    size_bytes: Mapped[int] = mapped_column(Integer, default=0)
    local_path: Mapped[str] = mapped_column(String(1024), nullable=False)

    source: Mapped[PackageSource] = mapped_column(Enum(PackageSource))
    uploaded_by: Mapped[str] = mapped_column(String(256), default="")
    fetched_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), server_default=func.now())


class PushRecord(Base):
    __tablename__ = "push_records"

    id: Mapped[uuid.UUID] = mapped_column(GUID(), primary_key=True, default=uuid.uuid4)
    device_id: Mapped[uuid.UUID] = mapped_column(GUID(), ForeignKey("devices.id", ondelete="CASCADE"))
    package_id: Mapped[uuid.UUID] = mapped_column(GUID(), ForeignKey("packages.id", ondelete="CASCADE"))

    pushed_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), server_default=func.now())
    pushed_by: Mapped[str] = mapped_column(String(256), default="")
    status: Mapped[PushStatus] = mapped_column(Enum(PushStatus))
    error_message: Mapped[str] = mapped_column(Text, default="")
    post_push_check_ok: Mapped[bool | None] = mapped_column(Boolean, nullable=True)
