# Device config backups

## Storage

Backup content is stored **encrypted in the database** (`backups.encrypted_content`,
via the same AES-256-GCM master key described in `docs/key-management.md`),
not on the filesystem. At the scale this product targets (under 50 devices
per install, typical FortiGate/FortiWeb config files in the tens to low
hundreds of KB), this is simpler to operate than a separate file store and
rides along with the existing Postgres backup (`run_self_db_backup`) instead
of needing its own backup path.

Raw bytes are base64-encoded before encryption (`encrypt_secret` operates on
text) so binary or non-UTF-8 content round-trips correctly - config files are
normally plain text, but nothing in the pipeline assumes that.

## Retention

`TAKTAPLUS_DEVICE_BACKUP_RETENTION_COUNT` (default 30) keeps the newest N
backups per device; older ones are pruned after every scheduled run
(`app.workers.tasks.run_scheduled_device_backups`, nightly at 02:00, before
the self DB backup at 03:00). Manual backups count toward the same limit.
Pruning does not run after a manual backup - only after the scheduled job -
so triggering many manual backups in a row will not silently delete older
ones until the next nightly run.

## Pre-restore validation

Every backup snapshots the device's `serial_number` and `firmware_version`
at the time it was taken. Before a restore, `validate_restore_compatibility`
compares that snapshot against the device's *current* values and raises
`RestoreBlockedError` (mapped to HTTP 409) if they differ, rather than
silently allowing it - a stale backup restored onto swapped-in replacement
hardware, or restored after a firmware upgrade, is exactly the kind of
mistake this exists to catch. It's a warning, not a hard rule: pass
`force: true` on the restore request to proceed anyway, e.g. when
deliberately migrating a saved config onto an RMA replacement unit.

## What's still unverified against real hardware

See the README section "چیزهایی که هنوز نیاز به تایید روی دستگاه واقعی دارن" -
FortiGate's `restore()` access-profile requirements and FortiWeb's
backup/restore endpoint paths are both best-effort guesses pending a test
against real devices.
