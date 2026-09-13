"""audit_logs: make `sequence` unique, so two genuinely concurrent writers
computing the same "next" value get a loud IntegrityError to retry against
instead of silently sharing one sequence number

Revision ID: 0008_audit_sequence_unique
Revises: 0007_audit_sequence
Create Date: 2026-09-13

"""
from alembic import op

revision = "0008_audit_sequence_unique"
down_revision = "0007_audit_sequence"
branch_labels = None
depends_on = None


def upgrade() -> None:
    # Replaces the plain index from 0007 - the unique constraint creates
    # its own backing index in Postgres, so this isn't losing any lookup
    # performance, just adding the uniqueness guarantee.
    op.drop_index("ix_audit_logs_sequence", table_name="audit_logs")
    op.create_unique_constraint("uq_audit_logs_sequence", "audit_logs", ["sequence"])


def downgrade() -> None:
    op.drop_constraint("uq_audit_logs_sequence", "audit_logs", type_="unique")
    op.create_index("ix_audit_logs_sequence", "audit_logs", ["sequence"])
