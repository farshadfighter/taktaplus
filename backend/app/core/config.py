from functools import lru_cache

from pydantic_settings import BaseSettings, SettingsConfigDict


class Settings(BaseSettings):
    model_config = SettingsConfigDict(env_prefix="TAKTAPLUS_", env_file=".env", extra="ignore")

    database_url: str = "postgresql+psycopg://taktaplus:taktaplus@localhost:5432/taktaplus"
    redis_url: str = "redis://localhost:6379/0"

    jwt_secret_key: str
    jwt_algorithm: str = "HS256"
    jwt_access_token_expire_minutes: int = 30
    jwt_refresh_token_expire_minutes: int = 60 * 24 * 7

    # Path to the file holding the master AES-256-GCM key used to encrypt device
    # credentials, FTP/SMS gateway secrets, etc. Kept outside the database and
    # outside version control on purpose - see docs/key-management.md.
    master_key_path: str = "/etc/taktaplus/master.key"

    # License Server integration (see docs/licensing.md for the API contract).
    # Points at a mock server under dev/mock-license-server during local dev.
    license_server_url: str = "http://localhost:8100"
    license_heartbeat_interval_minutes: int = 60
    license_grace_period_days: int = 7
    license_expiry_warning_days: int = 30

    # Default FTP source for offline signature/firmware distribution. Overridable
    # per-installation by the customer in Settings > Signature Source.
    default_ftp_host: str | None = None
    default_ftp_port: int = 21
    default_ftp_username: str | None = None
    default_ftp_password: str | None = None

    db_backup_dir: str = "/var/backups/taktaplus"
    db_backup_retention_days: int = 30

    # Per-device config backups (phase 2), stored encrypted in the database
    # rather than on disk - see docs/backups.md.
    device_backup_retention_count: int = 30


@lru_cache
def get_settings() -> Settings:
    return Settings()
