"""Import every ORM model so Base.metadata is complete for Alembic
autogenerate and for create_all() in tests. Import this module, not the
individual model modules, wherever the full metadata is needed.
"""

from app.domains.audit.models import AuditLog  # noqa: F401
from app.domains.identity.models import Role, User  # noqa: F401
from app.domains.licensing.models import License, LicenseEvent  # noqa: F401
from app.domains.self_update.models import UpdateHistory  # noqa: F401
from app.db.base import Base  # noqa: F401
