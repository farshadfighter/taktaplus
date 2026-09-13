"""Import guard for pysnmp 4.4.12 (and pyftpdlib) on Python 3.12+.

Two separate stdlib removals broke these libraries on newer Python, found
by actually running them on Python 3.13 rather than assuming the version
pin was still required:

1. `asyncore`/`asynchat` were removed outright. `pysnmp.carrier.asyncore.*`
   (used by both the SNMP poller's hlapi and the trap receiver) and
   `pyftpdlib`'s server core both import these by name. Fixed by installing
   the `pyasyncore`/`pyasynchat` backport packages (see requirements.txt),
   which provide top-level modules with those exact names - pyftpdlib
   already depends on them conditionally for Python >= 3.12, but pysnmp
   does not declare that dependency itself, so it's pinned here directly
   rather than relying on pyftpdlib to drag it in incidentally.

2. `pysnmp.smi.builder` does `import importlib` and then accesses
   `importlib.util.MAGIC_NUMBER` *without* ever doing `import importlib.util`
   itself. That submodule only becomes an attribute of the `importlib`
   package object if *something* in the process imported it first -
   usually true by accident (pytest, uvicorn, celery all end up doing it
   somewhere), but not guaranteed. When it isn't true, the AttributeError
   is caught internally and pysnmp falls back to `import imp` - a module
   Python removed in 3.12 with no available shim. Rather than depend on
   another library's imports happening to save us, we import these
   submodules ourselves so pysnmp's happy path always succeeds.

Import this module for its side effect before importing anything from
`pysnmp` - see snmp_client.py and workers/snmp_trap_receiver.py. Verified
end to end on Python 3.13 against a real snmpd (poll) and a real
`snmptrap` (trap receive), and pyftpdlib's real server against a real FTP
client - see docs/monitoring.md and docs/signature-distribution.md.
"""

import importlib.machinery  # noqa: F401
import importlib.util  # noqa: F401
