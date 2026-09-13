"""End-to-end test of the real RADIUS server process against a real pyrad
client over a real loopback UDP socket - not just the service-level unit
tests. This is what caught the User-Password raw-decryption bug during
development (see radius_server.py's module docstring and
docs/radius-2fa.md), so the same real-protocol round trip is kept here as
a regression test.
"""

import socket
import threading
import time
from datetime import datetime, timedelta, timezone

import pytest
from pyrad import packet
from pyrad.client import Client
from pyrad.dictionary import Dictionary
from pyrad.server import RemoteHost
from sqlalchemy import create_engine
from sqlalchemy.orm import sessionmaker
from sqlalchemy.pool import StaticPool

from app.core.security import encrypt_secret
from app.db.base import Base
from app.domains.licensing.models import License
from app.domains.radius.models import SmsGatewayConfig, SmsProvider, TwoFactorUser
from app.workers.radius_server import DICTIONARY_PATH, TaktaplusRadiusServer


def _grant_license(db_session):
    db_session.add(
        License(
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
    )
    db_session.commit()


def _free_udp_port() -> int:
    s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
    s.bind(("127.0.0.1", 0))
    port = s.getsockname()[1]
    s.close()
    return port


@pytest.fixture
def radius_env(monkeypatch):
    """Real server, driven by a real pyrad client, over a real loopback UDP
    socket, backed by a real (shared in-memory sqlite) database that both
    the test setup and the server's own background thread can see. The
    only thing stubbed out is the actual outbound SMS HTTP call, so the
    OTP code can be captured and fed back into the client instead of
    needing a real SMS gateway.
    """
    # A plain "sqlite:///:memory:" engine gives each connection its own
    # private database and forbids cross-thread use by default - neither
    # works here since the server runs its own SessionLocal() per request
    # in a background thread. StaticPool + check_same_thread=False shares
    # one real connection (and therefore one database) across threads.
    engine = create_engine("sqlite:///:memory:", connect_args={"check_same_thread": False}, poolclass=StaticPool)
    Base.metadata.create_all(engine)
    TestSessionLocal = sessionmaker(bind=engine)

    setup_session = TestSessionLocal()
    _grant_license(setup_session)

    shared_secret = "topsecret"
    setup_session.add(
        TwoFactorUser(
            username="alice",
            encrypted_password=encrypt_secret("Secret123"),
            encrypted_mobile_number=encrypt_secret("0912"),
        )
    )
    setup_session.add(
        SmsGatewayConfig(provider=SmsProvider.GENERIC_HTTP, generic_url_template="https://example.test/{mobile}/{code}")
    )
    setup_session.commit()
    setup_session.close()

    sent_codes = []

    def _fake_send(config, *, mobile_number, code):
        sent_codes.append(code)

    import app.domains.radius.service as service_module

    monkeypatch.setattr(service_module, "send_otp_sms", _fake_send)
    monkeypatch.setattr("app.workers.radius_server.SessionLocal", TestSessionLocal)

    port = _free_udp_port()
    radius_dict = Dictionary(DICTIONARY_PATH)
    server = TaktaplusRadiusServer(
        addresses=["127.0.0.1"],
        authport=port,
        hosts={"127.0.0.1": RemoteHost("127.0.0.1", shared_secret.encode("utf-8"), "test-nas")},
        dict=radius_dict,
        acct_enabled=False,
        coa_enabled=False,
    )

    thread = threading.Thread(target=server.Run, daemon=True)
    thread.start()
    time.sleep(0.2)

    client = Client(server="127.0.0.1", authport=port, secret=shared_secret.encode("utf-8"), dict=radius_dict)
    client.timeout = 3
    client.retries = 1

    yield client, sent_codes

    engine.dispose()


def test_full_challenge_response_round_trip(radius_env):
    client, sent_codes = radius_env

    req = client.CreateAuthPacket(code=packet.AccessRequest, User_Name="alice")
    req["User-Password"] = req.PwCrypt("Secret123")
    reply = client.SendPacket(req)

    assert reply.code == packet.AccessChallenge
    assert sent_codes, "expected an OTP to have been sent"
    state = reply["State"][0]

    otp_req = client.CreateAuthPacket(code=packet.AccessRequest, User_Name="alice")
    otp_req["User-Password"] = otp_req.PwCrypt(sent_codes[0])
    otp_req["State"] = state
    otp_reply = client.SendPacket(otp_req)

    assert otp_reply.code == packet.AccessAccept


def test_wrong_password_rejects(radius_env):
    client, _sent_codes = radius_env

    req = client.CreateAuthPacket(code=packet.AccessRequest, User_Name="alice")
    req["User-Password"] = req.PwCrypt("totally-wrong")
    reply = client.SendPacket(req)

    assert reply.code == packet.AccessReject


def test_concatenated_password_otp_round_trip(radius_env):
    """IPsec-style flow: no State forwarded, OTP appended directly to the
    primary password in a single follow-up request.
    """
    client, sent_codes = radius_env

    first = client.CreateAuthPacket(code=packet.AccessRequest, User_Name="alice")
    first["User-Password"] = first.PwCrypt("Secret123")
    first_reply = client.SendPacket(first)
    assert first_reply.code == packet.AccessChallenge
    assert sent_codes

    second = client.CreateAuthPacket(code=packet.AccessRequest, User_Name="alice")
    second["User-Password"] = second.PwCrypt(f"Secret123{sent_codes[0]}")
    second_reply = client.SendPacket(second)

    assert second_reply.code == packet.AccessAccept
