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

    def test_connection(self) -> DeviceStatusInfo:
        try:
            with httpx.Client(verify=self.verify_tls, timeout=10.0, transport=self._transport) as client:
                response = client.get(
                    f"{self.base_url}/api/v2/monitor/system/status",
                    params={"vdom": self.vdom},
                    headers={"Authorization": f"Bearer {self.token}"},
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
