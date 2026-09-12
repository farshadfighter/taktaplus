"""Cross-dialect column types.

Production always runs on Postgres, but the test suite runs against SQLite
in-memory (no Postgres server needed just to run unit tests). These types
use the native Postgres type in production and a portable fallback under
SQLite, so the same model definitions work in both without maintaining two
schemas by hand.
"""

from __future__ import annotations

import json
import uuid

from sqlalchemy import CHAR, JSON, TypeDecorator
from sqlalchemy.dialects.postgresql import ARRAY, UUID as PG_UUID


class GUID(TypeDecorator):
    impl = CHAR
    cache_ok = True

    def load_dialect_impl(self, dialect):
        if dialect.name == "postgresql":
            return dialect.type_descriptor(PG_UUID(as_uuid=True))
        return dialect.type_descriptor(CHAR(32))

    def process_bind_param(self, value, dialect):
        if value is None:
            return None
        if dialect.name == "postgresql":
            return str(value)
        return uuid.UUID(str(value)).hex

    def process_result_value(self, value, dialect):
        if value is None:
            return None
        return value if isinstance(value, uuid.UUID) else uuid.UUID(str(value))


class StringList(TypeDecorator):
    impl = JSON
    cache_ok = True

    def load_dialect_impl(self, dialect):
        if dialect.name == "postgresql":
            return dialect.type_descriptor(ARRAY(str))
        return dialect.type_descriptor(JSON())

    def process_bind_param(self, value, dialect):
        if value is None:
            return [] if dialect.name == "postgresql" else json.dumps([])
        return list(value) if dialect.name == "postgresql" else json.dumps(list(value))

    def process_result_value(self, value, dialect):
        if value is None:
            return []
        return value if dialect.name == "postgresql" else json.loads(value)
