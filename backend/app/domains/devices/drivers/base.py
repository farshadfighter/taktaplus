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
    """Common interface every vendor driver implements. Kept intentionally
    tiny for phase 1 (connectivity test only) - phase 2 (backup/restore) and
    phase 4 (signature/firmware push) will add methods here rather than
    inventing a second driver abstraction.
    """

    def test_connection(self) -> DeviceStatusInfo:
        raise NotImplementedError
