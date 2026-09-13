import pytest

from app.core.security import encrypt_secret
from app.domains.devices.models import Device, VendorType
from app.domains.monitoring import service
from app.domains.monitoring.models import AlertSeverity, AlertSource
from app.domains.monitoring.snmp_client import SnmpMetrics, SnmpPollError


def _make_device(db_session, **overrides) -> Device:
    defaults = dict(name="fw1", vendor_type=VendorType.FORTIGATE, host="10.0.0.1")
    defaults.update(overrides)
    device = Device(**defaults)
    db_session.add(device)
    db_session.commit()
    return device


def test_record_metric_sample_stores_values(db_session):
    device = _make_device(db_session)
    metrics = SnmpMetrics(cpu_percent=10.0, memory_percent=20.0, session_count=5, sys_uptime_ticks=100)

    sample = service.record_metric_sample(db_session, device, metrics)

    assert sample.cpu_percent == 10.0
    assert len(service.list_metrics(db_session, device.id)) == 1


def test_record_metric_sample_creates_alert_when_cpu_over_threshold(db_session):
    device = _make_device(db_session)
    metrics = SnmpMetrics(cpu_percent=95.0, memory_percent=10.0, session_count=None, sys_uptime_ticks=None)

    service.record_metric_sample(db_session, device, metrics)

    alerts = service.list_alerts(db_session, device.id)
    assert len(alerts) == 1
    assert alerts[0].source == AlertSource.POLL_THRESHOLD
    assert alerts[0].severity == AlertSeverity.WARNING


def test_record_metric_sample_no_alert_when_under_threshold(db_session):
    device = _make_device(db_session)
    metrics = SnmpMetrics(cpu_percent=10.0, memory_percent=10.0, session_count=None, sys_uptime_ticks=None)

    service.record_metric_sample(db_session, device, metrics)

    assert service.list_alerts(db_session, device.id) == []


def test_record_poll_failure_creates_critical_alert(db_session):
    device = _make_device(db_session)

    service.record_poll_failure(db_session, device, "timeout")

    alerts = service.list_alerts(db_session, device.id)
    assert len(alerts) == 1
    assert alerts[0].source == AlertSource.POLL_UNREACHABLE
    assert alerts[0].severity == AlertSeverity.CRITICAL


def test_acknowledge_alert(db_session):
    device = _make_device(db_session)
    alert = service.record_poll_failure(db_session, device, "timeout")
    assert alert.acknowledged is False

    acked = service.acknowledge_alert(db_session, alert)

    assert acked.acknowledged is True


def test_poll_now_requires_snmp_enabled(db_session):
    device = _make_device(db_session)

    with pytest.raises(service.SnmpNotConfiguredError):
        service.poll_now(db_session, device)


def test_poll_now_success(db_session, monkeypatch):
    device = _make_device(db_session, snmp_enabled=True, encrypted_snmp_community=encrypt_secret("public"))
    monkeypatch.setattr(
        "app.domains.monitoring.service.poll_device",
        lambda *a, **k: SnmpMetrics(cpu_percent=1.0, memory_percent=2.0, session_count=3, sys_uptime_ticks=4),
    )

    sample = service.poll_now(db_session, device)

    assert sample.cpu_percent == 1.0


def test_poll_now_records_failure_and_reraises(db_session, monkeypatch):
    device = _make_device(db_session, snmp_enabled=True, encrypted_snmp_community=encrypt_secret("public"))

    def _raise(*a, **k):
        raise SnmpPollError("timeout")

    monkeypatch.setattr("app.domains.monitoring.service.poll_device", _raise)

    with pytest.raises(SnmpPollError):
        service.poll_now(db_session, device)

    alerts = service.list_alerts(db_session, device.id)
    assert len(alerts) == 1
    assert alerts[0].source == AlertSource.POLL_UNREACHABLE
