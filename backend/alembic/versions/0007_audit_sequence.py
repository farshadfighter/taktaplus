"""audit_logs: add a monotonic `sequence` column for deterministic chain
ordering, replacing created_at (whose SQLite/coarse-resolution ties made
the hash chain's "last entry" lookup non-deterministic)

Revision ID: 0007_audit_sequence
Revises: 0006_radius
Create Date: 2026-09-13

"""
from alembic import op
import sqlalchemy as sa

revision = "0007_audit_sequence"
down_revision = "0006_radius"
branch_labels = None
depends_on = None


def upgrade() -> None:
    op.add_column("audit_logs", sa.Column("sequence", sa.Integer(), nullable=True))

    # Backfill existing rows in their best-known order. created_at is the
    # only ordering information available for rows written before this
    # column existed; id is just a tie-breaker to make the backfill itself
    # deterministic (it does not claim to recover true original insert
    # order for same-instant rows - only the app-assigned `sequence` going
    # forward guarantees that).
    op.execute(
        """
        UPDATE audit_logs
        SET sequence = sub.rn
        FROM (
            SELECT id, ROW_NUMBER() OVER (ORDER BY created_at ASC, id ASC) AS rn
            FROM audit_logs
        ) AS sub
        WHERE audit_logs.id = sub.id
        """
    )

    op.alter_column("audit_logs", "sequence", nullable=False)
    op.create_index("ix_audit_logs_sequence", "audit_logs", ["sequence"])


def downgrade() -> None:
    op.drop_index("ix_audit_logs_sequence", table_name="audit_logs")
    op.drop_column("audit_logs", "sequence")
