# SMS-based two-factor RADIUS auth (phase 5)

taktaplus acts as a full RADIUS PAP server that FortiGate points at for
admin login, SSL VPN, and IPsec VPN secondary authentication. This is not
an OTP add-on layered onto some other RADIUS server - taktaplus itself
validates the primary password (stored, encrypted, in `TwoFactorUser`)
*and* the SMS code.

## One algorithm, no per-NAS "mode" flag

FortiGate presents this to end users in two incompatible ways depending on
which service is authenticating:

- **Admin login and SSL VPN** forward RADIUS Access-Challenge to the end
  user, so a clean two-step exchange works: submit the password, get
  challenged, submit the code.
- **IPsec VPN does not forward challenges** - its only usable pattern is
  submitting the password and the OTP concatenated into a single
  `User-Password` field, which only works if the code was already sent by
  an earlier attempt.

Rather than configuring a per-NAS "challenge mode" flag (the same
FortiGate serves both a challenge-capable service and a non-capable one at
once, so a single flag can't be correct for both), `radius/service.py`'s
`authenticate()` tries, per request, in order:

1. **A `State` token is present** - continue a pending challenge: check the
   submitted value against that challenge's code, accept or reject.
2. **No state, but the password starts with the primary password and the
   remainder matches an active pending challenge's code** - this is the
   IPsec retry: accept.
3. **No state, password matches the primary password exactly** - issue a
   new challenge (sends the SMS, returns Access-Challenge with a `State`
   token). This is also what makes case 2 possible on IPsec's *next*
   attempt.
4. **Otherwise** - reject, and count it as a failed attempt.

This means the IPsec UX is: first attempt (password alone) is always
rejected by the NAS but silently triggers the SMS send server-side; the
user then retries with password+code appended and succeeds. This is a
real limitation of IPsec's RADIUS usage, not a bug - document it for
whoever configures the VPN client's retry/prompt behavior.

## Two real pyrad bugs found via protocol-level testing

Both were found running a real `pyrad` client against a real server (not
just unit tests) - the same testing rigor used for SSH/FTP/SNMP in earlier
phases.

**1. `User-Password` needs raw access, not `pkt["User-Password"]`.**
`Packet.__getitem__` decodes attributes per their dictionary type; for
`User-Password` that means treating the still-PAP-obfuscated bytes as a
plain `"string"` and UTF-8-decoding them - not the same as PAP-decrypting
them. Calling `PwDecrypt()` on that already-decoded value raises
`TypeError: unsupported operand type(s) for ^: 'int' and 'str'`. Fixed in
`radius_server.py` by bypassing the dictionary-driven decode:
`dict.__getitem__(pkt, pkt._EncodeKey("User-Password"))[0]`, then passing
*that* to `PwDecrypt()`.

**2. `AuthPacket.CreateReply()` doesn't copy `.source`/`.fd`.** Those are
set on the *request* packet by pyrad's own `_GrabPacket`, not propagated
to a reply built via `CreateReply()`. Without setting `reply.source =
pkt.source` by hand before calling `SendReplyPacket`, every single reply
(accept, challenge, or reject) crashes with
`AttributeError: 'AuthPacket' object has no attribute 'source'` instead of
ever reaching the NAS. This one only surfaces once a reply is actually
sent, so a test that never checks the client actually *received* a reply
would miss it entirely - `tests/test_radius_server_e2e.py` drives a real
client through all three outcomes for exactly this reason.

## Rate limiting and lockout

- `TAKTAPLUS_OTP_SMS_RATE_LIMIT_PER_HOUR` (default 5): caps how many SMS
  codes can be sent to one user per rolling hour, independent of
  success/failure - protects against SMS-cost abuse from repeated login
  attempts.
- `TAKTAPLUS_OTP_LOCKOUT_THRESHOLD` / `TAKTAPLUS_OTP_LOCKOUT_MINUTES`
  (defaults 5 / 15): after this many failed attempts (wrong password *or*
  wrong OTP), the account is locked out entirely - even a correct password
  is rejected - for the configured cooldown. An operator can also unlock a
  user manually via `POST /radius/users/{id}/unlock`.
- `TAKTAPLUS_OTP_MAX_ATTEMPTS_PER_CHALLENGE` (default 3): a single issued
  challenge is consumed (can no longer be answered, even correctly) after
  this many wrong guesses, forcing a fresh challenge/SMS.

## SMS gateway: no vendor default, unlike the FTP source

Every installation must configure its own SMS gateway - there is no
vendor-provided shared account (unlike the signature-distribution FTP
source, which does default to Fortinet's own server). Two provider modes:

- **Kavenegar**: confirmed against Kavenegar's own documented API shape
  (`POST https://api.kavenegar.com/v1/{api_key}/sms/send.json` with
  `receptor`/`message` form fields). Just needs an API key and optional
  sender line.
- **generic_http**: a customer-supplied URL template with `{mobile}` and
  `{code}` placeholders, method (GET/POST), and an optional single auth
  header. Covers any other provider without taktaplus needing to guess at
  an unverified API shape - other providers' exact endpoints (e.g.
  Melipayamak/Farapayamak) were not reliably confirmed during research, so
  they're left to this generic path rather than a half-verified dedicated
  integration.

## Operational notes

- `RadiusClient` rows (NAS/FortiGate + shared secret) are re-loaded from
  the database every `radius_client_refresh_seconds` (default 30s), not
  just once at startup - adding, editing, or disabling a NAS no longer
  needs a process restart. pyrad's `Server.Run()` has no timer-callback
  hook (unlike pysnmp's dispatcher - see docs/monitoring.md), so this
  uses a plain background thread instead; safe because the refresh only
  ever *replaces* the `hosts` dict reference rather than mutating it in
  place, and a single reference reassignment is atomic under the GIL.
  Verified end to end: a real pyrad client's request from an
  as-yet-unregistered NAS times out, a `RadiusClient` row is then
  inserted with the server still running, and the same client succeeds
  once the refresh interval has passed - see
  `tests/test_radius_server_e2e.py::test_new_radius_client_is_picked_up_without_restart`.
- Only authentication (UDP 1812) is served; accounting and CoA are
  disabled since none of FortiGate's admin/SSL-VPN/IPsec secondary-auth
  use of this server needs them.
- Expired `OtpChallenge` rows are pruned daily by a Celery beat task
  (`run_prune_expired_otp_challenges`) purely for table hygiene - an
  expired challenge is already unusable before it's deleted.
