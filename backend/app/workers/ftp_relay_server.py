"""Standalone FTP relay server for offline signature/firmware distribution.

Runs as its own process (the ftp-relay service in docker-compose), separate
from FastAPI/Celery, because it holds a long-lived listening socket just
like the SNMP trap receiver.

Serves TAKTAPLUS_PACKAGES_ROOT read-only to FortiGate devices, which pull
from it via `execute restore <type> ftp <filename> <advertised-host>[:port]
<username> <password>` (see docs/signature-distribution.md - there is no
REST API equivalent for this on FortiGate, so it's an SSH-issued CLI
command; FortiWeb doesn't use this relay at all, it gets files pushed
directly over HTTP - see drivers/fortiweb.py).

Read-only and credential-protected (verified with a real FTP client during
development: uploads are rejected with 550, wrong credentials with 530) -
a compromised or malicious device on the management network can only ever
read package files, never write to or browse outside this directory.
"""

from __future__ import annotations

import logging
import os

from pyftpdlib.authorizers import DummyAuthorizer
from pyftpdlib.handlers import FTPHandler
from pyftpdlib.servers import FTPServer

from app.core.config import get_settings

logger = logging.getLogger(__name__)


def run() -> None:
    logging.basicConfig(level=logging.INFO)
    settings = get_settings()

    if not settings.ftp_relay_password:
        raise SystemExit(
            "TAKTAPLUS_FTP_RELAY_PASSWORD تنظیم نشده است - یک رمز تصادفی برای این سرویس در .env قرار دهید."
        )

    os.makedirs(settings.packages_root, exist_ok=True)

    authorizer = DummyAuthorizer()
    authorizer.add_user(
        settings.ftp_relay_username,
        settings.ftp_relay_password,
        settings.packages_root,
        perm="elr",  # list + change-dir + retrieve only - never write
    )

    handler = FTPHandler
    handler.authorizer = authorizer
    handler.banner = "taktaplus signature relay"
    handler.passive_ports = range(settings.ftp_relay_passive_port_min, settings.ftp_relay_passive_port_max + 1)

    server = FTPServer((settings.ftp_relay_bind_host, settings.ftp_relay_port), handler)
    logger.info(
        "FTP relay serving %s on %s:%d (advertised to devices as %s)",
        settings.packages_root,
        settings.ftp_relay_bind_host,
        settings.ftp_relay_port,
        settings.ftp_relay_advertised_host or "<TAKTAPLUS_FTP_RELAY_ADVERTISED_HOST not set>",
    )
    server.serve_forever()


if __name__ == "__main__":
    run()
