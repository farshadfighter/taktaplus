import enum
import uuid
from datetime import datetime

from sqlalchemy import DateTime, Enum, ForeignKey, Integer, String, Text, func
from sqlalchemy.orm import Mapped, mapped_column

from app.db.base import Base
from app.db.types import GUID


class BackupSource(str, enum.Enum):
    MANUAL = "manual"
    SCHEDULED = "scheduled"


class BackupStatus(str, enum.Enum):
    SUCCESS = "success"
    FAILED = "failed"


class Backup(Base):
    __tablename__ = "backups"

    id: Mapped[uuid.UUID] = mapped_column(GUID(), primary_key=True, default=uuid.uuid4)
    device_id: Mapped[uuid.UUID] = mapped_column(GUID(), ForeignKey("devices.id", ondelete="CASCADE"))

    taken_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), server_default=func.now())
    taken_by: Mapped[str] = mapped_column(String(256), default="")
    source: Mapped[BackupSource] = mapped_column(Enum(BackupSource), default=BackupSource.MANUAL)
    status: Mapped[BackupStatus] = mapped_column(Enum(BackupStatus), default=BackupStatus.SUCCESS)
    error_message: Mapped[str] = mapped_column(Text, default="")

    checksum_sha256: Mapped[str] = mapped_column(String(64), default="")
    size_bytes: Mapped[int] = mapped_column(Integer, default=0)
    encrypted_content: Mapped[str | None] = mapped_column(Text, nullable=True)

    # Snapshot of the device's identity at backup time, used to warn before
    # restoring onto hardware that doesn't look like the one this came from.
    device_serial_snapshot: Mapped[str] = mapped_column(String(64), default="")
    device_firmware_snapshot: Mapped[str] = mapped_column(String(64), default="")
