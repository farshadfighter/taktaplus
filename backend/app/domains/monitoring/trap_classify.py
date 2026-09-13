"""Classify an incoming SNMP trap's OID into a severity + human message.

Only the standard SNMPv2 traps (RFC 3418, not Fortinet-specific) are
confidently identified by exact OID below - those are universal and were
exercised against a real trap exchange during development. Anything under
Fortinet's enterprise arc (1.3.6.1.4.1.12356.*) is real Fortinet-specific
telemetry, but this codebase doesn't have a confirmed mapping from specific
suffixes to specific meanings (e.g. "this exact OID means high CPU") - rather
than fabricate one, such traps are surfaced as a generic Fortinet event at
WARNING severity with the raw OID and varbinds included, so the operator
sees real data instead of an invented label. If you have a verified
FORTINET-FORTIGATE-MIB trap table, extend KNOWN_TRAPS below - the rest of
the pipeline doesn't change.
"""

from __future__ import annotations

from app.domains.monitoring.models import AlertSeverity

FORTINET_ENTERPRISE_PREFIX = "1.3.6.1.4.1.12356."

KNOWN_TRAPS: dict[str, tuple[AlertSeverity, str]] = {
    "1.3.6.1.6.3.1.1.5.1": (AlertSeverity.WARNING, "دستگاه ری‌استارت شد (coldStart)"),
    "1.3.6.1.6.3.1.1.5.2": (AlertSeverity.WARNING, "دستگاه ری‌استارت شد (warmStart)"),
    "1.3.6.1.6.3.1.1.5.3": (AlertSeverity.CRITICAL, "یک اینترفیس شبکه قطع شد (linkDown)"),
    "1.3.6.1.6.3.1.1.5.4": (AlertSeverity.INFO, "یک اینترفیس شبکه وصل شد (linkUp)"),
    "1.3.6.1.6.3.1.1.5.5": (AlertSeverity.WARNING, "تلاش ناموفق برای احراز هویت SNMP"),
}


def classify_trap(trap_oid: str, varbinds: dict[str, str]) -> tuple[AlertSeverity, str]:
    if trap_oid in KNOWN_TRAPS:
        return KNOWN_TRAPS[trap_oid]

    if trap_oid.startswith(FORTINET_ENTERPRISE_PREFIX):
        details = ", ".join(f"{oid}={value}" for oid, value in varbinds.items()) or "بدون مقدار اضافی"
        return AlertSeverity.WARNING, f"رویداد اختصاصی Fortinet دریافت شد (OID: {trap_oid}) - {details}"

    return AlertSeverity.INFO, f"trap ناشناخته دریافت شد (OID: {trap_oid})"
