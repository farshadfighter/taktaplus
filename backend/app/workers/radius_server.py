"""Standalone RADIUS authentication server for SMS-based 2FA (phase 5).

Runs as its own process (see docker-compose's radius-server service),
separate from FastAPI/Celery, because it needs to bind a UDP socket and
drive pyrad's own poll-based event loop rather than fitting a
request/response or task-queue model - same reasoning as the SNMP trap
receiver and FTP relay.

Registered NAS clients (RadiusClient rows) are re-loaded from the database
periodically (every radius_client_refresh_seconds, via a background
thread - see _refresh_hosts_periodically) rather than only once at
startup, so adding, editing, or disabling a RadiusClient no longer needs a
process restart to take effect. pyrad's Server.Run() has no timer-callback
hook (unlike pysnmp's dispatcher, see snmp_trap_receiver.py), so this uses
a plain background thread instead - safe because the refresh only ever
*replaces* the `hosts` dict reference (never mutates the existing dict in
place), and a single reference reassignment is atomic under the GIL, so
HandleAuthPacket always sees either the fully-old or fully-new mapping,
never a partial one.

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
import threading

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


def _load_hosts() -> dict[str, RemoteHost]:
    db = SessionLocal()
    try:
        clients = [c for c in list_radius_clients(db) if c.enabled]
        return {
            client.nas_ip: RemoteHost(client.nas_ip, decrypt_secret(client.encrypted_shared_secret).encode("utf-8"), client.name)
            for client in clients
        }
    finally:
        db.close()


def _hosts_snapshot(hosts: dict[str, RemoteHost]) -> set[tuple[str, bytes, str]]:
    return {(ip, host.secret, host.name) for ip, host in hosts.items()}


def _refresh_hosts_periodically(server: "TaktaplusRadiusServer", interval_seconds: int, stop_event: threading.Event) -> None:
    while not stop_event.wait(interval_seconds):
        try:
            new_hosts = _load_hosts()
        except Exception:
            logger.exception("failed to refresh RADIUS clients from the database")
            continue

        if _hosts_snapshot(new_hosts) != _hosts_snapshot(server.hosts):
            server.hosts = new_hosts
            logger.info("RADIUS clients refreshed: now trusting %d NAS(es)", len(new_hosts))


def _build_server() -> TaktaplusRadiusServer:
    settings = get_settings()
    radius_dict = Dictionary(DICTIONARY_PATH)
    hosts = _load_hosts()
    logger.info("registered %d RADIUS client(s)", len(hosts))

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

    stop_event = threading.Event()
    refresh_thread = threading.Thread(
        target=_refresh_hosts_periodically,
        args=(server, settings.radius_client_refresh_seconds, stop_event),
        daemon=True,
    )
    refresh_thread.start()
    try:
        server.Run()
    except KeyboardInterrupt:
        pass
    finally:
        stop_event.set()


if __name__ == "__main__":
    run()
