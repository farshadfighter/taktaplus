from celery import Celery
from celery.schedules import crontab

from app.core.config import get_settings

settings = get_settings()

celery_app = Celery("taktaplus", broker=settings.redis_url, backend=settings.redis_url)
celery_app.autodiscover_tasks(["app.workers"])

celery_app.conf.beat_schedule = {
    "license-heartbeat": {
        "task": "app.workers.tasks.run_license_heartbeat",
        "schedule": settings.license_heartbeat_interval_minutes * 60,
    },
    "self-db-backup": {
        "task": "app.workers.tasks.run_self_db_backup",
        "schedule": crontab(hour=3, minute=0),
    },
    "device-backups": {
        "task": "app.workers.tasks.run_scheduled_device_backups",
        "schedule": crontab(hour=2, minute=0),
    },
}
