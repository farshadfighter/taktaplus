import asyncio
import io

import pytest
from fastapi import HTTPException, UploadFile

from app.domains.devices.models import VendorType
from app.domains.distribution.router import upload_package


def _upload(db_session, monkeypatch, *, size_bytes: int, max_mb: int, tmp_path):
    monkeypatch.setattr(
        "app.domains.distribution.router.get_settings",
        lambda: type("S", (), {"packages_root": str(tmp_path), "max_package_upload_mb": max_mb})(),
    )
    file = UploadFile(io.BytesIO(b"x" * size_bytes), filename="pkg.bin")
    user = type("U", (), {"username": "tester"})()

    return asyncio.run(
        upload_package(
            vendor_type=VendorType.FORTIGATE,
            package_type="ips",
            file=file,
            db=db_session,
            user=user,
        )
    )


def test_upload_package_rejects_file_over_the_configured_cap(db_session, monkeypatch, tmp_path):
    with pytest.raises(HTTPException) as exc_info:
        _upload(db_session, monkeypatch, size_bytes=3 * 1024 * 1024, max_mb=2, tmp_path=tmp_path)

    assert exc_info.value.status_code == 413


def test_upload_package_accepts_file_within_the_configured_cap(db_session, monkeypatch, tmp_path):
    result = _upload(db_session, monkeypatch, size_bytes=1024, max_mb=2, tmp_path=tmp_path)

    assert result.size_bytes == 1024
