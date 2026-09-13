"""Send an OTP code via the customer's configured SMS gateway.

Kavenegar's API shape below is confirmed against its official documentation
(POST https://api.kavenegar.com/v1/{api_key}/sms/send.json with `receptor`
and `message` form fields). generic_http is the fallback for any other
provider - the customer supplies a URL template with {mobile}/{code}
placeholders (and optionally sender info baked into the template itself),
since taktaplus can't know every provider's exact API shape in advance.
"""

from __future__ import annotations

import httpx

from app.core.security import decrypt_secret
from app.domains.radius.models import SmsGatewayConfig, SmsProvider


class SmsSendError(RuntimeError):
    pass


def send_otp_sms(config: SmsGatewayConfig, *, mobile_number: str, code: str) -> None:
    if config.provider == SmsProvider.KAVENEGAR:
        _send_via_kavenegar(config, mobile_number=mobile_number, code=code)
    elif config.provider == SmsProvider.GENERIC_HTTP:
        _send_via_generic_http(config, mobile_number=mobile_number, code=code)
    else:
        raise SmsSendError(f"سرویس پیامکی ناشناخته: {config.provider}")


def _send_via_kavenegar(config: SmsGatewayConfig, *, mobile_number: str, code: str) -> None:
    if not config.encrypted_kavenegar_api_key:
        raise SmsSendError("کلید API کاوه‌نگار تنظیم نشده است")

    api_key = decrypt_secret(config.encrypted_kavenegar_api_key)
    message = f"کد ورود شما: {code}"
    payload = {"receptor": mobile_number, "message": message}
    if config.kavenegar_sender:
        payload["sender"] = config.kavenegar_sender

    try:
        response = httpx.post(f"https://api.kavenegar.com/v1/{api_key}/sms/send.json", data=payload, timeout=15.0)
    except httpx.HTTPError as exc:
        raise SmsSendError(f"ارسال پیامک از طریق کاوه‌نگار ناموفق بود: {exc}") from exc

    if response.status_code != 200:
        raise SmsSendError(f"کاوه‌نگار پاسخ غیرمنتظره {response.status_code} برگرداند")


def _send_via_generic_http(config: SmsGatewayConfig, *, mobile_number: str, code: str) -> None:
    if not config.generic_url_template:
        raise SmsSendError("آدرس سرویس پیامکی سفارشی تنظیم نشده است")

    url = config.generic_url_template.format(mobile=mobile_number, code=code)
    headers = {}
    if config.generic_auth_header_name and config.encrypted_generic_auth_header_value:
        headers[config.generic_auth_header_name] = decrypt_secret(config.encrypted_generic_auth_header_value)

    try:
        if config.generic_method.upper() == "POST":
            response = httpx.post(url, headers=headers, timeout=15.0)
        else:
            response = httpx.get(url, headers=headers, timeout=15.0)
    except httpx.HTTPError as exc:
        raise SmsSendError(f"ارسال پیامک از طریق سرویس سفارشی ناموفق بود: {exc}") from exc

    if response.status_code >= 400:
        raise SmsSendError(f"سرویس پیامکی سفارشی پاسخ غیرمنتظره {response.status_code} برگرداند")
