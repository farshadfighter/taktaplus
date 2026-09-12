import pytest

from app.core.auth import create_access_token, decode_token, hash_password, verify_password
from app.domains.identity.models import Role, User
from app.domains.identity.seed_roles import DEFAULT_ROLES
from app.domains.identity.service import authenticate_user


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
