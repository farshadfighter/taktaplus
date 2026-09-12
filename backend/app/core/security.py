"""Secret-at-rest encryption and master-key management.

The master key lives in a standalone file outside the database and outside
version control (see docs/key-management.md). Losing it makes every stored
device token / FTP password / SMS gateway credential unrecoverable, so the
install docs must tell operators to back this file up separately from the
database backup described in domains/self_backup.
"""

from __future__ import annotations

import base64
import os
from functools import lru_cache

from cryptography.hazmat.primitives.ciphers.aead import AESGCM

from app.core.config import get_settings

NONCE_SIZE = 12


class MasterKeyNotFound(RuntimeError):
    pass


def generate_master_key() -> bytes:
    return AESGCM.generate_key(bit_length=256)


def write_master_key(path: str, key: bytes | None = None) -> bytes:
    key = key or generate_master_key()
    os.makedirs(os.path.dirname(path), exist_ok=True)
    with open(path, "wb") as fh:
        fh.write(base64.b64encode(key))
    os.chmod(path, 0o600)
    return key


@lru_cache
def _load_master_key() -> bytes:
    path = get_settings().master_key_path
    if not os.path.exists(path):
        raise MasterKeyNotFound(
            f"Master encryption key not found at {path}. Generate one with "
            "`python -m app.core.security --init` before starting the app."
        )
    with open(path, "rb") as fh:
        return base64.b64decode(fh.read())


def encrypt_secret(plaintext: str) -> str:
    """Encrypt a secret for storage. Returns base64(nonce || ciphertext)."""
    key = _load_master_key()
    nonce = os.urandom(NONCE_SIZE)
    ciphertext = AESGCM(key).encrypt(nonce, plaintext.encode("utf-8"), None)
    return base64.b64encode(nonce + ciphertext).decode("ascii")


def decrypt_secret(token: str) -> str:
    key = _load_master_key()
    raw = base64.b64decode(token)
    nonce, ciphertext = raw[:NONCE_SIZE], raw[NONCE_SIZE:]
    return AESGCM(key).decrypt(nonce, ciphertext, None).decode("utf-8")


if __name__ == "__main__":
    import sys

    if "--init" in sys.argv:
        settings = get_settings()
        if os.path.exists(settings.master_key_path):
            raise SystemExit(f"Key already exists at {settings.master_key_path}, refusing to overwrite.")
        write_master_key(settings.master_key_path)
        print(f"Master key written to {settings.master_key_path}. Back this file up now.")
