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


class StubShell:
    """Serves banner_response until send() is called, then switches to
    post_send_response - mimics a real CLI session where the login banner
    arrives before any command is sent, and the command's own output only
    arrives after it.
    """

    def __init__(self, *, banner_response: bytes, post_send_response: bytes):
        self._banner_response = banner_response
        self._post_send_response = post_send_response
        self._sent = False
        self.sent: list[str] = []

    def send(self, data: str) -> None:
        self.sent.append(data)
        self._sent = True

    def recv_ready(self) -> bool:
        return bool(self._banner_response) or (self._sent and bool(self._post_send_response))

    def recv(self, n: int) -> bytes:
        if self._banner_response:
            chunk, self._banner_response = self._banner_response, b""
            return chunk
        if self._sent and self._post_send_response:
            chunk, self._post_send_response = self._post_send_response, b""
            return chunk
        return b""


class StubSSHClient:
    def __init__(self, *, banner_response: bytes = b"FGT-1 # ", post_send_response: bytes = b""):
        self._banner_response = banner_response
        self._post_send_response = post_send_response
        self.closed = False

    def set_missing_host_key_policy(self, policy) -> None:
        pass

    def connect(self, host, port, username, password, timeout) -> None:
        pass

    def invoke_shell(self) -> StubShell:
        return StubShell(banner_response=self._banner_response, post_send_response=self._post_send_response)

    def close(self) -> None:
        self.closed = True


class RaisingSSHClient:
    def set_missing_host_key_policy(self, policy) -> None:
        pass

    def connect(self, *a, **k):
        import paramiko

        raise paramiko.SSHException("auth failed")


def test_fortigate_push_signature_via_ftp_success():
    factory = lambda: StubSSHClient(post_send_response=b"Command executed successfully\r\nFGT-1 # ")  # noqa: E731
    driver = FortiGateDriver(
        host="10.0.0.1",
        port=443,
        token="tok",
        verify_tls=False,
        ssh_username="admin",
        ssh_password="pw",
        ssh_client_factory=factory,
    )

    output = driver.push_signature_via_ftp(
        package_type="ips",
        filename="ips.pkg",
        relay_host="10.0.0.50",
        relay_port=21,
        relay_username="relay",
        relay_password="relaypw",
        banner_idle_seconds=0.1,
        banner_max_seconds=0.3,
        response_idle_seconds=0.1,
        response_max_seconds=0.3,
    )

    assert "Command executed successfully" in output


def test_fortigate_push_signature_via_ftp_detects_failure_in_output():
    factory = lambda: StubSSHClient(post_send_response=b"Error: could not connect to FTP server\r\n")  # noqa: E731
    driver = FortiGateDriver(
        host="10.0.0.1",
        port=443,
        token="tok",
        verify_tls=False,
        ssh_username="admin",
        ssh_password="pw",
        ssh_client_factory=factory,
    )

    with pytest.raises(DeviceConnectionError):
        driver.push_signature_via_ftp(
            package_type="ips",
            filename="ips.pkg",
            relay_host="10.0.0.50",
            relay_port=21,
            relay_username="relay",
            relay_password="relaypw",
            banner_idle_seconds=0.1,
            banner_max_seconds=0.3,
            response_idle_seconds=0.1,
            response_max_seconds=0.3,
        )


def test_fortigate_push_signature_via_ftp_requires_ssh_credentials():
    driver = FortiGateDriver(host="10.0.0.1", port=443, token="tok", verify_tls=False)

    with pytest.raises(DeviceConnectionError):
        driver.push_signature_via_ftp(
            package_type="ips", filename="ips.pkg", relay_host="h", relay_port=21, relay_username="u", relay_password="p"
        )


def test_fortigate_push_signature_via_ftp_raises_on_ssh_connect_failure():
    driver = FortiGateDriver(
        host="10.0.0.1",
        port=443,
        token="tok",
        verify_tls=False,
        ssh_username="admin",
        ssh_password="wrong",
        ssh_client_factory=RaisingSSHClient,
    )

    with pytest.raises(DeviceConnectionError):
        driver.push_signature_via_ftp(
            package_type="ips", filename="ips.pkg", relay_host="h", relay_port=21, relay_username="u", relay_password="p"
        )


def test_fortigate_push_firmware_success():
    def handler(request: httpx.Request) -> httpx.Response:
        assert request.url.path == "/api/v2/monitor/system/firmware/upgrade"
        body = request.read()
        assert b"file_content" in body
        return httpx.Response(200, json={"status": "success"})

    driver = FortiGateDriver(
        host="10.0.0.1", port=443, token="tok", verify_tls=False, transport=httpx.MockTransport(handler)
    )

    driver.push_firmware(b"fake-firmware-image")  # should not raise


def test_fortigate_list_local_users_merges_admins_and_password_local_users():
    def handler(request: httpx.Request) -> httpx.Response:
        if request.url.path == "/api/v2/cmdb/system/admin":
            return httpx.Response(
                200,
                json={"results": [{"name": "admin", "sms-phone": "0912"}, {"name": "netops"}]},
            )
        if request.url.path == "/api/v2/cmdb/user/local":
            return httpx.Response(
                200,
                json={
                    "results": [
                        {"name": "vpnuser1", "type": "password", "sms-phone": ""},
                        {"name": "radius-backed", "type": "radius"},
                    ]
                },
            )
        return httpx.Response(404)

    driver = FortiGateDriver(
        host="10.0.0.1", port=443, token="tok", verify_tls=False, transport=httpx.MockTransport(handler)
    )

    candidates = driver.list_local_users()

    by_username = {c.username: c for c in candidates}
    assert by_username["admin"].source == "admin"
    assert by_username["admin"].existing_mobile == "0912"
    assert by_username["netops"].existing_mobile is None
    assert by_username["vpnuser1"].source == "local_user"
    assert "radius-backed" not in by_username  # not a password-type local user


def test_fortigate_list_local_users_raises_on_invalid_token():
    transport = httpx.MockTransport(lambda request: httpx.Response(401))
    driver = FortiGateDriver(host="10.0.0.1", port=443, token="bad", verify_tls=False, transport=transport)
    with pytest.raises(DeviceConnectionError):
        driver.list_local_users()


def test_fortiweb_list_local_users_not_implemented():
    driver = FortiWebDriver(host="10.0.0.2", port=443, username="admin", password="x", verify_tls=False)
    with pytest.raises(NotImplementedError):
        driver.list_local_users()


def test_fortiweb_push_signature_success():
    def handler(request: httpx.Request) -> httpx.Response:
        if request.url.path == "/logincheck":
            return httpx.Response(200, headers={"set-cookie": "ccsrftoken=abc123; Path=/"})
        if request.url.path == "/api/v2.0/system/fortiguard/signature/update":
            return httpx.Response(200, json={"status": "success"})
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

    driver.push_signature(b"fake-sig-package")  # should not raise


def test_fortiweb_push_firmware_success():
    def handler(request: httpx.Request) -> httpx.Response:
        if request.url.path == "/logincheck":
            return httpx.Response(200, headers={"set-cookie": "ccsrftoken=abc123; Path=/"})
        if request.url.path == "/api/v2.0/system/firmware/upgrade":
            return httpx.Response(200, json={"status": "success"})
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

    driver.push_firmware(b"fake-firmware-image")  # should not raise
