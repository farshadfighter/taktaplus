from datetime import datetime

from pydantic import BaseModel


class ActivateRequest(BaseModel):
    customer_key: str


class LicenseStatusOut(BaseModel):
    status: str
    fortigate_max_devices: int
    fortiweb_max_devices: int
    sms2fa_max_users: int
    expires_at: datetime | None
    days_until_expiry: int | None
    expiry_warning: bool
    last_heartbeat_at: datetime | None
    last_heartbeat_ok: bool
