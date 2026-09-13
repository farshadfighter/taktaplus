# SNMP monitoring (phase 3)

## Why SNMPv2c only, not v3

FortiOS/FortiWeb both support SNMPv3, but implementing only USM
authentication without privacy (or vice versa) would be a half-finished
feature - real v3 needs auth+priv protocol negotiation, and most existing
FortiGate/FortiWeb deployments in the field still run v2c community strings
day to day. v3 is a documented fast-follow, not built here.

## Library choice: pysnmp 4.4.12 (classic sync hlapi) + pyasn1 0.4.8

This exact pin was chosen and verified deliberately - pysnmp's actively
maintained fork (`pysnmp-lextudio`) moved to an asyncio API in v6, which
doesn't fit cleanly into synchronous Celery tasks. The classic 4.4.12
branch's `pysnmp.hlapi.getCmd` is synchronous and simple to call from a
Celery task; `pyasn1==0.4.8` is the version it actually works with (newer
pyasn1 releases break it).

**Known constraint**: the classic branch's trap receiver
(`pysnmp.carrier.asyncore.dgram.udp`) depends on Python's `asyncore` module,
which is deprecated and removed starting Python 3.12. `backend/Dockerfile`
pins `python:3.11-slim` for exactly this reason - do not bump the Python
base image without re-evaluating this dependency first (either pinning
asyncore's removal away, or migrating the trap receiver to the lextudio v6
async API).

## What was verified during development vs. what's still a guess

Both the poller (`snmp_client.py::poll_device`) and the trap receiver
(`app/workers/snmp_trap_receiver.py`) were exercised end-to-end against a
real local `snmpd` (SNMP GET) and real `snmptrap` sends (trap receiving,
including extracting the sender's source IP via
`snmpEngine.observer.getExecutionContext('rfc3412.receiveMessage:request')`)
during development - the wire protocol handling, error paths, and the
graceful-degradation behavior for a missing OID (SNMPv2c returns
`NoSuchObject` per-varbind rather than failing the whole request) are all
real, tested behavior, not assumptions.

What's still unverified against real Fortinet hardware:
- **Session count OID** (`OID_SESSION_COUNT` in `snmp_client.py`): cited in
  community sources as `fgSysSesCount` but not confirmed against an
  official FORTINET-FORTIGATE-MIB dump. CPU/memory OIDs are higher
  confidence (documented across multiple Fortinet KB articles). If session
  count comes back empty on real hardware, it fails silently (returns
  `None`) rather than erroring - check the MIB for the right OID and fix
  in one place.
- **Fortinet-specific trap meanings**: `trap_classify.py` only claims exact
  knowledge of the standard SNMPv2 traps (coldStart/warmStart/linkUp/
  linkDown/authenticationFailure - RFC 3418, verified against a real trap
  exchange). Anything under Fortinet's enterprise OID arc
  (`1.3.6.1.4.1.12356.*`) is surfaced as a generic "Fortinet event" at
  WARNING severity with the raw OID and varbinds shown, rather than
  guessing what a specific trap OID suffix means.

## Trap receiver operational notes

- Runs as its own process (`app.workers.snmp_trap_receiver`, wired up as
  the `snmp-trap-receiver` service in docker-compose), not inside FastAPI
  or Celery, because it owns a UDP socket and pysnmp's own event loop.
- Registers one SNMP-COMMUNITY-MIB entry per SNMP-enabled device at
  startup, decrypting each device's community string. **Changing a
  device's SNMP settings after the process has started requires
  restarting it** - there's no hot-reload (SIGHUP or periodic refresh)
  yet. The per-device index string used internally is a short positional
  tag (`area0`, `area1`, ...), not the device's UUID - SNMP-COMMUNITY-MIB
  caps that index at 32 octets, which a UUID doesn't fit in.
- A trap's source device is resolved by matching its source IP against
  `Device.host`. A trap from an IP that isn't a known device is logged and
  dropped, not guessed at.

## Alerting

`app/domains/alerting/service.py` is intentionally settings-based (one
email address, one Telegram chat per install) rather than a DB-managed
multi-channel table - this product targets a single install per customer.
A channel with no configuration is silently skipped; a send failure is
logged and swallowed, never raised, since the code calling `notify()`
already has a real problem to report (a failed backup, an unreachable
device) and a broken SMTP config shouldn't compound that into a crash.
SMS alerting isn't included here - phase 5 builds a customer-configurable
SMS gateway abstraction for 2FA, and reusing that for generic alerts is a
natural extension once it exists rather than a second implementation now.

## Compliance / fleet status report

`GET /api/v1/reports/fleet-status` aggregates what's already in the
database per device: connectivity status, latest backup and whether it's
overdue (`TAKTAPLUS_BACKUP_OVERDUE_HOURS`, default 48h), latest SNMP
metrics, and open (unacknowledged) alert count. It doesn't track signature
or firmware versions yet - that's meaningful once phase 4 (offline
signature/firmware distribution) exists to define what "up to date" means.
