"""Self-update package verification.

An update package (*.tuz) is a zip with:
  manifest.json  {"version": "1.2.0", "min_supported_version": "1.0.0"}
  payload.tar.gz
  signature.bin   -- Ed25519 signature of payload.tar.gz's raw bytes

taktaplus only ever *verifies* here with the public key below (safe to
embed - it's not a secret). Applying an update (stopping services, swapping
code, running migrations, restarting) is deployment-topology-specific and
deliberately left as a documented follow-up for whichever phase builds the
installer/updater CLI - verification is the part that had to land now
because retrofitting trust-checking onto an already-shipped updater is the
expensive mistake to avoid.
"""

from __future__ import annotations

import io
import json
import zipfile
from dataclasses import dataclass

from cryptography.exceptions import InvalidSignature
from cryptography.hazmat.primitives.asymmetric.ed25519 import Ed25519PublicKey

# Placeholder - replace with the vendor's real Ed25519 public key before
# shipping the first signed update package. Keep the matching private key
# only on the machine that builds releases, never in this repo.
UPDATE_SIGNING_PUBLIC_KEY_HEX = "0000000000000000000000000000000000000000000000000000000000000000"

CURRENT_VERSION = "0.1.0-dev"


class InvalidUpdatePackage(RuntimeError):
    pass


@dataclass
class UpdateManifest:
    version: str
    min_supported_version: str


def get_current_version() -> str:
    return CURRENT_VERSION


def verify_package(package_bytes: bytes) -> UpdateManifest:
    try:
        with zipfile.ZipFile(io.BytesIO(package_bytes)) as zf:
            manifest = json.loads(zf.read("manifest.json"))
            payload = zf.read("payload.tar.gz")
            signature = zf.read("signature.bin")
    except (KeyError, zipfile.BadZipFile, json.JSONDecodeError) as exc:
        raise InvalidUpdatePackage(f"بسته آپدیت نامعتبر است: {exc}") from exc

    public_key = Ed25519PublicKey.from_public_bytes(bytes.fromhex(UPDATE_SIGNING_PUBLIC_KEY_HEX))
    try:
        public_key.verify(signature, payload)
    except InvalidSignature as exc:
        raise InvalidUpdatePackage("امضای دیجیتال بسته آپدیت معتبر نیست") from exc

    return UpdateManifest(version=manifest["version"], min_supported_version=manifest["min_supported_version"])
