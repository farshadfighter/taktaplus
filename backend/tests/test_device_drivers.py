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
