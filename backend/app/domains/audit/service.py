from sqlalchemy import func, select
from sqlalchemy.orm import Session

from app.domains.audit.models import AuditLog


def record_audit_event(
    db: Session, *, actor: str, action: str, target: str = "", details: str = ""
) -> AuditLog:
    # Ordering by `sequence`, not `created_at`, is what makes "last entry"
    # deterministic - see AuditLog's docstring for why created_at alone
    # isn't enough.
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
    db.commit()
    db.refresh(entry)
    return entry


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
