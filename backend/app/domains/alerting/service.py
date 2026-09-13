"""Install-wide notification channels (email, Telegram).

Deliberately settings-based rather than a DB-managed multi-channel table:
this product targets a single install per customer, so one email address
and one Telegram chat is enough for phase 3. A notification failure never
raises - the calling code (a backup job, a trap handler) already has a real
problem to report; a broken SMTP config on top of that shouldn't crash it.
"""

from __future__ import annotations

import logging
import smtplib
from email.message import EmailMessage

import httpx

from app.core.config import get_settings

logger = logging.getLogger(__name__)


def _send_email(subject: str, message: str) -> None:
    settings = get_settings()
    if not (settings.alert_email_smtp_host and settings.alert_email_from and settings.alert_email_to):
        return

    email = EmailMessage()
    email["Subject"] = f"[taktaplus] {subject}"
    email["From"] = settings.alert_email_from
    email["To"] = settings.alert_email_to
    email.set_content(message)

    try:
        with smtplib.SMTP(settings.alert_email_smtp_host, settings.alert_email_smtp_port, timeout=10) as smtp:
            smtp.starttls()
            if settings.alert_email_smtp_username:
                smtp.login(settings.alert_email_smtp_username, settings.alert_email_smtp_password or "")
            smtp.send_message(email)
    except (smtplib.SMTPException, OSError) as exc:
        logger.warning("email alert failed: %s", exc)


def _send_telegram(subject: str, message: str) -> None:
    settings = get_settings()
    if not (settings.alert_telegram_bot_token and settings.alert_telegram_chat_id):
        return

    url = f"https://api.telegram.org/bot{settings.alert_telegram_bot_token}/sendMessage"
    try:
        httpx.post(
            url,
            json={"chat_id": settings.alert_telegram_chat_id, "text": f"[taktaplus] {subject}\n{message}"},
            timeout=10.0,
        )
    except httpx.HTTPError as exc:
        logger.warning("telegram alert failed: %s", exc)


def notify(subject: str, message: str) -> None:
    _send_email(subject, message)
    _send_telegram(subject, message)
