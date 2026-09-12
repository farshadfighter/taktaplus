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
