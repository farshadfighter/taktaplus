from datetime import datetime, timedelta, timezone

from app.domains.backups import service as backup_service
from app.domains.devices.drivers.base import DeviceStatusInfo, FortinetDriver
from app.domains.devices.models import Device, VendorType
from app.domains.monitoring import service as monitoring_service
from app.domains.monitoring.snmp_client import SnmpMetrics
from app.domains.reporting.service import build_fleet_status


class StubDriver(FortinetDriver):
    def backup(self) -> bytes:
        return b"config"

    def test_connection(self) -> DeviceStatusInfo:  # pragma: no cover - unused here
        raise NotImplementedError


def _make_device(db_session, **overrides) -> Device:
    defaults = dict(name="fw1", vendor_type=VendorType.FORTIGATE, host="10.0.0.1")
    defaults.update(overrides)
    device = Device(**defaults)
    db_session.add(device)
    db_session.commit()
    return device


def test_fleet_status_flags_overdue_backup_when_none_taken(db_session):
    _make_device(db_session)

    rows = build_fleet_status(db_session)

    assert len(rows) == 1
    assert rows[0].backup_overdue is True
    assert rows[0].last_backup_at is None


def test_fleet_status_not_overdue_after_recent_backup(db_session):
    device = _make_device(db_session)
    backup_service.create_backup(db_session, device, actor="tester", driver=StubDriver())

    rows = build_fleet_status(db_session)

    assert rows[0].backup_overdue is False
    assert rows[0].last_backup_status.value == "success"


def test_fleet_status_includes_latest_metrics_and_open_alerts(db_session):
    device = _make_device(db_session)
    monitoring_service.record_metric_sample(
        db_session, device, SnmpMetrics(cpu_percent=33.0, memory_percent=44.0, session_count=1, sys_uptime_ticks=1)
    )
    monitoring_service.record_poll_failure(db_session, device, "timeout")

    rows = build_fleet_status(db_session)

    assert rows[0].latest_cpu_percent == 33.0
    assert rows[0].latest_memory_percent == 44.0
    assert rows[0].open_alert_count == 1


def test_fleet_status_keeps_each_devices_own_latest_row_when_batched(db_session):
    """build_fleet_status batches the latest-backup/latest-metric/open-alert
    lookups into one query each across every device (see reporting/service.py)
    instead of querying per device - this pins down that the batching still
    attributes each row to the *correct* device rather than mixing them up
    or leaking one device's latest values onto another's.
    """
    device_a = _make_device(db_session, name="fw-a", host="10.0.0.1")
    device_b = _make_device(db_session, name="fw-b", host="10.0.0.2")

    backup_service.create_backup(db_session, device_a, actor="tester", driver=StubDriver())
    monitoring_service.record_metric_sample(
        db_session, device_a, SnmpMetrics(cpu_percent=10.0, memory_percent=20.0, session_count=1, sys_uptime_ticks=1)
    )
    monitoring_service.record_poll_failure(db_session, device_a, "timeout")

    monitoring_service.record_metric_sample(
        db_session, device_b, SnmpMetrics(cpu_percent=55.0, memory_percent=60.0, session_count=1, sys_uptime_ticks=1)
    )

    rows = {row.device_id: row for row in build_fleet_status(db_session)}

    assert rows[device_a.id].last_backup_status.value == "success"
    assert rows[device_a.id].latest_cpu_percent == 10.0
    assert rows[device_a.id].open_alert_count == 1

    assert rows[device_b.id].last_backup_at is None
    assert rows[device_b.id].latest_cpu_percent == 55.0
    assert rows[device_b.id].open_alert_count == 0
