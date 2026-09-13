from sqlalchemy.orm import sessionmaker

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


def test_record_audit_event_retries_on_a_genuine_sequence_conflict(db_session, monkeypatch):
    """Forces the exact race the unique `sequence` constraint + retry loop
    exist for: a second, independent session lands a row at the same
    `sequence` value in the gap between our session's read and its commit.
    Real thread timing against a single in-memory sqlite connection isn't
    a reliable way to reproduce this, so it's forced deterministically by
    hooking the first commit call to smuggle in a conflicting row from a
    second session that shares the same underlying engine/database - the
    same "another session already grabbed this slot" situation a
    unique-constraint violation on Postgres would represent for real.
    """
    first = record_audit_event(db_session, actor="alice", action="first")  # sequence=1

    InterloperSession = sessionmaker(bind=db_session.get_bind())
    original_commit = db_session.commit
    calls = {"n": 0}

    def racing_commit():
        calls["n"] += 1
        if calls["n"] == 1:
            # A genuinely concurrent writer would have read the same "last"
            # entry we did (since it committed before we did), so it
            # computes the same prev_hash we did - that's what makes this a
            # believable race and not just a corrupt row.
            interloper = InterloperSession()
            interloper.add(
                AuditLog(
                    sequence=2,
                    actor="bob",
                    action="interloper",
                    prev_hash=first.entry_hash,
                    entry_hash=AuditLog.compute_hash(first.entry_hash, "bob", "interloper", "", ""),
                )
            )
            interloper.commit()
            interloper.close()
        original_commit()

    monkeypatch.setattr(db_session, "commit", racing_commit)

    second = record_audit_event(db_session, actor="alice", action="second")

    assert calls["n"] == 2, "expected exactly one conflict then one successful retry"
    assert second.sequence == 3
    assert second.prev_hash != first.entry_hash  # anchored to the interloper, not skipped over
    assert verify_chain_integrity(db_session)
