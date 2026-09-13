from sqlalchemy import func, select
from sqlalchemy.exc import IntegrityError
from sqlalchemy.orm import Session

from app.domains.audit.models import AuditLog

MAX_SEQUENCE_CONFLICT_RETRIES = 5


class AuditWriteError(RuntimeError):
    """record_audit_event couldn't land its row after retrying past
    concurrent writers landing the same sequence number first."""


def record_audit_event(
    db: Session, *, actor: str, action: str, target: str = "", details: str = ""
) -> AuditLog:
    # `sequence` is unique at the DB level (see AuditLog), so two sessions
    # racing to compute the same "next" value can't both land silently -
    # one gets IntegrityError and retries against the now-updated last row,
    # rather than corrupting the chain with a duplicate sequence.
    for attempt in range(MAX_SEQUENCE_CONFLICT_RETRIES):
        last = db.scalar(select(AuditLog).order_by(AuditLog.sequence.desc()).limit(1))
        prev_hash = last.entry_hash if last else ""
        next_sequence = (last.sequence + 1) if last else 1
        entry = AuditLog(
            sequence=next_sequence,
            actor=actor,
            action=action,
            target=target,
            details=details,
            prev_hash=prev_hash,
            entry_hash=AuditLog.compute_hash(prev_hash, actor, action, target, details),
        )
        db.add(entry)
        try:
            db.commit()
        except IntegrityError:
            db.rollback()
            continue
        db.refresh(entry)
        return entry

    raise AuditWriteError(
        f"could not record audit event after {MAX_SEQUENCE_CONFLICT_RETRIES} sequence conflicts"
    )


def verify_chain_integrity(db: Session) -> bool:
    prev_hash = ""
    for entry in db.scalars(select(AuditLog).order_by(AuditLog.sequence.asc())):
        if entry.prev_hash != prev_hash:
            return False
        expected = AuditLog.compute_hash(prev_hash, entry.actor, entry.action, entry.target, entry.details)
        if entry.entry_hash != expected:
            return False
        prev_hash = entry.entry_hash
    return True
