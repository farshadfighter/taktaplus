import uuid
from datetime import datetime

from pydantic import BaseModel

from app.domains.backups.models import BackupSource, BackupStatus


class BackupOut(BaseModel):
    id: uuid.UUID
    device_id: uuid.UUID
    taken_at: datetime
    taken_by: str
    source: BackupSource
    status: BackupStatus
    error_message: str
    checksum_sha256: str
    size_bytes: int
    device_serial_snapshot: str
    device_firmware_snapshot: str

    model_config = {"from_attributes": True}


class RestoreRequest(BaseModel):
    force: bool = False
