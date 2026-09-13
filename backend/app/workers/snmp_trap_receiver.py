"""Standalone SNMP trap receiver process.

Runs as its own process (see docker-compose's snmp-trap-receiver service),
separate from the FastAPI app and Celery workers, because it needs to bind
a UDP socket and drive pysnmp's own event loop rather than fitting a
request/response or task-queue model.

Registered community strings are loaded once at startup from every
snmp_enabled device. Changing a device's SNMP community after this process
has started requires restarting it - hot-reload (SIGHUP or a periodic
refresh) is a reasonable fast-follow, not built for phase 3.

The source IP of an incoming trap is matched against Device.host to decide
which device it belongs to; a trap from an IP that isn't a known device's
host is logged and dropped rather than guessed at.
"""

from __future__ import annotations

import logging

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


def _build_engine() -> engine.SnmpEngine:
    settings = get_settings()
    snmp_engine = engine.SnmpEngine()
    config.addTransport(
        snmp_engine,
        udp.domainName,
        udp.UdpTransport().openServerMode((settings.snmp_trap_listen_host, settings.snmp_trap_listen_port)),
    )

    db = SessionLocal()
    try:
        devices = [d for d in list_devices(db) if d.snmp_enabled and d.encrypted_snmp_community]
        for index, device in enumerate(devices):
            community = decrypt_secret(device.encrypted_snmp_community)
            # SNMP-COMMUNITY-MIB caps this index string at 32 octets, so a
            # UUID-based name doesn't fit - a short positional tag is fine
            # since it only needs to be unique within this process.
            config.addV1System(snmp_engine, f"area{index}", community)
        logger.info("registered SNMP trap communities for %d device(s)", len(devices))
    finally:
        db.close()

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
    ntfrcv.NotificationReceiver(snmp_engine, _handle_trap)
    snmp_engine.transportDispatcher.jobStarted(1)

    logger.info(
        "SNMP trap receiver listening on %s:%d", settings.snmp_trap_listen_host, settings.snmp_trap_listen_port
    )
    try:
        snmp_engine.transportDispatcher.runDispatcher()
    except KeyboardInterrupt:
        snmp_engine.transportDispatcher.closeDispatcher()


if __name__ == "__main__":
    run()
