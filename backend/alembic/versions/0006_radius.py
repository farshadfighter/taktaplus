"""SMS-based two-factor RADIUS auth: 2FA users, RADIUS clients (NAS), OTP
challenges, SMS gateway config

Revision ID: 0006_radius
Revises: 0005_distribution
Create Date: 2026-09-13

"""
from alembic import op
import sqlalchemy as sa
from sqlalchemy.dialects import postgresql

revision = "0006_radius"
down_revision = "0005_distribution"
branch_labels = None
depends_on = None


def upgrade() -> None:
    op.create_table(
        "two_factor_users",
        sa.Column("id", postgresql.UUID(as_uuid=True), primary_key=True),
        sa.Column("username", sa.String(128), nullable=False, unique=True),
        sa.Column("encrypted_password", sa.Text(), nullable=False),
        sa.Column("encrypted_mobile_number", sa.Text(), nullable=False),
        sa.Column("enabled", sa.Boolean(), nullable=False, server_default=sa.true()),
        sa.Column("failed_attempts", sa.Integer(), nullable=False, server_default="0"),
        sa.Column("locked_until", sa.DateTime(timezone=True), nullable=True),
        sa.Column("otp_window_started_at", sa.DateTime(timezone=True), nullable=True),
        sa.Column("otp_sent_in_window", sa.Integer(), nullable=False, server_default="0"),
        sa.Column("created_at", sa.DateTime(timezone=True), server_default=sa.func.now()),
        sa.Column("updated_at", sa.DateTime(timezone=True), server_default=sa.func.now()),
    )

    op.create_table(
        "radius_clients",
        sa.Column("id", postgresql.UUID(as_uuid=True), primary_key=True),
        sa.Column("name", sa.String(128), nullable=False),
        sa.Column("nas_ip", sa.String(64), nullable=False, unique=True),
        sa.Column("encrypted_shared_secret", sa.Text(), nullable=False),
        sa.Column("enabled", sa.Boolean(), nullable=False, server_default=sa.true()),
        sa.Column("created_at", sa.DateTime(timezone=True), server_default=sa.func.now()),
    )

    op.create_table(
        "otp_challenges",
        sa.Column("id", postgresql.UUID(as_uuid=True), primary_key=True),
        sa.Column("username", sa.String(128), nullable=False),
        sa.Column("state_token", sa.String(64), nullable=False, unique=True),
        sa.Column("code_hash", sa.String(64), nullable=False),
        sa.Column("expires_at", sa.DateTime(timezone=True), nullable=False),
        sa.Column("consumed", sa.Boolean(), nullable=False, server_default=sa.false()),
        sa.Column("attempt_count", sa.Integer(), nullable=False, server_default="0"),
        sa.Column("created_at", sa.DateTime(timezone=True), server_default=sa.func.now()),
    )
    op.create_index("ix_otp_challenges_username", "otp_challenges", ["username"])

    sms_provider_enum = postgresql.ENUM("kavenegar", "generic_http", name="smsprovider")
    sms_provider_enum.create(op.get_bind(), checkfirst=True)

    op.create_table(
        "sms_gateway_config",
        sa.Column("id", postgresql.UUID(as_uuid=True), primary_key=True),
        sa.Column("provider", sms_provider_enum, nullable=False, server_default="generic_http"),
        sa.Column("encrypted_kavenegar_api_key", sa.Text(), nullable=True),
        sa.Column("kavenegar_sender", sa.String(32), nullable=True),
        sa.Column("generic_method", sa.String(8), nullable=False, server_default="GET"),
        sa.Column("generic_url_template", sa.Text(), nullable=True),
        sa.Column("generic_auth_header_name", sa.String(128), nullable=True),
        sa.Column("encrypted_generic_auth_header_value", sa.Text(), nullable=True),
        sa.Column("updated_at", sa.DateTime(timezone=True), server_default=sa.func.now()),
    )


def downgrade() -> None:
    op.drop_table("sms_gateway_config")
    postgresql.ENUM(name="smsprovider").drop(op.get_bind(), checkfirst=True)

    op.drop_index("ix_otp_challenges_username", table_name="otp_challenges")
    op.drop_table("otp_challenges")

    op.drop_table("radius_clients")

    op.drop_table("two_factor_users")
