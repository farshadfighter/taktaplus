"""SNMP monitoring: device fields, metric samples, alerts

Revision ID: 0004_monitoring
Revises: 0003_backups
Create Date: 2026-09-13

"""
from alembic import op
import sqlalchemy as sa
from sqlalchemy.dialects import postgresql

revision = "0004_monitoring"
down_revision = "0003_backups"
branch_labels = None
depends_on = None


def upgrade() -> None:
    op.add_column("devices", sa.Column("snmp_enabled", sa.Boolean(), nullable=False, server_default=sa.false()))
    op.add_column("devices", sa.Column("snmp_port", sa.Integer(), nullable=False, server_default="161"))
    op.add_column("devices", sa.Column("encrypted_snmp_community", sa.Text(), nullable=True))

    op.create_table(
        "snmp_metric_samples",
        sa.Column("id", postgresql.UUID(as_uuid=True), primary_key=True),
        sa.Column(
            "device_id",
            postgresql.UUID(as_uuid=True),
            sa.ForeignKey("devices.id", ondelete="CASCADE"),
            nullable=False,
        ),
        sa.Column("collected_at", sa.DateTime(timezone=True), server_default=sa.func.now()),
        sa.Column("cpu_percent", sa.Float(), nullable=True),
        sa.Column("memory_percent", sa.Float(), nullable=True),
        sa.Column("session_count", sa.Integer(), nullable=True),
        sa.Column("sys_uptime_ticks", sa.Integer(), nullable=True),
    )
    op.create_index("ix_snmp_metric_samples_device_id", "snmp_metric_samples", ["device_id"])

    alert_source_enum = postgresql.ENUM("trap", "poll_threshold", "poll_unreachable", name="alertsource")
    alert_source_enum.create(op.get_bind(), checkfirst=True)
    alert_severity_enum = postgresql.ENUM("info", "warning", "critical", name="alertseverity")
    alert_severity_enum.create(op.get_bind(), checkfirst=True)

    op.create_table(
        "snmp_alerts",
        sa.Column("id", postgresql.UUID(as_uuid=True), primary_key=True),
        sa.Column(
            "device_id",
            postgresql.UUID(as_uuid=True),
            sa.ForeignKey("devices.id", ondelete="CASCADE"),
            nullable=False,
        ),
        sa.Column("received_at", sa.DateTime(timezone=True), server_default=sa.func.now()),
        sa.Column("source", alert_source_enum, nullable=False),
        sa.Column("severity", alert_severity_enum, nullable=False),
        sa.Column("trap_oid", sa.String(128), nullable=False, server_default=""),
        sa.Column("message", sa.Text(), nullable=False, server_default=""),
        sa.Column("acknowledged", sa.Boolean(), nullable=False, server_default=sa.false()),
    )
    op.create_index("ix_snmp_alerts_device_id", "snmp_alerts", ["device_id"])


def downgrade() -> None:
    op.drop_index("ix_snmp_alerts_device_id", table_name="snmp_alerts")
    op.drop_table("snmp_alerts")
    postgresql.ENUM(name="alertseverity").drop(op.get_bind(), checkfirst=True)
    postgresql.ENUM(name="alertsource").drop(op.get_bind(), checkfirst=True)

    op.drop_index("ix_snmp_metric_samples_device_id", table_name="snmp_metric_samples")
    op.drop_table("snmp_metric_samples")

    op.drop_column("devices", "encrypted_snmp_community")
    op.drop_column("devices", "snmp_port")
    op.drop_column("devices", "snmp_enabled")
