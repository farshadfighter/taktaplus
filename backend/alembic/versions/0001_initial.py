"""initial schema: identity, audit, licensing, self-update

Revision ID: 0001_initial
Revises:
Create Date: 2026-09-12

"""
from alembic import op
import sqlalchemy as sa
from sqlalchemy.dialects import postgresql

revision = "0001_initial"
down_revision = None
branch_labels = None
depends_on = None


def upgrade() -> None:
    op.create_table(
        "roles",
        sa.Column("id", postgresql.UUID(as_uuid=True), primary_key=True),
        sa.Column("name", sa.String(64), nullable=False, unique=True),
        sa.Column("permissions", postgresql.ARRAY(sa.String()), nullable=False, server_default="{}"),
    )

    op.create_table(
        "users",
        sa.Column("id", postgresql.UUID(as_uuid=True), primary_key=True),
        sa.Column("username", sa.String(128), nullable=False, unique=True),
        sa.Column("full_name", sa.String(256), nullable=False, server_default=""),
        sa.Column("password_hash", sa.String(256), nullable=False),
        sa.Column("is_active", sa.Boolean(), nullable=False, server_default=sa.true()),
        sa.Column("role_id", postgresql.UUID(as_uuid=True), sa.ForeignKey("roles.id"), nullable=False),
        sa.Column("created_at", sa.DateTime(timezone=True), server_default=sa.func.now()),
    )

    op.create_table(
        "audit_logs",
        sa.Column("id", postgresql.UUID(as_uuid=True), primary_key=True),
        sa.Column("created_at", sa.DateTime(timezone=True), server_default=sa.func.now()),
        sa.Column("actor", sa.String(256), nullable=False),
        sa.Column("action", sa.String(128), nullable=False),
        sa.Column("target", sa.String(256), nullable=False, server_default=""),
        sa.Column("details", sa.Text(), nullable=False, server_default=""),
        sa.Column("prev_hash", sa.String(64), nullable=False, server_default=""),
        sa.Column("entry_hash", sa.String(64), nullable=False),
    )

    license_status_enum = postgresql.ENUM(
        "unactivated", "active", "grace", "expired", "suspended", name="licensestatus"
    )
    license_status_enum.create(op.get_bind(), checkfirst=True)

    op.create_table(
        "licenses",
        sa.Column("id", postgresql.UUID(as_uuid=True), primary_key=True),
        sa.Column("fingerprint", sa.String(64), nullable=False),
        sa.Column("customer_key", sa.String(128), nullable=False),
        sa.Column("status", license_status_enum, nullable=False, server_default="unactivated"),
        sa.Column("fortigate_max_devices", sa.Integer(), nullable=False, server_default="0"),
        sa.Column("fortiweb_max_devices", sa.Integer(), nullable=False, server_default="0"),
        sa.Column("sms2fa_max_users", sa.Integer(), nullable=False, server_default="0"),
        sa.Column("issued_at", sa.DateTime(timezone=True), nullable=True),
        sa.Column("expires_at", sa.DateTime(timezone=True), nullable=True),
        sa.Column("cached_token", sa.Text(), nullable=False, server_default=""),
        sa.Column("cached_token_issued_at", sa.DateTime(timezone=True), nullable=True),
        sa.Column("last_heartbeat_at", sa.DateTime(timezone=True), nullable=True),
        sa.Column("last_heartbeat_ok", sa.Boolean(), nullable=False, server_default=sa.false()),
        sa.Column("updated_at", sa.DateTime(timezone=True), server_default=sa.func.now()),
    )

    op.create_table(
        "license_events",
        sa.Column("id", postgresql.UUID(as_uuid=True), primary_key=True),
        sa.Column("created_at", sa.DateTime(timezone=True), server_default=sa.func.now()),
        sa.Column("event_type", sa.String(32), nullable=False),
        sa.Column("success", sa.Boolean(), nullable=False),
        sa.Column("message", sa.Text(), nullable=False, server_default=""),
    )

    op.create_table(
        "update_history",
        sa.Column("id", postgresql.UUID(as_uuid=True), primary_key=True),
        sa.Column("version", sa.String(32), nullable=False),
        sa.Column("applied_at", sa.DateTime(timezone=True), server_default=sa.func.now()),
        sa.Column("success", sa.Boolean(), nullable=False),
        sa.Column("notes", sa.Text(), nullable=False, server_default=""),
    )


def downgrade() -> None:
    op.drop_table("update_history")
    op.drop_table("license_events")
    op.drop_table("licenses")
    postgresql.ENUM(name="licensestatus").drop(op.get_bind(), checkfirst=True)
    op.drop_table("audit_logs")
    op.drop_table("users")
    op.drop_table("roles")
