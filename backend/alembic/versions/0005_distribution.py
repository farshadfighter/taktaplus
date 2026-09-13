"""offline signature/firmware distribution: SSH device fields, packages, push records

Revision ID: 0005_distribution
Revises: 0004_monitoring
Create Date: 2026-09-13

"""
from alembic import op
import sqlalchemy as sa
from sqlalchemy.dialects import postgresql

revision = "0005_distribution"
down_revision = "0004_monitoring"
branch_labels = None
depends_on = None


def upgrade() -> None:
    op.add_column("devices", sa.Column("ssh_port", sa.Integer(), nullable=False, server_default="22"))
    op.add_column("devices", sa.Column("ssh_username", sa.String(128), nullable=True))
    op.add_column("devices", sa.Column("encrypted_ssh_password", sa.Text(), nullable=True))

    op.create_table(
        "ftp_source_config",
        sa.Column("id", postgresql.UUID(as_uuid=True), primary_key=True),
        sa.Column("use_custom", sa.Boolean(), nullable=False, server_default=sa.false()),
        sa.Column("host", sa.String(256), nullable=False, server_default=""),
        sa.Column("port", sa.Integer(), nullable=False, server_default="21"),
        sa.Column("username", sa.String(128), nullable=False, server_default=""),
        sa.Column("encrypted_password", sa.Text(), nullable=True),
        sa.Column("remote_path", sa.String(512), nullable=False, server_default="/"),
        sa.Column("updated_at", sa.DateTime(timezone=True), server_default=sa.func.now()),
    )

    package_source_enum = postgresql.ENUM("ftp_sync", "manual_upload", name="packagesource")
    package_source_enum.create(op.get_bind(), checkfirst=True)

    op.create_table(
        "packages",
        sa.Column("id", postgresql.UUID(as_uuid=True), primary_key=True),
        sa.Column("vendor_type", postgresql.ENUM("fortigate", "fortiweb", name="vendortype", create_type=False), nullable=False),
        sa.Column("package_type", sa.String(32), nullable=False),
        sa.Column("filename", sa.String(256), nullable=False),
        sa.Column("version", sa.String(64), nullable=False, server_default=""),
        sa.Column("checksum_sha256", sa.String(64), nullable=False, server_default=""),
        sa.Column("size_bytes", sa.Integer(), nullable=False, server_default="0"),
        sa.Column("local_path", sa.String(1024), nullable=False),
        sa.Column("source", package_source_enum, nullable=False),
        sa.Column("uploaded_by", sa.String(256), nullable=False, server_default=""),
        sa.Column("fetched_at", sa.DateTime(timezone=True), server_default=sa.func.now()),
    )

    push_status_enum = postgresql.ENUM("success", "failed", name="pushstatus")
    push_status_enum.create(op.get_bind(), checkfirst=True)

    op.create_table(
        "push_records",
        sa.Column("id", postgresql.UUID(as_uuid=True), primary_key=True),
        sa.Column(
            "device_id",
            postgresql.UUID(as_uuid=True),
            sa.ForeignKey("devices.id", ondelete="CASCADE"),
            nullable=False,
        ),
        sa.Column(
            "package_id",
            postgresql.UUID(as_uuid=True),
            sa.ForeignKey("packages.id", ondelete="CASCADE"),
            nullable=False,
        ),
        sa.Column("pushed_at", sa.DateTime(timezone=True), server_default=sa.func.now()),
        sa.Column("pushed_by", sa.String(256), nullable=False, server_default=""),
        sa.Column("status", push_status_enum, nullable=False),
        sa.Column("error_message", sa.Text(), nullable=False, server_default=""),
        sa.Column("post_push_check_ok", sa.Boolean(), nullable=True),
    )
    op.create_index("ix_push_records_device_id", "push_records", ["device_id"])


def downgrade() -> None:
    op.drop_index("ix_push_records_device_id", table_name="push_records")
    op.drop_table("push_records")
    postgresql.ENUM(name="pushstatus").drop(op.get_bind(), checkfirst=True)

    op.drop_table("packages")
    postgresql.ENUM(name="packagesource").drop(op.get_bind(), checkfirst=True)

    op.drop_table("ftp_source_config")

    op.drop_column("devices", "encrypted_ssh_password")
    op.drop_column("devices", "ssh_username")
    op.drop_column("devices", "ssh_port")
