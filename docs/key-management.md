# Master encryption key management

Every secret taktaplus stores at rest - FortiGate/FortiWeb API tokens, FTP
source credentials, SMS gateway credentials (added in later phases) - is
encrypted with AES-256-GCM (`app/core/security.py`) under a single master
key.

## Where the key lives

- File: `TAKTAPLUS_MASTER_KEY_PATH` (default `/etc/taktaplus/master.key`),
  base64-encoded, mode `0600`.
- Deliberately **outside the database** - a database restore onto new
  hardware without the matching key file makes every encrypted column
  permanently unreadable. Treat the key file and the database backup
  (`app/workers/tasks.py::run_self_db_backup`) as two halves of one backup:
  neither is useful without the other.
- Not committed to version control, not baked into the Docker image - it's
  written once per installation via the init step below and lives in the
  `master_key` named Docker volume in `docker-compose.yml`.

## First-time setup

```bash
docker compose run --rm backend python -m app.core.security --init
```

Refuses to run if a key already exists at the target path, so it's safe to
include in a setup script without risking an accidental overwrite.

## Operational rules

- Back up `/etc/taktaplus/master.key` (or the `master_key` volume) on
  whatever schedule you back up the database - losing one without the
  other is equivalent to losing both.
- Rotating the key requires decrypting every stored secret with the old key
  and re-encrypting with the new one; there's no rotation tooling yet
  because there are no encrypted secrets to rotate until phase 1 (device
  tokens) lands. Build the rotation script alongside that phase, not after.
- If the key is lost with no backup, the only recovery path is re-entering
  every device token / FTP password / SMS credential by hand after
  generating a fresh key.
