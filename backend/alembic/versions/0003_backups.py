"""backups table

Revision ID: 0003_backups
Revises: 0002_devices
Create Date: 2026-09-13

"""
from alembic import op
import sqlalchemy as sa
from sqlalchemy.dialects import postgresql

revision = "0003_backups"
down_revision = "0002_devices"
branch_labels = None
depends_on = None


def upgrade() -> None:
    backup_source_enum = postgresql.ENUM("manual", "scheduled", name="backupsource")
    backup_source_enum.create(op.get_bind(), checkfirst=True)

    backup_status_enum = postgresql.ENUM("success", "failed", name="backupstatus")
    backup_status_enum.create(op.get_bind(), checkfirst=True)

    op.create_table(
        "backups",
        sa.Column("id", postgresql.UUID(as_uuid=True), primary_key=True),
        sa.Column(
            "device_id",
            postgresql.UUID(as_uuid=True),
            sa.ForeignKey("devices.id", ondelete="CASCADE"),
            nullable=False,
        ),
        sa.Column("taken_at", sa.DateTime(timezone=True), server_default=sa.func.now()),
        sa.Column("taken_by", sa.String(256), nullable=False, server_default=""),
        sa.Column("source", backup_source_enum, nullable=False, server_default="manual"),
        sa.Column("status", backup_status_enum, nullable=False, server_default="success"),
        sa.Column("error_message", sa.Text(), nullable=False, server_default=""),
        sa.Column("checksum_sha256", sa.String(64), nullable=False, server_default=""),
        sa.Column("size_bytes", sa.Integer(), nullable=False, server_default="0"),
        sa.Column("encrypted_content", sa.Text(), nullable=True),
        sa.Column("device_serial_snapshot", sa.String(64), nullable=False, server_default=""),
        sa.Column("device_firmware_snapshot", sa.String(64), nullable=False, server_default=""),
    )
    op.create_index("ix_backups_device_id", "backups", ["device_id"])


def downgrade() -> None:
    op.drop_index("ix_backups_device_id", table_name="backups")
    op.drop_table("backups")
    postgresql.ENUM(name="backupstatus").drop(op.get_bind(), checkfirst=True)
    postgresql.ENUM(name="backupsource").drop(op.get_bind(), checkfirst=True)
