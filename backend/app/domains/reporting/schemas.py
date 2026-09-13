import uuid
from datetime import datetime

from pydantic import BaseModel

from app.domains.backups.models import BackupStatus
from app.domains.devices.models import DeviceStatus, VendorType


class FleetStatusRow(BaseModel):
    device_id: uuid.UUID
    device_name: str
    vendor_type: VendorType
    status: DeviceStatus
    firmware_version: str
    last_backup_at: datetime | None
    last_backup_status: BackupStatus | None
    backup_overdue: bool
    snmp_enabled: bool
    latest_cpu_percent: float | None
    latest_memory_percent: float | None
    open_alert_count: int
