import uuid
from datetime import datetime

from pydantic import BaseModel, model_validator

from app.domains.radius.models import SmsProvider


class TwoFactorUserCreate(BaseModel):
    username: str
    password: str
    mobile_number: str


class TwoFactorUserOut(BaseModel):
    id: uuid.UUID
    username: str
    enabled: bool
    failed_attempts: int
    locked_until: datetime | None
    created_at: datetime

    model_config = {"from_attributes": True}


class RadiusClientCreate(BaseModel):
    name: str
    nas_ip: str
    shared_secret: str


class RadiusClientOut(BaseModel):
    id: uuid.UUID
    name: str
    nas_ip: str
    enabled: bool
    created_at: datetime

    model_config = {"from_attributes": True}


class SmsGatewayConfigUpdate(BaseModel):
    provider: SmsProvider
    kavenegar_api_key: str | None = None
    kavenegar_sender: str | None = None
    generic_method: str = "GET"
    generic_url_template: str | None = None
    generic_auth_header_name: str | None = None
    generic_auth_header_value: str | None = None

    @model_validator(mode="after")
    def _require_provider_fields(self) -> "SmsGatewayConfigUpdate":
        if self.provider == SmsProvider.GENERIC_HTTP and not self.generic_url_template:
            raise ValueError("برای سرویس پیامکی سفارشی باید الگوی آدرس تنظیم شود")
        return self


class SmsGatewayConfigOut(BaseModel):
    provider: SmsProvider
    kavenegar_sender: str | None
    generic_method: str
    generic_url_template: str | None
    generic_auth_header_name: str | None
    has_kavenegar_api_key: bool
    has_generic_auth_header_value: bool


class TestSmsRequest(BaseModel):
    mobile_number: str
