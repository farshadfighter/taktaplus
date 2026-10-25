# Trusted license keys

Public keys of the license authority, one `TPPUB1 <kid> <base64>` line per key,
in files named `<kid>.pub` (created with `lictool keygen` in taktaplus-lic).
They are embedded into the binary at build time.

Only **public** keys belong here. Never add a `.key` file.
