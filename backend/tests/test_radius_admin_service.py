from datetime import datetime, timedelta, timezone

import pytest

from app.core.security import decrypt_secret
from app.domains.licensing.models import License
from app.domains.licensing.service import LicenseRequiredError
from app.domains.radius import admin_service
from app.domains.radius.models import SmsProvider


def _grant_license(db_session, **overrides):
    defaults = dict(
        fingerprint="fp",
        customer_key="cust",
        fortigate_max_devices=2,
        fortiweb_max_devices=1,
        sms2fa_max_users=2,
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


def test_create_two_factor_user_encrypts_secrets(db_session):
    _grant_license(db_session)

    user = admin_service.create_two_factor_user(
        db_session, username="alice", password="pw123", mobile_number="0912", actor="admin"
    )

    assert user.encrypted_password != "pw123"
    assert decrypt_secret(user.encrypted_password) == "pw123"
    assert decrypt_secret(user.encrypted_mobile_number) == "0912"


def test_create_two_factor_user_enforces_seat_quota(db_session):
    _grant_license(db_session, sms2fa_max_users=1)
    admin_service.create_two_factor_user(db_session, username="a", password="p", mobile_number="m", actor="admin")

    with pytest.raises(LicenseRequiredError):
        admin_service.create_two_factor_user(db_session, username="b", password="p", mobile_number="m", actor="admin")


def test_create_two_factor_user_rejects_duplicate_username(db_session):
    _grant_license(db_session)
    admin_service.create_two_factor_user(db_session, username="alice", password="p", mobile_number="m", actor="admin")

    with pytest.raises(admin_service.DuplicateUsernameError):
        admin_service.create_two_factor_user(db_session, username="alice", password="p2", mobile_number="m2", actor="admin")

    # the failed attempt's rollback shouldn't have wiped the first user out
    assert admin_service.count_two_factor_users(db_session) == 1


def test_set_enabled_false_does_not_clear_lock(db_session):
    _grant_license(db_session)
    user = admin_service.create_two_factor_user(
        db_session, username="alice", password="pw", mobile_number="m", actor="admin"
    )
    user.failed_attempts = 3
    user.locked_until = datetime.now(timezone.utc) + timedelta(minutes=5)
    db_session.commit()

    admin_service.set_two_factor_user_enabled(db_session, user, False, actor="admin")
    db_session.refresh(user)
    assert user.enabled is False
    assert user.locked_until is not None


def test_set_enabled_true_clears_lock(db_session):
    _grant_license(db_session)
    user = admin_service.create_two_factor_user(
        db_session, username="alice", password="pw", mobile_number="m", actor="admin"
    )
    user.failed_attempts = 3
    user.locked_until = datetime.now(timezone.utc) + timedelta(minutes=5)
    db_session.commit()

    admin_service.set_two_factor_user_enabled(db_session, user, True, actor="admin")
    db_session.refresh(user)
    assert user.failed_attempts == 0
    assert user.locked_until is None


def test_unlock_two_factor_user(db_session):
    _grant_license(db_session)
    user = admin_service.create_two_factor_user(
        db_session, username="alice", password="pw", mobile_number="m", actor="admin"
    )
    user.failed_attempts = 3
    user.locked_until = datetime.now(timezone.utc) + timedelta(minutes=5)
    db_session.commit()

    admin_service.unlock_two_factor_user(db_session, user, actor="admin")
    db_session.refresh(user)
    assert user.failed_attempts == 0
    assert user.locked_until is None


def test_delete_two_factor_user(db_session):
    _grant_license(db_session)
    user = admin_service.create_two_factor_user(
        db_session, username="alice", password="pw", mobile_number="m", actor="admin"
    )
    admin_service.delete_two_factor_user(db_session, user, actor="admin")
    assert admin_service.get_two_factor_user(db_session, user.id) is None


def test_create_and_delete_radius_client(db_session):
    client = admin_service.create_radius_client(
        db_session, name="fw1", nas_ip="10.0.0.1", shared_secret="s3cr3t", actor="admin"
    )
    assert decrypt_secret(client.encrypted_shared_secret) == "s3cr3t"
    assert len(admin_service.list_radius_clients(db_session)) == 1

    admin_service.delete_radius_client(db_session, client, actor="admin")
    assert admin_service.list_radius_clients(db_session) == []


def test_set_sms_config_kavenegar(db_session):
    config = admin_service.set_sms_config(
        db_session,
        provider=SmsProvider.KAVENEGAR,
        kavenegar_api_key="key123",
        kavenegar_sender="1000",
        generic_method="GET",
        generic_url_template=None,
        generic_auth_header_name=None,
        generic_auth_header_value=None,
        actor="admin",
    )
    assert decrypt_secret(config.encrypted_kavenegar_api_key) == "key123"
    assert config.kavenegar_sender == "1000"


def test_set_sms_config_updates_existing_row(db_session):
    admin_service.set_sms_config(
        db_session,
        provider=SmsProvider.KAVENEGAR,
        kavenegar_api_key="key123",
        kavenegar_sender="1000",
        generic_method="GET",
        generic_url_template=None,
        generic_auth_header_name=None,
        generic_auth_header_value=None,
        actor="admin",
    )
    admin_service.set_sms_config(
        db_session,
        provider=SmsProvider.GENERIC_HTTP,
        kavenegar_api_key=None,
        kavenegar_sender=None,
        generic_method="POST",
        generic_url_template="https://example.test/{mobile}/{code}",
        generic_auth_header_name="X-Api-Key",
        generic_auth_header_value="secret",
        actor="admin",
    )

    config = admin_service.get_sms_config(db_session)
    assert config.provider == SmsProvider.GENERIC_HTTP
    assert config.generic_url_template == "https://example.test/{mobile}/{code}"
    # kavenegar api key from the first update is preserved since it wasn't
    # explicitly overwritten (set_sms_config only updates it if provided)
    assert decrypt_secret(config.encrypted_kavenegar_api_key) == "key123"


def test_set_sms_config_sms_ir(db_session):
    config = admin_service.set_sms_config(
        db_session,
        provider=SmsProvider.SMS_IR,
        kavenegar_api_key=None,
        kavenegar_sender=None,
        smsir_api_key="smsir-key-123",
        smsir_line_number="30001234",
        generic_method="GET",
        generic_url_template=None,
        generic_auth_header_name=None,
        generic_auth_header_value=None,
        actor="admin",
    )
    assert decrypt_secret(config.encrypted_smsir_api_key) == "smsir-key-123"
    assert config.smsir_line_number == "30001234"


def test_prune_expired_challenges(db_session):
    from app.domains.radius.models import OtpChallenge

    old = OtpChallenge(
        username="alice",
        state_token="t1",
        code_hash="h1",
        expires_at=datetime.now(timezone.utc) - timedelta(days=2),
    )
    fresh = OtpChallenge(
        username="alice",
        state_token="t2",
        code_hash="h2",
        expires_at=datetime.now(timezone.utc) + timedelta(minutes=2),
    )
    db_session.add_all([old, fresh])
    db_session.commit()

    pruned = admin_service.prune_expired_challenges(db_session)

    assert pruned == 1
    remaining = db_session.query(OtpChallenge).all()
    assert len(remaining) == 1
    assert remaining[0].state_token == "t2"
