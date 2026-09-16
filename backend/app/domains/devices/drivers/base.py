from __future__ import annotations

from dataclasses import dataclass


class DeviceConnectionError(RuntimeError):
    """Raised by a driver when the device can't be reached or rejects auth.
    Callers catch this and record it as a failed connectivity test rather
    than letting it bubble up as a 500.
    """


@dataclass
class DeviceStatusInfo:
    firmware_version: str
    serial_number: str
    hostname: str


@dataclass
class LocalUserCandidate:
    """A local admin or VPN user account that already exists on the device
    - a candidate to link to a taktaplus TwoFactorUser instead of typing a
    username from scratch (see radius/router.py's /local-users endpoint).
    """

    username: str
    source: str  # "admin" | "local_user"
    existing_mobile: str | None = None


class FortinetDriver:
    """Common interface every vendor driver implements. Phase 4
    (signature/firmware push) will add methods here rather than inventing a
    second driver abstraction.
    """

    def test_connection(self) -> DeviceStatusInfo:
        raise NotImplementedError

    def backup(self) -> bytes:
        """Return the full device configuration as raw bytes."""
        raise NotImplementedError

    def restore(self, content: bytes) -> None:
        """Push a previously captured configuration back to the device."""
        raise NotImplementedError

    def list_local_users(self) -> list[LocalUserCandidate]:
        """List existing local admin/VPN user accounts already configured on
        the device - taktaplus can never read their passwords back (Fortinet
        never exposes them, one-way hashed), only the username and,
        occasionally, a phone number if the device's own native SMS 2FA
        feature already has one set for that account.
        """
        raise NotImplementedError
