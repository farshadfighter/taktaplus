import httpx
import pytest

from app.domains.devices.drivers.base import DeviceConnectionError
from app.domains.devices.drivers.fortigate import FortiGateDriver
from app.domains.devices.drivers.fortiweb import FortiWebDriver


def test_fortigate_driver_parses_status_response():
    def handler(request: httpx.Request) -> httpx.Response:
        assert request.headers["Authorization"] == "Bearer secret-token"
        assert request.url.params["vdom"] == "root"
        return httpx.Response(
            200, json={"results": {"version": "v7.2.1", "serial": "FGT1234", "hostname": "fw1"}}
        )

    driver = FortiGateDriver(
        host="10.0.0.1", port=443, token="secret-token", verify_tls=False, transport=httpx.MockTransport(handler)
    )

    info = driver.test_connection()

    assert info.firmware_version == "v7.2.1"
    assert info.serial_number == "FGT1234"
    assert info.hostname == "fw1"


def test_fortigate_driver_raises_on_invalid_token():
    transport = httpx.MockTransport(lambda request: httpx.Response(401))
    driver = FortiGateDriver(host="10.0.0.1", port=443, token="bad", verify_tls=False, transport=transport)

    with pytest.raises(DeviceConnectionError):
        driver.test_connection()


def test_fortigate_driver_raises_on_network_error():
    def handler(request: httpx.Request) -> httpx.Response:
        raise httpx.ConnectError("connection refused", request=request)

    driver = FortiGateDriver(
        host="10.0.0.1", port=443, token="x", verify_tls=False, transport=httpx.MockTransport(handler)
    )

    with pytest.raises(DeviceConnectionError):
        driver.test_connection()


def test_fortiweb_driver_login_then_status_flow():
    def handler(request: httpx.Request) -> httpx.Response:
        if request.url.path == "/logincheck" and request.method == "POST":
            return httpx.Response(200, headers={"set-cookie": "ccsrftoken=abc123; Path=/"})
        if request.url.path == "/api/v2.0/system/status" and request.method == "GET":
            assert request.headers.get("X-CSRFTOKEN") == "abc123"
            return httpx.Response(200, json={"version": "6.4.2", "serial": "FWB001", "hostname": "waf1"})
        if request.url.path == "/logout":
            return httpx.Response(200)
        return httpx.Response(404)

    driver = FortiWebDriver(
        host="10.0.0.2",
        port=443,
        username="admin",
        password="secret",
        verify_tls=False,
        transport=httpx.MockTransport(handler),
    )

    info = driver.test_connection()

    assert info.firmware_version == "6.4.2"
    assert info.serial_number == "FWB001"
    assert info.hostname == "waf1"


def test_fortiweb_driver_raises_on_failed_login():
    transport = httpx.MockTransport(lambda request: httpx.Response(401))
    driver = FortiWebDriver(
        host="10.0.0.2", port=443, username="admin", password="wrong", verify_tls=False, transport=transport
    )

    with pytest.raises(DeviceConnectionError):
        driver.test_connection()


def test_fortigate_driver_backup_returns_raw_content():
    def handler(request: httpx.Request) -> httpx.Response:
        assert request.url.path == "/api/v2/monitor/system/config/backup"
        assert request.url.params["scope"] == "global"
        return httpx.Response(200, content=b"config system global\nend\n")

    driver = FortiGateDriver(
        host="10.0.0.1", port=443, token="tok", verify_tls=False, transport=httpx.MockTransport(handler)
    )

    content = driver.backup()

    assert content == b"config system global\nend\n"


def test_fortigate_driver_backup_raises_on_empty_response():
    transport = httpx.MockTransport(lambda request: httpx.Response(200, content=b""))
    driver = FortiGateDriver(host="10.0.0.1", port=443, token="tok", verify_tls=False, transport=transport)

    with pytest.raises(DeviceConnectionError):
        driver.backup()


def test_fortigate_driver_restore_uploads_multipart_file():
    def handler(request: httpx.Request) -> httpx.Response:
        assert request.url.path == "/api/v2/monitor/system/config/restore"
        assert b"config system global" in request.content
        return httpx.Response(200)

    driver = FortiGateDriver(
        host="10.0.0.1", port=443, token="tok", verify_tls=False, transport=httpx.MockTransport(handler)
    )

    driver.restore(b"config system global\nend\n")  # should not raise


def test_fortigate_driver_restore_raises_on_forbidden():
    transport = httpx.MockTransport(lambda request: httpx.Response(403))
    driver = FortiGateDriver(host="10.0.0.1", port=443, token="tok", verify_tls=False, transport=transport)

    with pytest.raises(DeviceConnectionError):
        driver.restore(b"config")


def test_fortiweb_driver_backup_returns_raw_content():
    def handler(request: httpx.Request) -> httpx.Response:
        if request.url.path == "/logincheck":
            return httpx.Response(200, headers={"set-cookie": "ccsrftoken=abc123; Path=/"})
        if request.url.path == "/api/v2.0/system/config/backup":
            return httpx.Response(200, content=b"config system global\nend\n")
        if request.url.path == "/logout":
            return httpx.Response(200)
        return httpx.Response(404)

    driver = FortiWebDriver(
        host="10.0.0.2",
        port=443,
        username="admin",
        password="secret",
        verify_tls=False,
        transport=httpx.MockTransport(handler),
    )

    assert driver.backup() == b"config system global\nend\n"


def test_fortiweb_driver_restore_uploads_multipart_file():
    def handler(request: httpx.Request) -> httpx.Response:
        if request.url.path == "/logincheck":
            return httpx.Response(200, headers={"set-cookie": "ccsrftoken=abc123; Path=/"})
        if request.url.path == "/api/v2.0/system/config/restore":
            assert b"config system global" in request.content
            return httpx.Response(200)
        if request.url.path == "/logout":
            return httpx.Response(200)
        return httpx.Response(404)

    driver = FortiWebDriver(
        host="10.0.0.2",
        port=443,
        username="admin",
        password="secret",
        verify_tls=False,
        transport=httpx.MockTransport(handler),
    )

    driver.restore(b"config system global\nend\n")  # should not raise
