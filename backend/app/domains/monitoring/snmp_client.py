"""SNMPv2c polling for FortiGate/FortiWeb CPU/memory/session metrics.

SNMPv3 is deliberately out of scope for phase 3 (see docs/monitoring.md) -
implementing only USM auth without privacy, or vice versa, would be a
half-finished feature; v2c community strings are what most existing FortiOS
deployments actually have configured today.

Verified against a real net-snmp agent during development for the wire
protocol (GET, error handling, and the NoSuchObject-per-varbind behavior
this relies on for graceful OID fallback) - see the "UNVERIFIED" markers
below for which specific OIDs still need confirmation against real Fortinet
hardware.
"""

from __future__ import annotations

from dataclasses import dataclass
from typing import Callable

from app.core import pysnmp_compat  # noqa: F401 - must import before pysnmp, see its docstring

from pysnmp.hlapi import CommunityData, ContextData, ObjectIdentity, ObjectType, SnmpEngine, UdpTransportTarget, getCmd

# Universal, not Fortinet-specific - high confidence.
OID_SYS_UPTIME = "1.3.6.1.2.1.1.3.0"

# FORTINET-FORTIGATE-MIB. CPU/memory usage OIDs are well documented across
# multiple Fortinet KB articles and community posts (high confidence).
OID_CPU_USAGE = "1.3.6.1.4.1.12356.101.4.1.3.0"
OID_MEM_USAGE = "1.3.6.1.4.1.12356.101.4.1.4.0"

# UNVERIFIED: session count OID is cited in community sources as
# fgSysSesCount but wasn't confirmed against an official MIB dump for this
# codebase. If it comes back empty on real hardware, check the
# FORTINET-FORTIGATE-MIB fgSystem group for the correct index and fix here -
# this is the only place that needs to change.
OID_SESSION_COUNT = "1.3.6.1.4.1.12356.101.4.1.8.0"


class SnmpPollError(RuntimeError):
    pass


@dataclass
class SnmpMetrics:
    cpu_percent: float | None
    memory_percent: float | None
    session_count: int | None
    sys_uptime_ticks: int | None


def _to_float(value) -> float | None:
    try:
        return float(value)
    except (TypeError, ValueError):
        return None


def _to_int(value) -> int | None:
    try:
        return int(value)
    except (TypeError, ValueError):
        return None


def poll_device(
    host: str,
    port: int,
    community: str,
    *,
    timeout: int = 5,
    command: Callable = getCmd,
) -> SnmpMetrics:
    """Fetch uptime/CPU/memory/session-count in a single GET. `command` is a
    test seam - defaults to pysnmp's real getCmd, tests inject a stub
    generator instead of hitting a real socket.
    """
    iterator = command(
        SnmpEngine(),
        CommunityData(community, mpModel=1),  # mpModel=1 => SNMPv2c
        UdpTransportTarget((host, port), timeout=timeout, retries=1),
        ContextData(),
        ObjectType(ObjectIdentity(OID_SYS_UPTIME)),
        ObjectType(ObjectIdentity(OID_CPU_USAGE)),
        ObjectType(ObjectIdentity(OID_MEM_USAGE)),
        ObjectType(ObjectIdentity(OID_SESSION_COUNT)),
    )
    error_indication, error_status, error_index, var_binds = next(iterator)

    if error_indication:
        raise SnmpPollError(str(error_indication))
    if error_status:
        raise SnmpPollError(f"{error_status.prettyPrint()} at varbind {error_index}")

    values = [value for _, value in var_binds]
    if len(values) != 4:
        raise SnmpPollError("پاسخ SNMP تعداد متغیرهای نامنتظره‌ای داشت")

    return SnmpMetrics(
        sys_uptime_ticks=_to_int(values[0]),
        cpu_percent=_to_float(values[1]),
        memory_percent=_to_float(values[2]),
        session_count=_to_int(values[3]),
    )
