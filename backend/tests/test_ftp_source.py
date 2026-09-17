import os
import threading

import pytest
from pyftpdlib.authorizers import DummyAuthorizer
from pyftpdlib.handlers import FTPHandler
from pyftpdlib.servers import FTPServer

from app.domains.distribution.ftp_source import FtpSyncError, resolve_effective_config, sync_from_ftp
from app.domains.distribution.models import FtpSourceConfig, Package


@pytest.fixture
def vendor_ftp_server(tmp_path):
    """A real FTP server (not a mock) serving a fake vendor package layout -
    this is what actually caught the NLST-returns-bare-names bug during
    development, so the regression test uses the same real server rather
    than a mocked ftplib client.
    """
    root = tmp_path / "vendor-ftp-root"
    (root / "fortigate" / "ips").mkdir(parents=True)
    (root / "fortigate" / "av").mkdir(parents=True)
    (root / "fortiweb" / "signature").mkdir(parents=True)
    (root / "fortigate" / "ips" / "ips-7.2.1.pkg").write_bytes(b"fake ips package v1")
    (root / "fortigate" / "av" / "av-7.2.1.pkg").write_bytes(b"fake av package v1")
    (root / "fortiweb" / "signature" / "sig-1.0.pkg").write_bytes(b"fake fortiweb sig v1")

    authorizer = DummyAuthorizer()
    authorizer.add_user("vendor", "vendorpass", str(root), perm="elr")
    handler = FTPHandler
    handler.authorizer = authorizer
    server = FTPServer(("127.0.0.1", 0), handler)
    port = server.socket.getsockname()[1]

    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    yield port
    server.close_all()


def test_sync_from_ftp_imports_all_packages(db_session, vendor_ftp_server, tmp_path, monkeypatch):
    monkeypatch.setenv("TAKTAPLUS_DEFAULT_FTP_HOST", "127.0.0.1")
    monkeypatch.setenv("TAKTAPLUS_DEFAULT_FTP_PORT", str(vendor_ftp_server))
    monkeypatch.setenv("TAKTAPLUS_DEFAULT_FTP_USERNAME", "vendor")
    monkeypatch.setenv("TAKTAPLUS_DEFAULT_FTP_PASSWORD", "vendorpass")
    from app.core.config import get_settings

    get_settings.cache_clear()

    packages_root = str(tmp_path / "packages")
    result = sync_from_ftp(db_session, packages_root=packages_root)

    assert result == {"imported": 3, "skipped": 0}
    packages = db_session.query(Package).all()
    assert {p.filename for p in packages} == {"ips-7.2.1.pkg", "av-7.2.1.pkg", "sig-1.0.pkg"}
    ips_package = next(p for p in packages if p.filename == "ips-7.2.1.pkg")
    assert ips_package.vendor_type.value == "fortigate"
    assert ips_package.package_type == "ips"
    assert os.path.exists(ips_package.local_path)
    with open(ips_package.local_path, "rb") as fh:
        assert fh.read() == b"fake ips package v1"

    get_settings.cache_clear()


def test_sync_from_ftp_is_idempotent(db_session, vendor_ftp_server, tmp_path, monkeypatch):
    monkeypatch.setenv("TAKTAPLUS_DEFAULT_FTP_HOST", "127.0.0.1")
    monkeypatch.setenv("TAKTAPLUS_DEFAULT_FTP_PORT", str(vendor_ftp_server))
    monkeypatch.setenv("TAKTAPLUS_DEFAULT_FTP_USERNAME", "vendor")
    monkeypatch.setenv("TAKTAPLUS_DEFAULT_FTP_PASSWORD", "vendorpass")
    from app.core.config import get_settings

    get_settings.cache_clear()
    packages_root = str(tmp_path / "packages")

    sync_from_ftp(db_session, packages_root=packages_root)
    second = sync_from_ftp(db_session, packages_root=packages_root)

    assert second == {"imported": 0, "skipped": 3}
    get_settings.cache_clear()


def test_resolve_effective_config_uses_custom_when_enabled(db_session):
    db_session.add(
        FtpSourceConfig(use_custom=True, host="custom.example", port=2121, username="me", remote_path="/pkgs")
    )
    db_session.commit()

    config = resolve_effective_config(db_session)

    assert config.host == "custom.example"
    assert config.port == 2121
    assert config.remote_path == "/pkgs"


def test_resolve_effective_config_raises_without_any_source(db_session, monkeypatch):
    monkeypatch.delenv("TAKTAPLUS_DEFAULT_FTP_HOST", raising=False)
    from app.core.config import get_settings

    get_settings.cache_clear()

    with pytest.raises(FtpSyncError):
        resolve_effective_config(db_session)

    get_settings.cache_clear()


@pytest.fixture
def malicious_vendor_ftp_server(tmp_path):
    """A real FTP server whose vendor-provided (untrusted) file/directory
    names include ones unsafe for a filesystem path component and for the
    SSH command they're later interpolated into - the same class of names a
    compromised or malicious FTP source could publish. Proves sync_from_ftp
    skips just those entries instead of writing outside packages_root or
    crashing the whole (unattended, nightly) sync.
    """
    root = tmp_path / "vendor-ftp-root"
    (root / "fortigate" / "ips").mkdir(parents=True)
    (root / "fortigate" / "ips" / "ips-7.2.1.pkg").write_bytes(b"fake ips package v1")
    (root / "fortigate" / "ips" / "bad name.pkg").write_bytes(b"unsafe filename")
    (root / "fortigate" / "..evil..").mkdir(parents=True)
    (root / "fortigate" / "..evil.." / "x.pkg").write_bytes(b"unsafe package_type dir")

    authorizer = DummyAuthorizer()
    authorizer.add_user("vendor", "vendorpass", str(root), perm="elr")
    handler = FTPHandler
    handler.authorizer = authorizer
    server = FTPServer(("127.0.0.1", 0), handler)
    port = server.socket.getsockname()[1]

    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    yield port
    server.close_all()


def test_sync_from_ftp_skips_unsafe_names_without_crashing(db_session, malicious_vendor_ftp_server, tmp_path, monkeypatch):
    monkeypatch.setenv("TAKTAPLUS_DEFAULT_FTP_HOST", "127.0.0.1")
    monkeypatch.setenv("TAKTAPLUS_DEFAULT_FTP_PORT", str(malicious_vendor_ftp_server))
    monkeypatch.setenv("TAKTAPLUS_DEFAULT_FTP_USERNAME", "vendor")
    monkeypatch.setenv("TAKTAPLUS_DEFAULT_FTP_PASSWORD", "vendorpass")
    from app.core.config import get_settings

    get_settings.cache_clear()

    packages_root = str(tmp_path / "packages")
    result = sync_from_ftp(db_session, packages_root=packages_root)

    packages = db_session.query(Package).all()
    assert {p.filename for p in packages} == {"ips-7.2.1.pkg"}
    assert result["imported"] == 1
    assert result["skipped"] >= 2
    assert not os.path.exists(os.path.join(packages_root, "fortigate", "..evil.."))

    get_settings.cache_clear()
