"""users: add failed_attempts/locked_until for admin-login brute-force
lockout - mirrors two_factor_users' existing columns but for this app's
own operator accounts (see app/domains/identity/service.py)

Revision ID: 0010_user_lockout
Revises: 0009_sms_ir
Create Date: 2026-09-17

"""
from alembic import op
import sqlalchemy as sa

revision = "0010_user_lockout"
down_revision = "0009_sms_ir"
branch_labels = None
depends_on = None


def upgrade() -> None:
    op.add_column("users", sa.Column("failed_attempts", sa.Integer(), nullable=False, server_default="0"))
    op.add_column("users", sa.Column("locked_until", sa.DateTime(timezone=True), nullable=True))
    op.alter_column("users", "failed_attempts", server_default=None)


def downgrade() -> None:
    op.drop_column("users", "locked_until")
    op.drop_column("users", "failed_attempts")
