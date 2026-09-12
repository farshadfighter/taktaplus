from __future__ import annotations

import httpx

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
        transport: httpx.BaseTransport | None = None,
    ) -> None:
        self.base_url = f"https://{host}:{port}"
        self.token = token
        self.verify_tls = verify_tls
        self.vdom = vdom
        self._transport = transport  # test seam; None uses a real network transport

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
