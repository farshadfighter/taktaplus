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

    # SNMP monitoring (phase 3) - see docs/monitoring.md.
    snmp_poll_interval_minutes: int = 5
    snmp_poll_timeout_seconds: int = 5
    snmp_trap_listen_host: str = "0.0.0.0"
    snmp_trap_listen_port: int = 162
    # How often the trap receiver re-checks which SNMP community strings
    # are valid, without needing a process restart - see
    # snmp_trap_receiver.py's _refresh_communities.
    snmp_trap_config_refresh_seconds: int = 30
    snmp_cpu_alert_threshold: int = 90
    snmp_memory_alert_threshold: int = 90
    backup_overdue_hours: int = 48

    # Alerting (phase 3) - both optional; a channel with no config is simply
    # skipped rather than erroring. Values live in env/.env, not the DB,
    # since they're install-wide rather than per-device.
    alert_email_smtp_host: str | None = None
    alert_email_smtp_port: int = 587
    alert_email_smtp_username: str | None = None
    alert_email_smtp_password: str | None = None
    alert_email_from: str | None = None
    alert_email_to: str | None = None
    alert_telegram_bot_token: str | None = None
    alert_telegram_chat_id: str | None = None

    # Offline signature/firmware distribution (phase 4) - see
    # docs/signature-distribution.md.
    packages_root: str = "/var/lib/taktaplus/packages"
    # Firmware images legitimately run into the hundreds of MB, so this is
    # generous - it exists only to bound a single authenticated upload
    # (packages:manage), not to constrain normal use.
    max_package_upload_mb: int = 4096

    # The internal FTP relay FortiGate devices pull signature packages from
    # (taktaplus is both an FTP *client* to the upstream source above, and
    # an FTP *server* to its own managed FortiGates). advertised_host is
    # what devices are told to connect to - may differ from bind_host if
    # NAT/multiple interfaces are involved.
    ftp_relay_bind_host: str = "0.0.0.0"
    ftp_relay_port: int = 21
    ftp_relay_advertised_host: str | None = None
    ftp_relay_username: str = "taktaplus-relay"
    ftp_relay_password: str | None = None
    # FTP PASV data connections otherwise use OS-ephemeral ports, which is
    # fine on a flat management LAN but breaks if a firewall sits between
    # taktaplus and the FortiGates - pin a narrow range and open exactly
    # those ports (plus ftp_relay_port) on that firewall.
    ftp_relay_passive_port_min: int = 60000
    ftp_relay_passive_port_max: int = 60020

    # SMS-based two-factor RADIUS server (phase 5) - see docs/radius-2fa.md.
    radius_listen_host: str = "0.0.0.0"
    radius_auth_port: int = 1812
    # How often the RADIUS server re-loads RadiusClient (NAS) rows from the
    # database, without needing a process restart - see radius_server.py's
    # _refresh_hosts.
    radius_client_refresh_seconds: int = 30
    otp_length: int = 5
    otp_expiry_seconds: int = 120
    otp_max_attempts_per_challenge: int = 3
    otp_sms_rate_limit_per_hour: int = 5
    otp_lockout_threshold: int = 5
    otp_lockout_minutes: int = 15

    # Brute-force protection for the admin login endpoint (/auth/login) -
    # separate from the RADIUS/OTP lockout above, which only covers
    # TwoFactorUser (SMS 2FA end users), not the operator accounts that log
    # into this app itself.
    login_lockout_threshold: int = 5
    login_lockout_minutes: int = 15


@lru_cache
def get_settings() -> Settings:
    return Settings()
