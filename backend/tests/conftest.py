import os
import tempfile

os.environ.setdefault("TAKTAPLUS_JWT_SECRET_KEY", "test-secret-key-not-for-production")
os.environ.setdefault("TAKTAPLUS_DATABASE_URL", "sqlite:///:memory:")
os.environ.setdefault(
    "TAKTAPLUS_MASTER_KEY_PATH", os.path.join(tempfile.gettempdir(), "taktaplus-test-master.key")
)
os.environ.setdefault("TAKTAPLUS_LICENSE_SERVER_URL", "http://localhost:8100")

import pytest  # noqa: E402
from sqlalchemy import create_engine  # noqa: E402
from sqlalchemy.orm import sessionmaker  # noqa: E402

from app.core.security import write_master_key  # noqa: E402
from app.db import model_registry  # noqa: E402,F401 - populates Base.metadata
from app.db.base import Base  # noqa: E402


@pytest.fixture(scope="session", autouse=True)
def _master_key():
    path = os.environ["TAKTAPLUS_MASTER_KEY_PATH"]
    if not os.path.exists(path):
        write_master_key(path)


@pytest.fixture
def db_session():
    engine = create_engine("sqlite:///:memory:")
    Base.metadata.create_all(engine)
    session = sessionmaker(bind=engine)()
    try:
        yield session
    finally:
        session.close()
        engine.dispose()
