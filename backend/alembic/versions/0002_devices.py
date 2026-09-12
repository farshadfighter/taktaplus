"""devices table

Revision ID: 0002_devices
Revises: 0001_initial
Create Date: 2026-09-13

"""
from alembic import op
import sqlalchemy as sa
from sqlalchemy.dialects import postgresql

revision = "0002_devices"
down_revision = "0001_initial"
branch_labels = None
depends_on = None


def upgrade() -> None:
    vendor_type_enum = postgresql.ENUM("fortigate", "fortiweb", name="vendortype")
    vendor_type_enum.create(op.get_bind(), checkfirst=True)

    device_status_enum = postgresql.ENUM("unknown", "online", "offline", "error", name="devicestatus")
    device_status_enum.create(op.get_bind(), checkfirst=True)

    op.create_table(
        "devices",
        sa.Column("id", postgresql.UUID(as_uuid=True), primary_key=True),
        sa.Column("name", sa.String(128), nullable=False),
        sa.Column("vendor_type", vendor_type_enum, nullable=False),
        sa.Column("host", sa.String(256), nullable=False),
        sa.Column("port", sa.Integer(), nullable=False, server_default="443"),
        sa.Column("verify_tls", sa.Boolean(), nullable=False, server_default=sa.false()),
        sa.Column("vdom", sa.String(64), nullable=False, server_default="root"),
        sa.Column("encrypted_api_token", sa.Text(), nullable=True),
        sa.Column("username", sa.String(128), nullable=True),
        sa.Column("encrypted_password", sa.Text(), nullable=True),
        sa.Column("status", device_status_enum, nullable=False, server_default="unknown"),
        sa.Column("last_checked_at", sa.DateTime(timezone=True), nullable=True),
        sa.Column("last_error", sa.Text(), nullable=False, server_default=""),
        sa.Column("firmware_version", sa.String(64), nullable=False, server_default=""),
        sa.Column("serial_number", sa.String(64), nullable=False, server_default=""),
        sa.Column("reported_hostname", sa.String(256), nullable=False, server_default=""),
        sa.Column("created_at", sa.DateTime(timezone=True), server_default=sa.func.now()),
        sa.Column("updated_at", sa.DateTime(timezone=True), server_default=sa.func.now()),
    )


def downgrade() -> None:
    op.drop_table("devices")
    postgresql.ENUM(name="devicestatus").drop(op.get_bind(), checkfirst=True)
    postgresql.ENUM(name="vendortype").drop(op.get_bind(), checkfirst=True)
