"""Pull new signature/firmware packages from the configured FTP source.

Directory convention on the FTP source (either the vendor's own server, or
the customer's own FTP server pointed at with the same layout - see
docs/signature-distribution.md):

    <remote_path>/fortigate/<package_type>/<filename>
    <remote_path>/fortiweb/<package_type>/<filename>

e.g. <remote_path>/fortigate/ips/ips-7.2.12345.pkg

package_type is a free-form directory name (ips, av, ips-engine, firmware,
...) - whatever the source publishes, matching Package.package_type.
"""

from __future__ import annotations

import hashlib
import os
from dataclasses import dataclass
from ftplib import FTP, error_perm

from sqlalchemy import select
from sqlalchemy.orm import Session

from app.core.config import get_settings
from app.core.security import decrypt_secret
from app.domains.distribution.models import FtpSourceConfig, Package, PackageSource

VENDOR_DIRS = ("fortigate", "fortiweb")


class FtpSyncError(RuntimeError):
    pass


@dataclass
class EffectiveFtpConfig:
    host: str
    port: int
    username: str
    password: str
    remote_path: str


def get_ftp_config(db: Session) -> FtpSourceConfig | None:
    return db.scalar(select(FtpSourceConfig).limit(1))


def resolve_effective_config(db: Session) -> EffectiveFtpConfig:
    config = get_ftp_config(db)
    if config is not None and config.use_custom:
        if not config.host:
            raise FtpSyncError("منبع FTP سفارشی فعال است ولی آدرس سرور وارد نشده")
        return EffectiveFtpConfig(
            host=config.host,
            port=config.port,
            username=config.username,
            password=decrypt_secret(config.encrypted_password) if config.encrypted_password else "",
            remote_path=config.remote_path,
        )

    settings = get_settings()
    if not settings.default_ftp_host:
        raise FtpSyncError(
            "نه منبع FTP سفارشی تنظیم شده و نه FTP پیش‌فرض ارائه‌دهنده در این نصب پیکربندی شده است"
        )
    return EffectiveFtpConfig(
        host=settings.default_ftp_host,
        port=settings.default_ftp_port,
        username=settings.default_ftp_username or "",
        password=settings.default_ftp_password or "",
        remote_path="/",
    )


def _sha256_file(path: str) -> str:
    digest = hashlib.sha256()
    with open(path, "rb") as fh:
        for chunk in iter(lambda: fh.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def _already_imported(db: Session, vendor_type: str, package_type: str, filename: str) -> bool:
    return (
        db.scalar(
            select(Package.id).where(
                Package.vendor_type == vendor_type,
                Package.package_type == package_type,
                Package.filename == filename,
            )
        )
        is not None
    )


def sync_from_ftp(db: Session, *, packages_root: str) -> dict:
    config = resolve_effective_config(db)

    try:
        ftp = FTP()
        ftp.connect(config.host, config.port, timeout=20)
        ftp.login(config.username, config.password)
    except (OSError, error_perm) as exc:
        raise FtpSyncError(f"اتصال به سرور FTP منبع ناموفق بود: {exc}") from exc

    imported = 0
    skipped = 0
    try:
        base = config.remote_path.rstrip("/")
        for vendor in VENDOR_DIRS:
            vendor_path = f"{base}/{vendor}"
            try:
                package_type_names = ftp.nlst(vendor_path)
            except error_perm:
                continue  # this vendor's directory doesn't exist on this source - fine

            # NLST is only guaranteed to return bare names, not full paths -
            # some servers (e.g. pyftpdlib) do exactly that even when given
            # a directory argument, so paths are built explicitly rather
            # than trusting what comes back to already be a full path.
            for package_type_name in package_type_names:
                package_type = os.path.basename(package_type_name.rstrip("/"))
                package_type_path = f"{vendor_path}/{package_type}"
                try:
                    file_names = ftp.nlst(package_type_path)
                except error_perm:
                    continue

                for file_name in file_names:
                    filename = os.path.basename(file_name.rstrip("/"))
                    if not filename or _already_imported(db, vendor, package_type, filename):
                        skipped += 1
                        continue

                    local_dir = os.path.join(packages_root, vendor, package_type)
                    os.makedirs(local_dir, exist_ok=True)
                    local_path = os.path.join(local_dir, filename)
                    file_path = f"{package_type_path}/{filename}"

                    with open(local_path, "wb") as fh:
                        ftp.retrbinary(f"RETR {file_path}", fh.write)

                    db.add(
                        Package(
                            vendor_type=vendor,
                            package_type=package_type,
                            filename=filename,
                            checksum_sha256=_sha256_file(local_path),
                            size_bytes=os.path.getsize(local_path),
                            local_path=local_path,
                            source=PackageSource.FTP_SYNC,
                        )
                    )
                    db.commit()
                    imported += 1
    finally:
        try:
            ftp.quit()
        except Exception:  # noqa: BLE001 - best-effort cleanup, connection may already be dead
            pass

    return {"imported": imported, "skipped": skipped}
