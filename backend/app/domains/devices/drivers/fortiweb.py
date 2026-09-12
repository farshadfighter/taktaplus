from __future__ import annotations

import httpx

from app.domains.devices.drivers.base import DeviceConnectionError, DeviceStatusInfo, FortinetDriver

# UNVERIFIED AGAINST REAL HARDWARE - see the module docstring below.
# FortiWeb's on-box management REST API does not document a stable Bearer
# token admin the way FortiGate's "REST API Admin" does (what exists under
# API Gateway > API User is for protecting the *customer's* backend APIs
# through FortiWeb as a WAF, unrelated to managing FortiWeb itself).
# The mechanism implemented here - POST /logincheck with form-encoded
# credentials, then a CSRF cookie/header pair on the session - mirrors the
# classic FortiOS-family cookie login used across FortiGate/FortiWeb/etc.
# before REST API token admins existed. This MUST be confirmed against the
# real FortiWeb hardware available for testing; if that firmware instead
# exposes a Bearer-token REST API admin, swap this driver's implementation
# for one shaped like FortiGateDriver - the rest of the codebase only
# depends on the FortinetDriver interface, not on how auth happens inside.
STATUS_ENDPOINT = "/api/v2.0/system/status"


class FortiWebDriver(FortinetDriver):
    def __init__(
        self,
        *,
        host: str,
        port: int,
        username: str,
        password: str,
        verify_tls: bool,
        transport: httpx.BaseTransport | None = None,
    ) -> None:
        self.base_url = f"https://{host}:{port}"
        self.username = username
        self.password = password
        self.verify_tls = verify_tls
        self._transport = transport  # test seam; None uses a real network transport

    def test_connection(self) -> DeviceStatusInfo:
        try:
            with httpx.Client(
                base_url=self.base_url, verify=self.verify_tls, timeout=10.0, transport=self._transport
            ) as client:
                login = client.post(
                    "/logincheck",
                    data={"username": self.username, "password": self.password},
                )
                if login.status_code != 200 or "ccsrftoken" not in client.cookies:
                    raise DeviceConnectionError("ورود به FortiWeb ناموفق بود - نام کاربری/رمز عبور را بررسی کنید")

                csrf_token = client.cookies["ccsrftoken"].strip('"')
                response = client.get(STATUS_ENDPOINT, headers={"X-CSRFTOKEN": csrf_token})
                client.post("/logout")
        except httpx.HTTPError as exc:
            raise DeviceConnectionError(f"اتصال به FortiWeb برقرار نشد: {exc}") from exc

        if response.status_code != 200:
            raise DeviceConnectionError(f"FortiWeb پاسخ غیرمنتظره {response.status_code} برگرداند")

        try:
            payload = response.json()
            data = payload.get("results", payload)
        except ValueError as exc:
            raise DeviceConnectionError("پاسخ FortiWeb قابل تفسیر نبود") from exc

        return DeviceStatusInfo(
            firmware_version=data.get("version", data.get("Version", "")),
            serial_number=data.get("serial", data.get("Serial Number", "")),
            hostname=data.get("hostname", data.get("Host Name", "")),
        )
