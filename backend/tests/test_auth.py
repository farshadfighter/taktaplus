import pytest
from fastapi import HTTPException

from app.core.auth import create_access_token, create_refresh_token, decode_token, hash_password, verify_password
from app.domains.identity.models import Role, User
from app.domains.identity.router import login, refresh
from app.domains.identity.schemas import LoginRequest, RefreshRequest
from app.domains.identity.seed_roles import DEFAULT_ROLES
from app.domains.identity.service import AccountLockedError, authenticate_user


def test_password_hash_round_trip():
    hashed = hash_password("correct-horse-battery-staple")

    assert verify_password("correct-horse-battery-staple", hashed)
    assert not verify_password("wrong-password", hashed)


def test_access_token_round_trip():
    token = create_access_token("user-123")

    payload = decode_token(token)

    assert payload["sub"] == "user-123"
    assert payload["type"] == "access"


def test_decode_token_rejects_garbage():
    with pytest.raises(ValueError):
        decode_token("not-a-real-token")


def test_authenticate_user_success_and_failure(db_session):
    role = Role(name="operator", permissions=DEFAULT_ROLES["operator"])
    db_session.add(role)
    db_session.flush()

    user = User(username="alice", password_hash=hash_password("s3cret"), role_id=role.id)
    db_session.add(user)
    db_session.commit()

    assert authenticate_user(db_session, "alice", "s3cret") is not None
    assert authenticate_user(db_session, "alice", "wrong") is None
    assert authenticate_user(db_session, "no-such-user", "s3cret") is None


def _make_user(db_session) -> User:
    role = Role(name="operator", permissions=DEFAULT_ROLES["operator"])
    db_session.add(role)
    db_session.flush()
    user = User(username="alice", password_hash=hash_password("s3cret"), role_id=role.id)
    db_session.add(user)
    db_session.commit()
    db_session.refresh(user)
    return user


def test_refresh_issues_new_access_and_refresh_tokens(db_session):
    user = _make_user(db_session)
    refresh_token = create_refresh_token(str(user.id))

    result = refresh(RefreshRequest(refresh_token=refresh_token), db_session)

    access_payload = decode_token(result.access_token)
    assert access_payload["sub"] == str(user.id)
    assert access_payload["type"] == "access"

    refresh_payload = decode_token(result.refresh_token)
    assert refresh_payload["sub"] == str(user.id)
    assert refresh_payload["type"] == "refresh"


def test_refresh_rejects_an_access_token(db_session):
    user = _make_user(db_session)
    access_token = create_access_token(str(user.id))

    with pytest.raises(HTTPException):
        refresh(RefreshRequest(refresh_token=access_token), db_session)


def test_refresh_rejects_garbage_token(db_session):
    with pytest.raises(HTTPException):
        refresh(RefreshRequest(refresh_token="not-a-real-token"), db_session)


def test_refresh_rejects_disabled_user(db_session):
    user = _make_user(db_session)
    refresh_token = create_refresh_token(str(user.id))
    user.is_active = False
    db_session.commit()

    with pytest.raises(HTTPException):
        refresh(RefreshRequest(refresh_token=refresh_token), db_session)


def test_login_locks_account_after_repeated_failures(db_session, monkeypatch):
    monkeypatch.setattr(
        "app.domains.identity.service.get_settings",
        lambda: type("S", (), {"login_lockout_threshold": 3, "login_lockout_minutes": 15})(),
    )
    _make_user(db_session)

    for _ in range(3):
        assert authenticate_user(db_session, "alice", "wrong") is None

    with pytest.raises(AccountLockedError):
        authenticate_user(db_session, "alice", "s3cret")


def test_login_success_resets_failed_attempts(db_session, monkeypatch):
    monkeypatch.setattr(
        "app.domains.identity.service.get_settings",
        lambda: type("S", (), {"login_lockout_threshold": 3, "login_lockout_minutes": 15})(),
    )
    _make_user(db_session)

    assert authenticate_user(db_session, "alice", "wrong") is None
    assert authenticate_user(db_session, "alice", "wrong") is None
    assert authenticate_user(db_session, "alice", "s3cret") is not None

    # A successful login clears the counter - the two prior failures don't
    # carry over toward a future lockout.
    assert authenticate_user(db_session, "alice", "wrong") is None
    assert authenticate_user(db_session, "alice", "wrong") is None
    assert authenticate_user(db_session, "alice", "s3cret") is not None


def test_login_lockout_expires_after_cooldown(db_session, monkeypatch):
    monkeypatch.setattr(
        "app.domains.identity.service.get_settings",
        lambda: type("S", (), {"login_lockout_threshold": 1, "login_lockout_minutes": 15})(),
    )
    user = _make_user(db_session)

    assert authenticate_user(db_session, "alice", "wrong") is None
    with pytest.raises(AccountLockedError):
        authenticate_user(db_session, "alice", "s3cret")

    from datetime import datetime, timedelta, timezone

    user.locked_until = datetime.now(timezone.utc) - timedelta(seconds=1)
    db_session.commit()

    assert authenticate_user(db_session, "alice", "s3cret") is not None


def test_login_endpoint_returns_401_with_locked_message(db_session, monkeypatch):
    monkeypatch.setattr(
        "app.domains.identity.service.get_settings",
        lambda: type("S", (), {"login_lockout_threshold": 1, "login_lockout_minutes": 15})(),
    )
    _make_user(db_session)

    with pytest.raises(HTTPException):
        login(LoginRequest(username="alice", password="wrong"), db_session)

    try:
        login(LoginRequest(username="alice", password="s3cret"), db_session)
        assert False, "expected HTTPException for a locked account"
    except HTTPException as exc:
        assert exc.status_code == 401
        assert "قفل" in exc.detail
