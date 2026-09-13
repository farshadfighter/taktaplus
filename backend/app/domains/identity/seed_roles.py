"""Default role/permission matrix seeded on first boot.

Kept intentionally small for phase 0 (device management, backups, licensing,
and settings aren't built yet); extend PERMISSIONS as later phases land.
"""

DEFAULT_ROLES: dict[str, list[str]] = {
    "super_admin": ["*"],
    "operator": [
        "devices:read",
        "devices:write",
        "backups:read",
        "backups:create",
        "backups:restore",
        "reports:read",
    ],
    "viewer": [
        "devices:read",
        "backups:read",
        "reports:read",
    ],
}


def has_permission(role_permissions: list[str], required: str) -> bool:
    return "*" in role_permissions or required in role_permissions
