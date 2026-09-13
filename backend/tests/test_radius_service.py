from datetime import datetime, timedelta, timezone

import pytest

from app.core.security import encrypt_secret
from app.domains.licensing.models import License
from app.domains.radius import service
from app.domains.radius.models import SmsGatewayConfig, SmsProvider, TwoFactorUser


def _grant_license(db_session, **overrides):
    defaults = dict(
        fingerprint="fp",
        customer_key="cust",
        fortigate_max_devices=2,
        fortiweb_max_devices=1,
        sms2fa_max_users=5,
        issued_at=datetime.now(timezone.utc),
        expires_at=datetime.now(timezone.utc) + timedelta(days=300),
        cached_token="tok",
        cached_token_issued_at=datetime.now(timezone.utc),
        last_heartbeat_at=datetime.now(timezone.utc),
        last_heartbeat_ok=True,
    )
    defaults.update(overrides)
    db_session.add(License(**defaults))
    db_session.commit()


def _make_user(db_session, *, username="alice", password="Secret123", mobile="09120000000") -> TwoFactorUser:
    user = TwoFactorUser(
        username=username,
        encrypted_password=encrypt_secret(password),
        encrypted_mobile_number=encrypt_secret(mobile),
    )
    db_session.add(user)
    db_session.commit()
    db_session.refresh(user)
    return user


def _make_sms_config(db_session) -> SmsGatewayConfig:
    config = SmsGatewayConfig(provider=SmsProvider.GENERIC_HTTP, generic_url_template="https://example.test/{mobile}/{code}")
    db_session.add(config)
    db_session.commit()
    return config


@pytest.fixture(autouse=True)
def _stub_sms(monkeypatch):
    sent = []

    def _fake_send(config, *, mobile_number, code):
        sent.append((mobile_number, code))

    monkeypatch.setattr(service, "send_otp_sms", _fake_send)
    return sent


def test_exact_password_issues_challenge(db_session, _stub_sms):
    _grant_license(db_session)
    _make_user(db_session)
    _make_sms_config(db_session)

    result = service.authenticate(db_session, username="alice", password="Secret123")

    assert result.result == "challenge"
    assert result.state_token
    assert len(_stub_sms) == 1


def test_correct_otp_with_state_accepts(db_session, _stub_sms):
    _grant_license(db_session)
    _make_user(db_session)
    _make_sms_config(db_session)

    challenge = service.authenticate(db_session, username="alice", password="Secret123")
    code = _stub_sms[0][1]

    result = service.authenticate(db_session, username="alice", password=code, state_token=challenge.state_token)

    assert result.result == "accept"


def test_wrong_otp_rejects_and_increments_failure(db_session, _stub_sms):
    _grant_license(db_session)
    user = _make_user(db_session)
    _make_sms_config(db_session)

    challenge = service.authenticate(db_session, username="alice", password="Secret123")
    result = service.authenticate(db_session, username="alice", password="00000", state_token=challenge.state_token)

    assert result.result == "reject"
    db_session.refresh(user)
    assert user.failed_attempts == 1


def test_too_many_wrong_otp_attempts_consumes_challenge(db_session, _stub_sms):
    _grant_license(db_session)
    _make_user(db_session)
    _make_sms_config(db_session)

    challenge = service.authenticate(db_session, username="alice", password="Secret123")
    state_token = challenge.state_token

    from app.core.config import get_settings

    max_attempts = get_settings().otp_max_attempts_per_challenge
    for _ in range(max_attempts):
        service.authenticate(db_session, username="alice", password="00000", state_token=state_token)

    # challenge is now consumed - even the correct code no longer works
    code = _stub_sms[0][1]
    result = service.authenticate(db_session, username="alice", password=code, state_token=state_token)
    assert result.result == "reject"


def test_concatenated_password_and_otp_accepts(db_session, _stub_sms):
    """IPsec doesn't forward Access-Challenge, so its usable flow is:
    submit primary password alone (triggers SMS but this attempt itself is
    rejected upstream by the NAS since no challenge went through), then
    submit primary+OTP concatenated in one request once the code arrived.
    """
    _grant_license(db_session)
    _make_user(db_session)
    _make_sms_config(db_session)

    service.authenticate(db_session, username="alice", password="Secret123")
    code = _stub_sms[0][1]

    result = service.authenticate(db_session, username="alice", password=f"Secret123{code}")

    assert result.result == "accept"


def test_rate_limit_exceeded_rejects(db_session, _stub_sms):
    _grant_license(db_session)
    _make_user(db_session)
    _make_sms_config(db_session)

    from app.core.config import get_settings

    limit = get_settings().otp_sms_rate_limit_per_hour
    for _ in range(limit):
        result = service.authenticate(db_session, username="alice", password="Secret123")
        assert result.result == "challenge"

    result = service.authenticate(db_session, username="alice", password="Secret123")
    assert result.result == "reject"


def test_lockout_after_repeated_failures(db_session, _stub_sms):
    _grant_license(db_session)
    _make_user(db_session)
    _make_sms_config(db_session)

    from app.core.config import get_settings

    threshold = get_settings().otp_lockout_threshold
    for _ in range(threshold):
        result = service.authenticate(db_session, username="alice", password="wrong-password")
        assert result.result == "reject"

    # even the correct password is now rejected because the account is locked
    result = service.authenticate(db_session, username="alice", password="Secret123")
    assert result.result == "reject"
    assert "قفل" in result.reply_message


def test_disabled_user_rejects(db_session, _stub_sms):
    _grant_license(db_session)
    user = _make_user(db_session)
    user.enabled = False
    db_session.commit()

    result = service.authenticate(db_session, username="alice", password="Secret123")
    assert result.result == "reject"


def test_nonexistent_user_rejects(db_session, _stub_sms):
    _grant_license(db_session)

    result = service.authenticate(db_session, username="ghost", password="whatever")
    assert result.result == "reject"
