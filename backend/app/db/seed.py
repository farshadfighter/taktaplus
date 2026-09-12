"""First-run seed: default roles + a one-time bootstrap super admin.

Run with `python -m app.db.seed`. Idempotent - safe to run again, it only
creates rows that don't exist yet.
"""

from __future__ import annotations

import secrets

from sqlalchemy import select

from app.core.auth import hash_password
from app.db.session import SessionLocal
from app.domains.identity.models import Role, User
from app.domains.identity.seed_roles import DEFAULT_ROLES


def seed() -> None:
    db = SessionLocal()
    try:
        role_rows: dict[str, Role] = {}
        for name, permissions in DEFAULT_ROLES.items():
            role = db.scalar(select(Role).where(Role.name == name))
            if role is None:
                role = Role(name=name, permissions=permissions)
                db.add(role)
                db.flush()
            role_rows[name] = role

        existing_admin = db.scalar(select(User).where(User.username == "admin"))
        if existing_admin is None:
            password = secrets.token_urlsafe(12)
            admin = User(
                username="admin",
                full_name="مدیر سیستم",
                password_hash=hash_password(password),
                role_id=role_rows["super_admin"].id,
            )
            db.add(admin)
            db.commit()
            print("Bootstrap super admin created.")
            print("  username: admin")
            print(f"  password: {password}")
            print("Change this password after first login.")
        else:
            db.commit()
            print("Roles ensured; admin user already exists.")
    finally:
        db.close()


if __name__ == "__main__":
    seed()
