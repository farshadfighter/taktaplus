"""HTTP client for the (separately deployed) taktaplus License Server.

The License Server is a standalone product, built and operated by the
vendor, not shipped to customers. Until it exists, dev/mock-license-server
implements the same contract for local testing. See docs/licensing.md for
the full API contract this client depends on - keep both in sync.
"""

from __future__ import annotations

from dataclasses import dataclass
from datetime import datetime

import httpx

from app.core.config import get_settings


class LicenseServerError(RuntimeError):
    pass


class LicenseSuspended(LicenseServerError):
    pass


class LicenseNotFound(LicenseServerError):
    pass


@dataclass
class LicenseGrant:
    token: str
    fortigate_max_devices: int
    fortiweb_max_devices: int
    sms2fa_max_users: int
    issued_at: datetime
    expires_at: datetime


def _parse_grant(payload: dict) -> LicenseGrant:
    return LicenseGrant(
        token=payload["token"],
        fortigate_max_devices=payload["fortigate_max_devices"],
        fortiweb_max_devices=payload["fortiweb_max_devices"],
        sms2fa_max_users=payload["sms2fa_max_users"],
        issued_at=datetime.fromisoformat(payload["issued_at"]),
        expires_at=datetime.fromisoformat(payload["expires_at"]),
    )


class LicenseServerClient:
    def __init__(self, base_url: str | None = None, timeout: float = 10.0) -> None:
        self.base_url = (base_url or get_settings().license_server_url).rstrip("/")
        self.timeout = timeout

    def _post(self, path: str, json: dict) -> dict:
        try:
            response = httpx.post(f"{self.base_url}{path}", json=json, timeout=self.timeout)
        except httpx.HTTPError as exc:
            raise LicenseServerError(f"license server unreachable: {exc}") from exc

        if response.status_code == 404:
            raise LicenseNotFound(response.json().get("detail", "لایسنس یافت نشد"))
        if response.status_code == 403:
            raise LicenseSuspended(response.json().get("detail", "لایسنس معلق شده است"))
        if response.status_code >= 400:
            raise LicenseServerError(f"license server returned {response.status_code}: {response.text}")
        return response.json()

    def activate(self, *, fingerprint: str, customer_key: str) -> LicenseGrant:
        payload = self._post("/api/v1/activate", {"fingerprint": fingerprint, "customer_key": customer_key})
        return _parse_grant(payload)

    def heartbeat(self, *, fingerprint: str, current_token: str) -> LicenseGrant:
        payload = self._post(
            "/api/v1/heartbeat", {"fingerprint": fingerprint, "current_token": current_token}
        )
        return _parse_grant(payload)
