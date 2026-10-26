# Trusted package keys

Public keys that sign the central repository's `manifest.json`, one
`TPPUB1 <kid> <base64>` line per key, in files named `<kid>.pub`
(`lictool keygen -kid pkg-...` in taktaplus-lic). They are separate from the
license keys and embedded into the binary at build time.

Only **public** keys belong here. Never add a `.key` file.
