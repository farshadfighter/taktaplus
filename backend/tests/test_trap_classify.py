from app.domains.monitoring.models import AlertSeverity
from app.domains.monitoring.trap_classify import classify_trap


def test_classifies_known_standard_trap():
    severity, message = classify_trap("1.3.6.1.6.3.1.1.5.3", {})

    assert severity == AlertSeverity.CRITICAL
    assert "linkDown" in message


def test_classifies_unknown_fortinet_trap_as_generic_warning():
    severity, message = classify_trap("1.3.6.1.4.1.12356.101.15.99.9", {"1.2.3": "95"})

    assert severity == AlertSeverity.WARNING
    assert "1.3.6.1.4.1.12356.101.15.99.9" in message
    assert "1.2.3=95" in message


def test_classifies_fully_unknown_trap_as_info():
    severity, message = classify_trap("1.2.3.4.5", {})

    assert severity == AlertSeverity.INFO
    assert "1.2.3.4.5" in message
