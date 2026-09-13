# Offline signature and firmware distribution (phase 4)

## The two very different push mechanisms

FortiGate and FortiWeb needed genuinely different designs here, confirmed
by research rather than assumed:

- **FortiGate has no REST API for triggering an AV/IPS/etc. update from a
  custom FTP/TFTP server** - only the CLI (`execute restore ips ftp ...`)
  covers it. So taktaplus talks to FortiGate over **SSH** for this one
  operation (everything else in the codebase uses the REST API). Firmware
  is different again: FortiGate's monitor API *does* have
  `/api/v2/monitor/system/firmware/upgrade` with `{"source": "upload",
  "file_content": <base64>}` - this shape is confirmed by Fortinet's own
  Ansible collection for this exact endpoint, not a guess.
- **FortiWeb has no FTP-pull CLI at all** for signatures (confirmed absent
  in phase 1 research) - the only documented mechanism is a GUI file
  upload. So FortiWeb gets files pushed directly over HTTP through the
  same session-cookie mechanism already used for backup/restore
  (`drivers/fortiweb.py`), with no relay involved. The exact endpoint
  paths (`SIGNATURE_UPLOAD_ENDPOINT`, `FIRMWARE_UPLOAD_ENDPOINT`) are
  guessed by analogy to FortiGate's naming, same confidence level as
  FortiWeb's backup/restore endpoints from phase 2 - **unverified, check
  against real hardware**.

This is why `FortiGateDriver` gained `push_signature_via_ftp()` (SSH) and
`push_firmware()` (REST), while `FortiWebDriver` gained `push_signature()`
and `push_firmware()` (both plain HTTP uploads, no FTP anywhere).

## Why SSH via raw paramiko, not netmiko

netmiko's `fortinet` device type was tried first and rejected: its
`session_preparation()` does FortiOS version detection against `get system
status` output and raised `ValueError: Unexpected FortiOS Version
encountered` against a real (non-FortiOS) SSH server during development -
and a documented GitHub issue shows the same driver has had output-capture
quirks against real FortiGate hardware too. Raw `paramiko` with
`invoke_shell()` + a manual idle-based read loop (`_drain()` in
`fortigate.py`) is simpler, more predictable, and was verified end-to-end
against a real SSH server (connect, send, drain, close all worked
correctly) - only the FortiOS-specific prompt/output format on the other
end remains genuinely unverified.

## Two FTP relationships, don't confuse them

1. **taktaplus as FTP client** (`ftp_source.py`): pulls new packages from
   the *upstream* source - either the vendor's own FTP server (the
   default) or the customer's own FTP server (`use_custom` in
   `FtpSourceConfig`). Directory convention on that source:
   ```
   <remote_path>/fortigate/<package_type>/<filename>
   <remote_path>/fortiweb/<package_type>/<filename>
   ```
   package_type is free text (ips, av, ips-engine, firmware, ...),
   matching whatever the source actually publishes.

2. **taktaplus as FTP server** (`ftp_relay_server.py`, its own process):
   serves `TAKTAPLUS_PACKAGES_ROOT` read-only to FortiGate devices so they
   can run `execute restore <type> ftp <filename> <relay>:<port> <user>
   <pass>`. One shared credential (`TAKTAPLUS_FTP_RELAY_USERNAME/PASSWORD`)
   for all devices, not per-device or per-session - a documented
   simplification, not a security-unaware choice: the relay is read-only
   and chrooted to the packages directory regardless (verified with a real
   FTP client: uploads get 550, wrong password gets 530), so a leaked
   relay credential only exposes package files, never device config or
   other devices' credentials.

**A real bug this two-hop design caught during testing**: `ftplib`'s
`NLST` on a directory argument isn't guaranteed to return full paths - some
servers (pyftpdlib included) return bare basenames even so. The first
version of `sync_from_ftp` trusted NLST's return value as a path for the
next-level NLST/RETR call, which produced `550 No such file or directory`
silently swallowed by the "this directory doesn't exist, skip" handling -
so it looked like a source with zero packages instead of erroring. Fixed by
building every path explicitly (`f"{parent_path}/{name}"`) rather than
reusing whatever NLST returned. Covered by `tests/test_ftp_source.py`
against a real ephemeral FTP server, not a mock, so this can't regress
silently again.

## Why packages live on disk, not in the database

Unlike phase 2's config backups (small text files, stored encrypted in
Postgres), package files can be firmware images in the hundreds of MB.
`Package.local_path` points at a file under `TAKTAPLUS_PACKAGES_ROOT`
(a shared Docker volume across `backend`, `worker`, and `ftp-relay` - see
docker-compose.yml) instead. They aren't secret, so they aren't encrypted
at rest; `checksum_sha256` is recorded for integrity, not confidentiality.

## Post-push health check, not a rollback

After a push, `push_package_to_device` runs the same connectivity check
from phase 1 (`device_test_connection`) and records the result on the
`PushRecord` as `post_push_check_ok`. This is **not** an automatic
rollback - correctly verifying that a specific signature or firmware
version "took" would need per-package-type verification this codebase
can't safely automate without real hardware to validate against. What it
does catch cheaply: a push that broke the device's management plane
entirely (API stops responding). Treat a `post_push_check_ok: false`
(or `null`, meaning the check itself errored) as "go look at this device
by hand," not as "the push was reverted."

## Shared Python-3.11 constraint

Like the phase 3 SNMP trap receiver, `pyftpdlib` (the FTP relay) is built
on the same deprecated `asyncore`/`asynchat` modules removed in Python
3.12+ (confirmed via a real deprecation warning during development, see
`docs/monitoring.md`). Both are reasons `backend/Dockerfile` stays pinned
to `python:3.11-slim` - re-evaluate this dependency before bumping it.

## What still needs real-hardware validation

- FortiGate SSH CLI: the exact prompt/output format for `execute restore`
  success vs. failure (connection mechanics are verified, parsing isn't).
- FortiWeb's signature and firmware upload endpoint paths - pure guesses
  by analogy, same as FortiWeb's backup/restore endpoints from phase 2.
- Whether FortiGate's `execute restore` needs a specific admin access
  profile beyond what the SSH admin account already has (parallel to the
  `restore()` REST endpoint's `super_admin` caveat from phase 2).
