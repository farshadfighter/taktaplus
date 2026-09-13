"""Standalone SNMP trap receiver process.

Runs as its own process (see docker-compose's snmp-trap-receiver service),
separate from the FastAPI app and Celery workers, because it needs to bind
a UDP socket and drive pysnmp's own event loop rather than fitting a
request/response or task-queue model.

Registered community strings are re-synced from the database periodically
(every snmp_trap_config_refresh_seconds, via pysnmp's own timer-callback
hook - see _CommunityRegistry.sync and run()) rather than only once at
startup, so enabling/disabling SNMP or changing a device's community no
longer needs a process restart to take effect. The refresh runs on the
same thread as trap processing (pysnmp's dispatcher calls it between
socket events), not a separate thread, so there's no concurrent-mutation
risk with pysnmp's own internal engine state.

The source IP of an incoming trap is matched against Device.host to decide
which device it belongs to - that lookup already hits the database fresh
on every trap, so it was never stale; only the community-string allowlist
needed this periodic refresh. A trap from an IP that isn't a known
device's host is logged and dropped rather than guessed at.
"""

from __future__ import annotations

import logging

from app.core import pysnmp_compat  # noqa: F401 - must import before pysnmp, see its docstring

from pysnmp.carrier.asyncore.dgram import udp
from pysnmp.entity import config, engine
from pysnmp.entity.rfc3413 import ntfrcv

from app.core.config import get_settings
from app.core.security import decrypt_secret
from app.db.session import SessionLocal
from app.domains.devices.service import find_device_by_host, list_devices
from app.domains.monitoring.service import record_trap_alert
from app.domains.monitoring.trap_classify import classify_trap

logger = logging.getLogger(__name__)

SNMP_TRAP_OID_KEY = "1.3.6.1.6.3.1.1.4.1.0"  # snmpTrapOID.0, standard across v2c traps


def _load_desired_communities() -> set[str]:
    db = SessionLocal()
    try:
        devices = [d for d in list_devices(db) if d.snmp_enabled and d.encrypted_snmp_community]
        return {decrypt_secret(d.encrypted_snmp_community) for d in devices}
    finally:
        db.close()


class _CommunityRegistry:
    """Tracks which community strings are currently registered on the
    engine (tag -> community) and reconciles that against the database.
    Tags are assigned from an ever-increasing counter, never reused, so
    repeated add/remove cycles can't collide two live tags - a plain
    `f"area{len(registered)}"` scheme can, once removals leave gaps.
    """

    def __init__(self) -> None:
        self.by_tag: dict[str, str] = {}
        self._next_index = 0

    def _new_tag(self) -> str:
        # SNMP-COMMUNITY-MIB caps this index string at 32 octets, so a
        # UUID-based name doesn't fit - a short counter-based tag is fine
        # since it only needs to be unique within this process.
        tag = f"area{self._next_index}"
        self._next_index += 1
        return tag

    def sync(self, snmp_engine: engine.SnmpEngine) -> None:
        desired = _load_desired_communities()
        current = set(self.by_tag.values())
        if desired == current:
            return

        for tag, community in list(self.by_tag.items()):
            if community not in desired:
                config.delV1System(snmp_engine, tag)
                del self.by_tag[tag]

        for community in desired - set(self.by_tag.values()):
            tag = self._new_tag()
            config.addV1System(snmp_engine, tag, community)
            self.by_tag[tag] = community

        logger.info("SNMP trap communities refreshed: now trusting %d community string(s)", len(self.by_tag))


def _build_engine() -> engine.SnmpEngine:
    settings = get_settings()
    snmp_engine = engine.SnmpEngine()
    config.addTransport(
        snmp_engine,
        udp.domainName,
        udp.UdpTransport().openServerMode((settings.snmp_trap_listen_host, settings.snmp_trap_listen_port)),
    )
    return snmp_engine


def _handle_trap(snmp_engine, state_reference, context_engine_id, context_name, var_binds, cb_ctx) -> None:
    exec_ctx = snmp_engine.observer.getExecutionContext("rfc3412.receiveMessage:request")
    transport_address = exec_ctx.get("transportAddress")
    source_ip = transport_address[0] if transport_address else None

    if source_ip is None:
        logger.warning("received trap with no resolvable source address, dropping")
        return

    varbind_map = {name.prettyPrint(): value.prettyPrint() for name, value in var_binds}
    trap_oid = varbind_map.pop(SNMP_TRAP_OID_KEY, "")

    db = SessionLocal()
    try:
        device = find_device_by_host(db, source_ip)
        if device is None:
            logger.warning("received trap from unregistered host %s, dropping", source_ip)
            return

        severity, message = classify_trap(trap_oid, varbind_map)
        record_trap_alert(db, device, trap_oid, severity, message)
        logger.info("recorded %s alert for %s: %s", severity.value, device.name, message)
    finally:
        db.close()


def run() -> None:
    logging.basicConfig(level=logging.INFO)
    settings = get_settings()
    snmp_engine = _build_engine()

    registry = _CommunityRegistry()
    registry.sync(snmp_engine)
    logger.info("registered SNMP trap communities for %d community string(s)", len(registry.by_tag))

    ntfrcv.NotificationReceiver(snmp_engine, _handle_trap)
    snmp_engine.transportDispatcher.jobStarted(1)
    snmp_engine.transportDispatcher.registerTimerCbFun(
        lambda time_now: registry.sync(snmp_engine), tickInterval=settings.snmp_trap_config_refresh_seconds
    )

    logger.info(
        "SNMP trap receiver listening on %s:%d", settings.snmp_trap_listen_host, settings.snmp_trap_listen_port
    )
    try:
        snmp_engine.transportDispatcher.runDispatcher()
    except KeyboardInterrupt:
        snmp_engine.transportDispatcher.closeDispatcher()


if __name__ == "__main__":
    run()
