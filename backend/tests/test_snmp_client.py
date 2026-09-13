import pytest

from app.domains.monitoring.snmp_client import SnmpPollError, poll_device


class FakeValue:
    """Mimics a pysnmp value object: some convert to float/int, some don't
    (like NoSuchObject) and raise ValueError, matching real observed
    behavior from pysnmp against a live SNMPv2c agent.
    """

    def __init__(self, raw):
        self._raw = raw

    def __float__(self):
        if self._raw is None:
            raise ValueError("no such object")
        return float(self._raw)

    def __int__(self):
        if self._raw is None:
            raise ValueError("no such object")
        return int(self._raw)


class FakeErrorStatus:
    def __init__(self, is_error: bool):
        self._is_error = is_error

    def __bool__(self):
        return self._is_error

    def prettyPrint(self):
        return "genErr"


def _fake_command_factory(*, error_indication=None, error_status=False, values=(20000, 42, 55, 1234)):
    def _stub(*args, **kwargs):
        var_binds = [(f"oid{i}", FakeValue(v)) for i, v in enumerate(values)]

        def generator():
            yield (error_indication, FakeErrorStatus(error_status), 0, var_binds)

        return generator()

    return _stub


def test_poll_device_returns_parsed_metrics():
    command = _fake_command_factory(values=(20000, 42.5, 55.0, 1234))

    metrics = poll_device("10.0.0.1", 161, "public", command=command)

    assert metrics.sys_uptime_ticks == 20000
    assert metrics.cpu_percent == 42.5
    assert metrics.memory_percent == 55.0
    assert metrics.session_count == 1234


def test_poll_device_gracefully_handles_missing_oid():
    command = _fake_command_factory(values=(20000, None, None, None))

    metrics = poll_device("10.0.0.1", 161, "public", command=command)

    assert metrics.sys_uptime_ticks == 20000
    assert metrics.cpu_percent is None
    assert metrics.memory_percent is None
    assert metrics.session_count is None


def test_poll_device_raises_on_error_indication():
    command = _fake_command_factory(error_indication="timeout")

    with pytest.raises(SnmpPollError):
        poll_device("10.0.0.1", 161, "public", command=command)


def test_poll_device_raises_on_error_status():
    command = _fake_command_factory(error_status=True)

    with pytest.raises(SnmpPollError):
        poll_device("10.0.0.1", 161, "public", command=command)
