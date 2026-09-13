from app.domains.audit.models import AuditLog
from app.domains.audit.service import record_audit_event, verify_chain_integrity


def test_chain_links_and_verifies(db_session):
    first = record_audit_event(db_session, actor="alice", action="login")
    second = record_audit_event(db_session, actor="alice", action="device.create", target="fw1")

    assert first.prev_hash == ""
    assert second.prev_hash == first.entry_hash
    assert verify_chain_integrity(db_session)


def test_tampering_breaks_verification(db_session):
    record_audit_event(db_session, actor="alice", action="login")
    entry = record_audit_event(db_session, actor="alice", action="device.delete", target="fw1")

    entry.target = "fw2"
    db_session.commit()

    assert not verify_chain_integrity(db_session)


def test_ordering_is_deterministic_even_when_created_at_ties(db_session):
    """SQLite's CURRENT_TIMESTAMP only has 1-second resolution, so entries
    written back-to-back in a test very likely share the same created_at.
    Before the `sequence` column, `ORDER BY created_at DESC LIMIT 1` had no
    deterministic winner among ties, so a new entry's prev_hash could
    anchor to the wrong row. Forcing every row here to the exact same
    created_at reproduces the tie deterministically instead of relying on
    the test happening to run fast enough - and the chain must still be a
    single, verifiable, in-order line.
    """
    entries = [record_audit_event(db_session, actor="alice", action=f"action-{i}") for i in range(5)]

    same_instant = entries[0].created_at
    for entry in entries:
        entry.created_at = same_instant
    db_session.commit()

    assert [e.sequence for e in entries] == [1, 2, 3, 4, 5]
    assert verify_chain_integrity(db_session)

    # And appending one more after the tie must still anchor to the
    # actually-last entry (by sequence), not whichever tied row a
    # created_at-ordered query happens to return.
    sixth = record_audit_event(db_session, actor="alice", action="action-5")
    assert sixth.prev_hash == entries[-1].entry_hash
    assert verify_chain_integrity(db_session)


def test_sequence_assigned_in_insertion_order(db_session):
    for i in range(3):
        record_audit_event(db_session, actor="alice", action=f"a{i}")

    rows = list(db_session.query(AuditLog).order_by(AuditLog.sequence.asc()))
    assert [r.action for r in rows] == ["a0", "a1", "a2"]
