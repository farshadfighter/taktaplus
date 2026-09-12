from __future__ import annotations

from contextlib import contextmanager
from typing import Iterator

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
#
# The backup/restore endpoint paths below are an even less certain guess -
# mirrored from FortiGate's naming under FortiWeb's /api/v2.0 prefix, since
# no concrete documented example was found for either. Verify both the path
# and the multipart field name against real hardware before relying on them.
STATUS_ENDPOINT = "/api/v2.0/system/status"
BACKUP_ENDPOINT = "/api/v2.0/system/config/backup"
RESTORE_ENDPOINT = "/api/v2.0/system/config/restore"


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

    @contextmanager
    def _session(self) -> Iterator[tuple[httpx.Client, str]]:
        with httpx.Client(
            base_url=self.base_url, verify=self.verify_tls, timeout=30.0, transport=self._transport
        ) as client:
            login = client.post("/logincheck", data={"username": self.username, "password": self.password})
            if login.status_code != 200 or "ccsrftoken" not in client.cookies:
                raise DeviceConnectionError("ورود به FortiWeb ناموفق بود - نام کاربری/رمز عبور را بررسی کنید")

            csrf_token = client.cookies["ccsrftoken"].strip('"')
            try:
                yield client, csrf_token
            finally:
                client.post("/logout")

    def test_connection(self) -> DeviceStatusInfo:
        try:
            with self._session() as (client, csrf_token):
                response = client.get(STATUS_ENDPOINT, headers={"X-CSRFTOKEN": csrf_token})
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

    def backup(self) -> bytes:
        try:
            with self._session() as (client, csrf_token):
                response = client.get(BACKUP_ENDPOINT, headers={"X-CSRFTOKEN": csrf_token})
        except httpx.HTTPError as exc:
            raise DeviceConnectionError(f"دریافت بکاپ از FortiWeb ناموفق بود: {exc}") from exc

        if response.status_code != 200:
            raise DeviceConnectionError(f"FortiWeb پاسخ غیرمنتظره {response.status_code} برگرداند")
        if not response.content:
            raise DeviceConnectionError("FortiWeb یک بکاپ خالی برگرداند")

        return response.content

    def restore(self, content: bytes) -> None:
        try:
            with self._session() as (client, csrf_token):
                response = client.post(
                    RESTORE_ENDPOINT,
                    headers={"X-CSRFTOKEN": csrf_token},
                    files={"file": ("backup.conf", content, "text/plain")},
                )
        except httpx.HTTPError as exc:
            raise DeviceConnectionError(f"ریستور روی FortiWeb ناموفق بود: {exc}") from exc

        if response.status_code != 200:
            raise DeviceConnectionError(f"FortiWeb پاسخ غیرمنتظره {response.status_code} برگرداند")
