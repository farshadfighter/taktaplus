"""Throwaway License Server stand-in for local development only.

The real License Server is a separate product (its own repo, built later -
see docs/licensing.md in the taktaplus repo for the API contract it must
implement). This file exists purely so `docker compose up` gives taktaplus
something to activate/heartbeat against while developing phases 1-5; it has
no persistence, no auth, and must never be deployed to a real customer.
"""

import uuid
from datetime import datetime, timedelta, timezone

from fastapi import FastAPI, HTTPException

app = FastAPI(title="taktaplus mock license server (dev only)")

# customer_key -> entitlements. Add rows here to test different license shapes.
CUSTOMERS = {
    "DEMO-0001": {
        "fortigate_max_devices": 10,
        "fortiweb_max_devices": 3,
        "sms2fa_max_users": 25,
        "suspended": False,
    },
}

# fingerprint -> {"customer_key": ..., "token": ...}
ACTIVATIONS: dict[str, dict] = {}


def _grant(customer_key: str, token: str) -> dict:
    customer = CUSTOMERS[customer_key]
    now = datetime.now(timezone.utc)
    return {
        "token": token,
        "fortigate_max_devices": customer["fortigate_max_devices"],
        "fortiweb_max_devices": customer["fortiweb_max_devices"],
        "sms2fa_max_users": customer["sms2fa_max_users"],
        "issued_at": now.isoformat(),
        "expires_at": (now + timedelta(days=365)).isoformat(),
    }


@app.post("/api/v1/activate")
def activate(body: dict):
    customer_key = body["customer_key"]
    fingerprint = body["fingerprint"]

    if customer_key not in CUSTOMERS:
        raise HTTPException(status_code=404, detail="کد لایسنس یافت نشد")
    if CUSTOMERS[customer_key]["suspended"]:
        raise HTTPException(status_code=403, detail="این لایسنس معلق شده است")

    token = uuid.uuid4().hex
    ACTIVATIONS[fingerprint] = {"customer_key": customer_key, "token": token}
    return _grant(customer_key, token)


@app.post("/api/v1/heartbeat")
def heartbeat(body: dict):
    fingerprint = body["fingerprint"]
    current_token = body["current_token"]

    activation = ACTIVATIONS.get(fingerprint)
    if activation is None or activation["token"] != current_token:
        raise HTTPException(status_code=404, detail="فعال‌سازی معتبر برای این دستگاه یافت نشد")

    customer_key = activation["customer_key"]
    if CUSTOMERS[customer_key]["suspended"]:
        raise HTTPException(status_code=403, detail="این لایسنس معلق شده است")

    new_token = uuid.uuid4().hex
    activation["token"] = new_token
    return _grant(customer_key, new_token)
