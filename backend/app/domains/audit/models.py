import hashlib
import uuid
from datetime import datetime

from sqlalchemy import DateTime, Integer, String, Text, func
from sqlalchemy.orm import Mapped, mapped_column

from app.db.base import Base
from app.db.types import GUID


class AuditLog(Base):
    """Hash-chained audit trail: each row commits to the previous row's hash,
    so a tampered or deleted middle row breaks the chain and is detectable.

    Chain order is anchored on `sequence`, an application-assigned monotonic
    counter (see record_audit_event) - not on `created_at`. Postgres's
    default timestamp resolution is fine, but SQLite (used in tests) only
    keeps whole-second resolution, so two events recorded within the same
    second used to tie under `ORDER BY created_at` with no deterministic
    winner, letting the "last entry" lookup pick the wrong row as the new
    entry's prev_hash anchor.
    """

    __tablename__ = "audit_logs"

    id: Mapped[uuid.UUID] = mapped_column(GUID(), primary_key=True, default=uuid.uuid4)
    # unique, not just indexed: this is what turns a genuinely concurrent
    # double-write (two sessions both computing the same "next" sequence)
    # into a loud IntegrityError that record_audit_event retries, instead
    # of two rows silently sharing one sequence number and corrupting the
    # chain's order.
    sequence: Mapped[int] = mapped_column(Integer, nullable=False, unique=True, index=True)
    created_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), server_default=func.now())
    actor: Mapped[str] = mapped_column(String(256))
    action: Mapped[str] = mapped_column(String(128))
    target: Mapped[str] = mapped_column(String(256), default="")
    details: Mapped[str] = mapped_column(Text, default="")
    prev_hash: Mapped[str] = mapped_column(String(64), default="")
    entry_hash: Mapped[str] = mapped_column(String(64))

    @staticmethod
    def compute_hash(prev_hash: str, actor: str, action: str, target: str, details: str) -> str:
        payload = f"{prev_hash}|{actor}|{action}|{target}|{details}".encode()
        return hashlib.sha256(payload).hexdigest()
