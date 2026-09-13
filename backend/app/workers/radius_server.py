"""Standalone RADIUS authentication server for SMS-based 2FA (phase 5).

Runs as its own process (see docker-compose's radius-server service),
separate from FastAPI/Celery, because it needs to bind a UDP socket and
drive pyrad's own poll-based event loop rather than fitting a
request/response or task-queue model - same reasoning as the SNMP trap
receiver and FTP relay.

Registered NAS clients (RadiusClient rows) are loaded once at startup,
keyed by their nas_ip with their decrypted shared secret. Adding, editing
or disabling a RadiusClient after this process has started requires
restarting it - same documented limitation as the other standalone workers.

CRITICAL pyrad gotcha (found during protocol-level testing against a real
pyrad client, see docs/radius-2fa.md): Packet.__getitem__ auto-decodes
attributes according to their dictionary type - for User-Password that
means treating the still-PAP-obfuscated bytes as a plain "string" and
UTF-8-decoding them, which is not the same as PAP-decrypting them. Calling
PwDecrypt() on that already-decoded value raises
"TypeError: unsupported operand type(s) for ^: 'int' and 'str'". The raw
obfuscated bytes must be read by bypassing __getitem__'s dictionary-driven
decoding, via dict.__getitem__(pkt, pkt._EncodeKey("User-Password"))[0],
*then* passed to PwDecrypt().

Second gotcha found the same way: AuthPacket.CreateReply() copies the
secret/authenticator/id from the request but not .source/.fd (those are
set by pyrad's own _GrabPacket on the *request* packet, never on a reply
built via CreateReply) - SendReplyPacket needs both, so HandleAuthPacket
sets reply.source by hand before sending, or every reply raises
AttributeError instead of reaching the NAS.

Only authentication (port 1812) is served - accounting and CoA are
disabled since FortiGate's use of this server (admin/SSL VPN/IPsec
secondary auth) never needs them.
"""

from __future__ import annotations

import logging
import os

from pyrad import packet
from pyrad.dictionary import Dictionary
from pyrad.server import RemoteHost, Server

from app.core.config import get_settings
from app.core.security import decrypt_secret
from app.db.session import SessionLocal
from app.domains.radius import service
from app.domains.radius.admin_service import list_radius_clients

logger = logging.getLogger(__name__)

DICTIONARY_PATH = os.path.join(os.path.dirname(__file__), "..", "domains", "radius", "dictionary.txt")


def _raw_user_password(pkt: packet.AuthPacket) -> bytes:
    return dict.__getitem__(pkt, pkt._EncodeKey("User-Password"))[0]


class TaktaplusRadiusServer(Server):
    def HandleAuthPacket(self, pkt: packet.AuthPacket) -> None:
        username = pkt["User-Name"][0]

        try:
            raw_password = _raw_user_password(pkt)
        except KeyError:
            logger.warning("Access-Request for %s from %s has no User-Password, dropping", username, pkt.source)
            return
        password = pkt.PwDecrypt(raw_password)

        state_token = None
        if "State" in pkt:
            state_token = pkt["State"][0]
            if isinstance(state_token, bytes):
                state_token = state_token.decode("utf-8", errors="ignore")

        db = SessionLocal()
        try:
            result = service.authenticate(db, username=username, password=password, state_token=state_token)
        finally:
            db.close()

        if result.result == "accept":
            reply = pkt.CreateReply()
        elif result.result == "challenge":
            reply = pkt.CreateReply(**{"Reply-Message": result.reply_message, "State": result.state_token})
            reply.code = packet.AccessChallenge
        else:
            reply = pkt.CreateReply(**{"Reply-Message": result.reply_message})
            reply.code = packet.AccessReject

        # CreateReply() builds a fresh AuthPacket that copies the secret and
        # authenticator but *not* .source/.fd (those are set by pyrad's own
        # _GrabPacket on the request, not on the reply) - SendReplyPacket
        # needs both, so they must be propagated by hand. Found via a real
        # client/server round-trip test, not from reading the pyrad source.
        reply.source = pkt.source
        logger.info("RADIUS %s for user=%s nas=%s", result.result, username, pkt.source[0])
        self.SendReplyPacket(pkt.fd, reply)


def _build_server() -> TaktaplusRadiusServer:
    settings = get_settings()
    radius_dict = Dictionary(DICTIONARY_PATH)

    hosts: dict[str, RemoteHost] = {}
    db = SessionLocal()
    try:
        clients = [c for c in list_radius_clients(db) if c.enabled]
        for client in clients:
            secret = decrypt_secret(client.encrypted_shared_secret).encode("utf-8")
            hosts[client.nas_ip] = RemoteHost(client.nas_ip, secret, client.name)
        logger.info("registered %d RADIUS client(s)", len(clients))
    finally:
        db.close()

    return TaktaplusRadiusServer(
        addresses=[settings.radius_listen_host],
        authport=settings.radius_auth_port,
        hosts=hosts,
        dict=radius_dict,
        acct_enabled=False,
        coa_enabled=False,
    )


def run() -> None:
    logging.basicConfig(level=logging.INFO)
    settings = get_settings()
    server = _build_server()
    logger.info("RADIUS auth server listening on %s:%d", settings.radius_listen_host, settings.radius_auth_port)
    try:
        server.Run()
    except KeyboardInterrupt:
        pass


if __name__ == "__main__":
    run()
