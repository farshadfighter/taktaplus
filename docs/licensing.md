# Licensing

taktaplus is licensed annually per installation, with two independent
entitlement dimensions:

- device count, tracked **separately per vendor type** (FortiGate vs FortiWeb)
- SMS 2FA seats (number of end users enrolled for the RADIUS/SMS module),
  sold as an add-on independent of the base device-count license

taktaplus stays **online-connected** to the License Server (a separate
product, operated by the vendor, not shipped to customers) rather than using
a fully offline signed-file model - customers in this business are
air-gapped on their *device* network, not necessarily on the network the
management server itself sits on.

## Contract this repo depends on

Implemented today by `dev/mock-license-server/app.py` for local development
only. The real License Server (separate repo, built later) MUST implement
the same shapes so `app/domains/licensing/client.py` doesn't need to change.

### `POST /api/v1/activate`

Request:
```json
{"fingerprint": "<32-hex-char installation fingerprint>", "customer_key": "<license key given to the customer>"}
```

Response `200`:
```json
{
  "token": "<opaque string, re-sent on every heartbeat>",
  "fortigate_max_devices": 10,
  "fortiweb_max_devices": 3,
  "sms2fa_max_users": 25,
  "issued_at": "2026-09-12T00:00:00+00:00",
  "expires_at": "2027-09-12T00:00:00+00:00"
}
```

Errors: `404` unknown customer_key, `403` suspended license.

### `POST /api/v1/heartbeat`

Request:
```json
{"fingerprint": "...", "current_token": "..."}
```

Response: same shape as `/activate` (server may rotate the token on every
call - the client always stores whatever it gets back). Called every
`TAKTAPLUS_LICENSE_HEARTBEAT_INTERVAL_MINUTES` minutes by
`app.workers.tasks.run_license_heartbeat`.

Errors: `404` fingerprint/token pair not recognized (re-activation required),
`403` suspended.

## Client-side behavior (already built, phase 0)

- `app/domains/licensing/fingerprint.py` generates a stable per-install
  fingerprint once, persisted outside the database at
  `/etc/taktaplus/installation.id`.
- `app/domains/licensing/service.py::compute_status` derives
  `active | grace | expired | suspended | unactivated` from timestamps on
  every read, rather than trusting a status column that could go stale
  between heartbeats:
  - a successful heartbeat within living memory -> `active`
  - heartbeats failing (server unreachable) but still inside
    `TAKTAPLUS_LICENSE_GRACE_PERIOD_DAYS` (default 7) of the last
    successful one -> still `active`
  - past the grace deadline -> `grace` (UI should show a hard warning;
    phase 5's enforcement hooks treat `grace` the same as `active` so a
    multi-day outage doesn't lock a customer out instantly, but see the
    device/seat quota functions below - they degrade the same way)
  - past `expires_at` -> `expired`
  - server returned 403 -> `suspended`, sticky until a fresh
    activate/heartbeat clears it
- `enforce_device_quota(db, vendor_type, current_count)` and
  `enforce_2fa_seat_quota(db, current_count)` raise `LicenseRequiredError`
  and must be called before inserting a new Device row / enrolling a new
  2FA user (wired up in phases 1 and 5 respectively).
- `should_warn_expiry` flags the last `TAKTAPLUS_LICENSE_EXPIRY_WARNING_DAYS`
  (default 30) days before `expires_at` so the frontend can show a renewal
  banner well before hard expiry.

## Known limitation

taktaplus runs with root/admin access on hardware the customer controls.
No client-side license check - offline file or online heartbeat - can be
made 100% tamper-proof against an operator willing to patch the binary.
The heartbeat model raises the bar (checks are spread across multiple
enforcement points rather than one central flag, and the License Server can
revoke in near-real-time for online customers) but this is risk reduction,
not a guarantee.
