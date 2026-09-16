import enum
import uuid
from datetime import datetime

from sqlalchemy import Boolean, DateTime, Enum, Integer, String, Text, func
from sqlalchemy.orm import Mapped, mapped_column

from app.db.base import Base
from app.db.types import GUID


class SmsProvider(str, enum.Enum):
    KAVENEGAR = "kavenegar"
    SMS_IR = "sms_ir"
    GENERIC_HTTP = "generic_http"


class TwoFactorUser(Base):
    """An end user who authenticates through taktaplus's RADIUS server
    (FortiGate admin login, SSL VPN, or IPsec VPN - see
    docs/radius-2fa.md). taktaplus validates the primary password itself
    (this *is* the primary+secondary auth backend FortiGate points at, not
    just an OTP add-on), then challenges for an SMS code.
    """

    __tablename__ = "two_factor_users"

    id: Mapped[uuid.UUID] = mapped_column(GUID(), primary_key=True, default=uuid.uuid4)
    username: Mapped[str] = mapped_column(String(128), unique=True, nullable=False)
    encrypted_password: Mapped[str] = mapped_column(Text, nullable=False)
    encrypted_mobile_number: Mapped[str] = mapped_column(Text, nullable=False)
    enabled: Mapped[bool] = mapped_column(Boolean, default=True)

    failed_attempts: Mapped[int] = mapped_column(Integer, default=0)
    locked_until: Mapped[datetime | None] = mapped_column(DateTime(timezone=True), nullable=True)

    otp_window_started_at: Mapped[datetime | None] = mapped_column(DateTime(timezone=True), nullable=True)
    otp_sent_in_window: Mapped[int] = mapped_column(Integer, default=0)

    created_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), server_default=func.now())
    updated_at: Mapped[datetime] = mapped_column(
        DateTime(timezone=True), server_default=func.now(), onupdate=func.now()
    )


class RadiusClient(Base):
    """A NAS (network access server) allowed to talk to taktaplus's RADIUS
    server - typically a FortiGate. Each gets its own shared secret.
    """

    __tablename__ = "radius_clients"

    id: Mapped[uuid.UUID] = mapped_column(GUID(), primary_key=True, default=uuid.uuid4)
    name: Mapped[str] = mapped_column(String(128), nullable=False)
    nas_ip: Mapped[str] = mapped_column(String(64), unique=True, nullable=False)
    encrypted_shared_secret: Mapped[str] = mapped_column(Text, nullable=False)
    enabled: Mapped[bool] = mapped_column(Boolean, default=True)
    created_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), server_default=func.now())


class OtpChallenge(Base):
    """A single pending (or already-resolved) OTP challenge. code_hash, not
    the plaintext code, is stored - only ever compared, never displayed
    again after sending.
    """

    __tablename__ = "otp_challenges"

    id: Mapped[uuid.UUID] = mapped_column(GUID(), primary_key=True, default=uuid.uuid4)
    username: Mapped[str] = mapped_column(String(128), nullable=False, index=True)
    state_token: Mapped[str] = mapped_column(String(64), unique=True, nullable=False)
    code_hash: Mapped[str] = mapped_column(String(64), nullable=False)
    expires_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), nullable=False)
    consumed: Mapped[bool] = mapped_column(Boolean, default=False)
    attempt_count: Mapped[int] = mapped_column(Integer, default=0)
    created_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), server_default=func.now())


class SmsGatewayConfig(Base):
    """Singleton-ish table (one row): the customer's own SMS gateway,
    entered by them - taktaplus never ships a shared/default SMS account
    (unlike the FTP source, which does have a vendor-provided default).
    """

    __tablename__ = "sms_gateway_config"

    id: Mapped[uuid.UUID] = mapped_column(GUID(), primary_key=True, default=uuid.uuid4)
    provider: Mapped[SmsProvider] = mapped_column(Enum(SmsProvider), default=SmsProvider.GENERIC_HTTP)

    # Kavenegar: just needs an API key.
    encrypted_kavenegar_api_key: Mapped[str | None] = mapped_column(Text, nullable=True)
    kavenegar_sender: Mapped[str | None] = mapped_column(String(32), nullable=True)

    # sms.ir: an API key plus the account's line number (required by their
    # /v1/send/bulk endpoint - see sms_gateway.py for the verified request
    # shape).
    encrypted_smsir_api_key: Mapped[str | None] = mapped_column(Text, nullable=True)
    smsir_line_number: Mapped[str | None] = mapped_column(String(32), nullable=True)

    # generic_http: a URL template with {mobile} and {code} placeholders,
    # for any provider that isn't Kavenegar - covers "customer brings their
    # own gateway" without taktaplus needing to know its exact API shape.
    generic_method: Mapped[str] = mapped_column(String(8), default="GET")
    generic_url_template: Mapped[str | None] = mapped_column(Text, nullable=True)
    generic_auth_header_name: Mapped[str | None] = mapped_column(String(128), nullable=True)
    encrypted_generic_auth_header_value: Mapped[str | None] = mapped_column(Text, nullable=True)

    updated_at: Mapped[datetime] = mapped_column(
        DateTime(timezone=True), server_default=func.now(), onupdate=func.now()
    )
