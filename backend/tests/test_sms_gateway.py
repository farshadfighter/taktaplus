import httpx
import pytest

from app.core.security import encrypt_secret
from app.domains.radius.models import SmsGatewayConfig, SmsProvider
from app.domains.radius.sms_gateway import SmsSendError, send_otp_sms


class _FakeResponse:
    def __init__(self, status_code: int, json_body=None):
        self.status_code = status_code
        self._json_body = json_body

    def json(self):
        if self._json_body is None:
            raise ValueError("no json body")
        return self._json_body


def test_kavenegar_success(monkeypatch):
    captured = {}

    def _fake_post(url, data=None, timeout=None):
        captured["url"] = url
        captured["data"] = data
        return _FakeResponse(200)

    monkeypatch.setattr(httpx, "post", _fake_post)

    config = SmsGatewayConfig(
        provider=SmsProvider.KAVENEGAR,
        encrypted_kavenegar_api_key=encrypt_secret("apikey"),
        kavenegar_sender="1000",
    )

    send_otp_sms(config, mobile_number="0912", code="12345")

    assert captured["url"] == "https://api.kavenegar.com/v1/apikey/sms/send.json"
    assert captured["data"]["receptor"] == "0912"
    assert "12345" in captured["data"]["message"]
    assert captured["data"]["sender"] == "1000"


def test_kavenegar_missing_api_key_raises():
    config = SmsGatewayConfig(provider=SmsProvider.KAVENEGAR, encrypted_kavenegar_api_key=None)
    with pytest.raises(SmsSendError):
        send_otp_sms(config, mobile_number="0912", code="12345")


def test_kavenegar_non_200_raises(monkeypatch):
    monkeypatch.setattr(httpx, "post", lambda *a, **k: _FakeResponse(400))
    config = SmsGatewayConfig(provider=SmsProvider.KAVENEGAR, encrypted_kavenegar_api_key=encrypt_secret("k"))
    with pytest.raises(SmsSendError):
        send_otp_sms(config, mobile_number="0912", code="12345")


def test_sms_ir_success(monkeypatch):
    captured = {}

    def _fake_post(url, json=None, headers=None, timeout=None):
        captured["url"] = url
        captured["json"] = json
        captured["headers"] = headers
        return _FakeResponse(200, {"status": 1, "message": "OK", "data": {"packId": "x", "messageIds": [1], "cost": 100}})

    monkeypatch.setattr(httpx, "post", _fake_post)

    config = SmsGatewayConfig(
        provider=SmsProvider.SMS_IR,
        encrypted_smsir_api_key=encrypt_secret("smsir-key"),
        smsir_line_number="30001234",
    )

    send_otp_sms(config, mobile_number="09120000000", code="54321")

    assert captured["url"] == "https://api.sms.ir/v1/send/bulk"
    assert captured["headers"]["X-API-KEY"] == "smsir-key"
    assert captured["json"]["lineNumber"] == 30001234
    assert captured["json"]["mobiles"] == ["09120000000"]
    assert "54321" in captured["json"]["messageText"]


def test_sms_ir_missing_api_key_raises():
    config = SmsGatewayConfig(provider=SmsProvider.SMS_IR, encrypted_smsir_api_key=None, smsir_line_number="30001234")
    with pytest.raises(SmsSendError):
        send_otp_sms(config, mobile_number="0912", code="12345")


def test_sms_ir_missing_line_number_raises():
    config = SmsGatewayConfig(provider=SmsProvider.SMS_IR, encrypted_smsir_api_key=encrypt_secret("k"), smsir_line_number=None)
    with pytest.raises(SmsSendError):
        send_otp_sms(config, mobile_number="0912", code="12345")


def test_sms_ir_non_numeric_line_number_raises():
    config = SmsGatewayConfig(provider=SmsProvider.SMS_IR, encrypted_smsir_api_key=encrypt_secret("k"), smsir_line_number="not-a-number")
    with pytest.raises(SmsSendError):
        send_otp_sms(config, mobile_number="0912", code="12345")


def test_sms_ir_non_200_raises(monkeypatch):
    monkeypatch.setattr(httpx, "post", lambda *a, **k: _FakeResponse(500))
    config = SmsGatewayConfig(provider=SmsProvider.SMS_IR, encrypted_smsir_api_key=encrypt_secret("k"), smsir_line_number="30001234")
    with pytest.raises(SmsSendError):
        send_otp_sms(config, mobile_number="0912", code="12345")


def test_sms_ir_envelope_error_status_raises(monkeypatch):
    """sms.ir always answers HTTP 200 - even a failed send - so the actual
    error signal is the envelope's own `status` field, not the HTTP status.
    """
    monkeypatch.setattr(
        httpx, "post", lambda *a, **k: _FakeResponse(200, {"status": 8, "message": "کد اعتبار پیامکی نامعتبر است", "data": None})
    )
    config = SmsGatewayConfig(provider=SmsProvider.SMS_IR, encrypted_smsir_api_key=encrypt_secret("bad-key"), smsir_line_number="30001234")
    with pytest.raises(SmsSendError):
        send_otp_sms(config, mobile_number="0912", code="12345")


def test_generic_http_get(monkeypatch):
    captured = {}

    def _fake_get(url, headers=None, timeout=None):
        captured["url"] = url
        captured["headers"] = headers
        return _FakeResponse(200)

    monkeypatch.setattr(httpx, "get", _fake_get)

    config = SmsGatewayConfig(
        provider=SmsProvider.GENERIC_HTTP,
        generic_method="GET",
        generic_url_template="https://sms.example.test/send?to={mobile}&text={code}",
    )

    send_otp_sms(config, mobile_number="0912", code="99999")

    assert captured["url"] == "https://sms.example.test/send?to=0912&text=99999"
    assert captured["headers"] == {}


def test_generic_http_post_with_auth_header(monkeypatch):
    captured = {}

    def _fake_post(url, headers=None, timeout=None):
        captured["url"] = url
        captured["headers"] = headers
        return _FakeResponse(200)

    monkeypatch.setattr(httpx, "post", _fake_post)

    config = SmsGatewayConfig(
        provider=SmsProvider.GENERIC_HTTP,
        generic_method="POST",
        generic_url_template="https://sms.example.test/{mobile}/{code}",
        generic_auth_header_name="X-Api-Key",
        encrypted_generic_auth_header_value=encrypt_secret("secretheader"),
    )

    send_otp_sms(config, mobile_number="0912", code="99999")

    assert captured["headers"] == {"X-Api-Key": "secretheader"}


def test_generic_http_missing_template_raises():
    config = SmsGatewayConfig(provider=SmsProvider.GENERIC_HTTP, generic_url_template=None)
    with pytest.raises(SmsSendError):
        send_otp_sms(config, mobile_number="0912", code="12345")


def test_generic_http_error_status_raises(monkeypatch):
    monkeypatch.setattr(httpx, "get", lambda *a, **k: _FakeResponse(500))
    config = SmsGatewayConfig(
        provider=SmsProvider.GENERIC_HTTP,
        generic_method="GET",
        generic_url_template="https://sms.example.test/{mobile}/{code}",
    )
    with pytest.raises(SmsSendError):
        send_otp_sms(config, mobile_number="0912", code="12345")
