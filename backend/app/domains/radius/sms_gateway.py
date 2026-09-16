"""Send an OTP code via the customer's configured SMS gateway.

Kavenegar's API shape below is confirmed against its official documentation
(POST https://api.kavenegar.com/v1/{api_key}/sms/send.json with `receptor`
and `message` form fields).

sms.ir's shape is confirmed by reading its official Go SDK's source
directly (github.com/1tzArad/sms_ir - transport.go and response.go), not
guessed and not from the (egress-blocked, so unfetchable here) apidocs.sms.ir
pages: POST https://api.sms.ir/v1/send/bulk, header `X-API-KEY: <key>`,
JSON body `{"lineNumber": <int>, "messageText": <str>, "mobiles": [<str>],
"sendDateTime": null}`, and every response - success or failure - comes
back HTTP 200 with an envelope `{"status": <int>, "message": <str>,
"data": {...}}`; the SDK's own transport layer treats `status != 1` as the
error condition, so that's what's checked here too.

generic_http is the fallback for any other provider - the customer
supplies a URL template with {mobile}/{code} placeholders (and optionally
sender info baked into the template itself), since taktaplus can't know
every provider's exact API shape in advance.
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
    elif config.provider == SmsProvider.SMS_IR:
        _send_via_sms_ir(config, mobile_number=mobile_number, code=code)
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


def _send_via_sms_ir(config: SmsGatewayConfig, *, mobile_number: str, code: str) -> None:
    if not config.encrypted_smsir_api_key:
        raise SmsSendError("کلید API سرویس sms.ir تنظیم نشده است")
    if not config.smsir_line_number:
        raise SmsSendError("شماره خط sms.ir تنظیم نشده است")

    try:
        line_number = int(config.smsir_line_number)
    except ValueError as exc:
        raise SmsSendError("شماره خط sms.ir باید عددی باشد") from exc

    api_key = decrypt_secret(config.encrypted_smsir_api_key)
    message = f"کد ورود شما: {code}"
    payload = {"lineNumber": line_number, "messageText": message, "mobiles": [mobile_number], "sendDateTime": None}
    headers = {"X-API-KEY": api_key, "Accept": "application/json"}

    try:
        response = httpx.post("https://api.sms.ir/v1/send/bulk", json=payload, headers=headers, timeout=15.0)
    except httpx.HTTPError as exc:
        raise SmsSendError(f"ارسال پیامک از طریق sms.ir ناموفق بود: {exc}") from exc

    if response.status_code != 200:
        raise SmsSendError(f"sms.ir پاسخ غیرمنتظره {response.status_code} برگرداند")

    try:
        body = response.json()
    except ValueError as exc:
        raise SmsSendError("پاسخ sms.ir قابل تفسیر نبود") from exc

    # sms.ir always answers HTTP 200, success or failure - the envelope's
    # own `status` field (1 == success) is what actually distinguishes
    # them, per its SDK's transport layer.
    if body.get("status") != 1:
        raise SmsSendError(f"sms.ir خطا برگرداند: {body.get('message', 'نامشخص')}")


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
