from __future__ import annotations

import base64
import time

import httpx
import paramiko

from app.domains.devices.drivers.base import DeviceConnectionError, DeviceStatusInfo, FortinetDriver


class FortiGateDriver(FortinetDriver):
    """FortiGate REST API driver. Auth is a Bearer API token from a REST API
    Admin (System > Administrators > REST API Admin) - this is well
    documented and consistent across recent FortiOS versions, unlike
    FortiWeb's auth (see drivers/fortiweb.py).
    """

    def __init__(
        self,
        *,
        host: str,
        port: int,
        token: str,
        verify_tls: bool,
        vdom: str = "root",
        ssh_port: int = 22,
        ssh_username: str | None = None,
        ssh_password: str | None = None,
        transport: httpx.BaseTransport | None = None,
        ssh_client_factory: type | None = None,
    ) -> None:
        self.host = host
        self.base_url = f"https://{host}:{port}"
        self.token = token
        self.verify_tls = verify_tls
        self.vdom = vdom
        self.ssh_port = ssh_port
        self.ssh_username = ssh_username
        self.ssh_password = ssh_password
        self._transport = transport  # test seam; None uses a real network transport
        self._ssh_client_factory = ssh_client_factory or paramiko.SSHClient  # test seam

    def _client(self) -> httpx.Client:
        return httpx.Client(verify=self.verify_tls, timeout=30.0, transport=self._transport)

    def _auth_headers(self) -> dict[str, str]:
        return {"Authorization": f"Bearer {self.token}"}

    def test_connection(self) -> DeviceStatusInfo:
        try:
            with self._client() as client:
                response = client.get(
                    f"{self.base_url}/api/v2/monitor/system/status",
                    params={"vdom": self.vdom},
                    headers=self._auth_headers(),
                )
        except httpx.HTTPError as exc:
            raise DeviceConnectionError(f"اتصال به FortiGate برقرار نشد: {exc}") from exc

        if response.status_code == 401:
            raise DeviceConnectionError("توکن API نامعتبر است یا دسترسی کافی ندارد")
        if response.status_code != 200:
            raise DeviceConnectionError(f"FortiGate پاسخ غیرمنتظره {response.status_code} برگرداند")

        try:
            payload = response.json()["results"]
        except (KeyError, ValueError) as exc:
            raise DeviceConnectionError("پاسخ FortiGate قابل تفسیر نبود") from exc

        return DeviceStatusInfo(
            firmware_version=payload.get("version", ""),
            serial_number=payload.get("serial", ""),
            hostname=payload.get("hostname", ""),
        )

    def backup(self) -> bytes:
        # scope=global pulls the full config (all VDOMs), matching what's
        # needed to restore the device completely rather than one VDOM.
        # Documented as a plain GET in Fortinet's own curl examples, unlike
        # restore() below which is genuinely a mutating POST.
        try:
            with self._client() as client:
                response = client.get(
                    f"{self.base_url}/api/v2/monitor/system/config/backup",
                    params={"scope": "global"},
                    headers=self._auth_headers(),
                )
        except httpx.HTTPError as exc:
            raise DeviceConnectionError(f"دریافت بکاپ از FortiGate ناموفق بود: {exc}") from exc

        if response.status_code == 401:
            raise DeviceConnectionError("توکن API دسترسی لازم برای بکاپ‌گیری (sysgrp) را ندارد")
        if response.status_code != 200:
            raise DeviceConnectionError(f"FortiGate پاسخ غیرمنتظره {response.status_code} برگرداند")
        if not response.content:
            raise DeviceConnectionError("FortiGate یک بکاپ خالی برگرداند")

        return response.content

    def restore(self, content: bytes) -> None:
        # UNVERIFIED AGAINST REAL HARDWARE: the exact multipart field name and
        # required admin access profile for this endpoint vary across
        # community reports (some need a super_admin-level API user, not just
        # sysgrp). Confirm against real hardware and adjust the `files=` field
        # name below if FortiGate rejects this.
        try:
            with self._client() as client:
                response = client.post(
                    f"{self.base_url}/api/v2/monitor/system/config/restore",
                    params={"scope": "global"},
                    headers=self._auth_headers(),
                    files={"file": ("backup.conf", content, "text/plain")},
                )
        except httpx.HTTPError as exc:
            raise DeviceConnectionError(f"ریستور روی FortiGate ناموفق بود: {exc}") from exc

        if response.status_code == 401 or response.status_code == 403:
            raise DeviceConnectionError(
                "دسترسی کافی برای ریستور وجود ندارد - ممکن است نیاز به پروفایل super_admin باشد"
            )
        if response.status_code != 200:
            raise DeviceConnectionError(f"FortiGate پاسخ غیرمنتظره {response.status_code} برگرداند")

    def push_signature_via_ftp(
        self,
        *,
        package_type: str,
        filename: str,
        relay_host: str,
        relay_port: int,
        relay_username: str,
        relay_password: str,
        banner_idle_seconds: float = 2,
        banner_max_seconds: float = 5,
        response_idle_seconds: float = 3,
        response_max_seconds: float = 60,
    ) -> str:
        """Run `execute restore <type> ftp ...` over SSH so the device pulls
        the package from taktaplus's own FTP relay. UNVERIFIED AGAINST REAL
        HARDWARE: FortiOS has no REST API for this (confirmed absent from
        Fortinet's own REST API monitor documentation - only the CLI `execute
        restore` family covers it), so this is the one place in the codebase
        that talks to a FortiGate over SSH instead of the REST API. Exact
        prompt timing/output format needs confirming on real hardware; the
        connection mechanics themselves (paramiko interactive shell, command
        send, response drain) were verified against a real SSH server during
        development.
        """
        if not self.ssh_username or not self.ssh_password:
            raise DeviceConnectionError("اطلاعات SSH برای این دستگاه تنظیم نشده است")

        client = self._ssh_client_factory()
        client.set_missing_host_key_policy(paramiko.AutoAddPolicy())
        try:
            client.connect(
                self.host,
                port=self.ssh_port,
                username=self.ssh_username,
                password=self.ssh_password,
                timeout=15,
            )
        except (paramiko.SSHException, OSError) as exc:
            raise DeviceConnectionError(f"اتصال SSH به FortiGate برقرار نشد: {exc}") from exc

        try:
            shell = client.invoke_shell()
            _drain(shell, idle_seconds=banner_idle_seconds, max_seconds=banner_max_seconds)  # clear login banner/prompt

            command = f"execute restore {package_type} ftp {filename} {relay_host}:{relay_port} {relay_username} {relay_password}\n"
            shell.send(command)
            output = _drain(shell, idle_seconds=response_idle_seconds, max_seconds=response_max_seconds)
        finally:
            client.close()

        if "fail" in output.lower() or "error" in output.lower():
            raise DeviceConnectionError(f"FortiGate در اجرای دستور بازیابی خطا داد: {output.strip()}")

        return output

    def push_firmware(self, content: bytes) -> None:
        """Upload+install a firmware image via the monitor API's upload
        source (base64 file_content, not multipart - this shape is
        confirmed by Fortinet's own Ansible collection for this exact
        endpoint, not just a guess). UNVERIFIED: real hardware behavior for
        very large images (timeouts, memory) hasn't been tested - only the
        request shape is confirmed.
        """
        encoded = base64.b64encode(content).decode("ascii")
        try:
            with self._client() as client:
                response = client.post(
                    f"{self.base_url}/api/v2/monitor/system/firmware/upgrade",
                    headers=self._auth_headers(),
                    json={"source": "upload", "file_content": encoded},
                    timeout=300.0,
                )
        except httpx.HTTPError as exc:
            raise DeviceConnectionError(f"آپلود فرم‌ور به FortiGate ناموفق بود: {exc}") from exc

        if response.status_code != 200:
            raise DeviceConnectionError(f"FortiGate پاسخ غیرمنتظره {response.status_code} برگرداند")


def _drain(shell: paramiko.Channel, *, idle_seconds: float, max_seconds: float) -> str:
    """Read from an interactive SSH channel until it's quiet for
    idle_seconds or max_seconds total has elapsed - CLI sessions like this
    don't have a clean EOF to read until, so this is the standard pattern
    for interactive-shell automation.
    """
    output = b""
    elapsed = 0.0
    idle = 0.0
    while elapsed < max_seconds and idle < idle_seconds:
        if shell.recv_ready():
            output += shell.recv(65536)
            idle = 0.0
        else:
            time.sleep(0.2)
            idle += 0.2
        elapsed += 0.2
    return output.decode(errors="replace")
