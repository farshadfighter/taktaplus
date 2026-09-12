import uuid
from datetime import datetime

from pydantic import BaseModel, Field, model_validator

from app.domains.devices.models import DeviceStatus, VendorType


class DeviceCreate(BaseModel):
    name: str
    vendor_type: VendorType
    host: str
    port: int = 443
    verify_tls: bool = False
    vdom: str = "root"

    # FortiGate
    api_token: str | None = None
    # FortiWeb
    username: str | None = None
    password: str | None = None

    @model_validator(mode="after")
    def _check_credentials_match_vendor(self) -> "DeviceCreate":
        if self.vendor_type == VendorType.FORTIGATE and not self.api_token:
            raise ValueError("برای FortiGate باید api_token وارد شود")
        if self.vendor_type == VendorType.FORTIWEB and not (self.username and self.password):
            raise ValueError("برای FortiWeb باید username و password وارد شود")
        return self


class DeviceOut(BaseModel):
    id: uuid.UUID
    name: str
    vendor_type: VendorType
    host: str
    port: int
    verify_tls: bool
    vdom: str
    status: DeviceStatus
    last_checked_at: datetime | None
    last_error: str
    firmware_version: str
    serial_number: str
    reported_hostname: str
    created_at: datetime

    model_config = {"from_attributes": True}


class DeviceTestResult(BaseModel):
    success: bool
    status: DeviceStatus
    firmware_version: str = ""
    serial_number: str = ""
    reported_hostname: str = ""
    error: str = Field(default="")
