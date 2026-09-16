"""sms_gateway_config: add sms.ir as a native provider (API key + line
number), alongside the existing kavenegar/generic_http support

Revision ID: 0009_sms_ir
Revises: 0008_audit_sequence_unique
Create Date: 2026-09-16

"""
from alembic import op
import sqlalchemy as sa

revision = "0009_sms_ir"
down_revision = "0008_audit_sequence_unique"
branch_labels = None
depends_on = None


def upgrade() -> None:
    op.execute("ALTER TYPE smsprovider ADD VALUE IF NOT EXISTS 'sms_ir'")
    op.add_column("sms_gateway_config", sa.Column("encrypted_smsir_api_key", sa.Text(), nullable=True))
    op.add_column("sms_gateway_config", sa.Column("smsir_line_number", sa.String(32), nullable=True))


def downgrade() -> None:
    # Postgres has no ALTER TYPE ... DROP VALUE - leaving the enum label in
    # place on downgrade is harmless (matches how Postgres enum downgrades
    # are usually handled: only the columns that used it are removed here).
    op.drop_column("sms_gateway_config", "smsir_line_number")
    op.drop_column("sms_gateway_config", "encrypted_smsir_api_key")
