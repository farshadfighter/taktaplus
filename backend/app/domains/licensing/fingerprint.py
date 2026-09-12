"""Stable per-installation fingerprint used to node-lock a license.

Generated once and persisted to disk (next to the master encryption key, not
in the database, so a DB restore onto different hardware doesn't silently
change it). The License Server binds every issued license to this value.
"""

from __future__ import annotations

import hashlib
import os
import uuid

FINGERPRINT_PATH = "/etc/taktaplus/installation.id"


def _machine_id() -> str:
    for path in ("/etc/machine-id", "/var/lib/dbus/machine-id"):
        if os.path.exists(path):
            with open(path) as fh:
                content = fh.read().strip()
                if content:
                    return content
    return ""


def get_or_create_fingerprint(path: str = FINGERPRINT_PATH) -> str:
    if os.path.exists(path):
        with open(path) as fh:
            return fh.read().strip()

    seed = _machine_id() or uuid.uuid4().hex
    fingerprint = hashlib.sha256(seed.encode()).hexdigest()[:32]

    os.makedirs(os.path.dirname(path), exist_ok=True)
    with open(path, "w") as fh:
        fh.write(fingerprint)
    os.chmod(path, 0o600)
    return fingerprint
