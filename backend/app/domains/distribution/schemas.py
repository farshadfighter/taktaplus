import uuid
from datetime import datetime

from pydantic import BaseModel, model_validator

from app.domains.devices.models import VendorType
from app.domains.distribution.models import PackageSource, PushStatus


class FtpSourceConfigUpdate(BaseModel):
    use_custom: bool
    host: str = ""
    port: int = 21
    username: str = ""
    password: str | None = None
    remote_path: str = "/"

    @model_validator(mode="after")
    def _require_host_when_custom(self) -> "FtpSourceConfigUpdate":
        if self.use_custom and not self.host:
            raise ValueError("برای استفاده از FTP سفارشی باید آدرس سرور وارد شود")
        return self


class FtpSourceConfigOut(BaseModel):
    use_custom: bool
    host: str
    port: int
    username: str
    remote_path: str


class PackageOut(BaseModel):
    id: uuid.UUID
    vendor_type: VendorType
    package_type: str
    filename: str
    version: str
    checksum_sha256: str
    size_bytes: int
    source: PackageSource
    uploaded_by: str
    fetched_at: datetime

    model_config = {"from_attributes": True}


class PushRecordOut(BaseModel):
    id: uuid.UUID
    device_id: uuid.UUID
    package_id: uuid.UUID
    pushed_at: datetime
    pushed_by: str
    status: PushStatus
    error_message: str
    post_push_check_ok: bool | None

    model_config = {"from_attributes": True}
