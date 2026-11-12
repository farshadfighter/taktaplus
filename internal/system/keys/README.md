# Trusted release keys

Public keys that sign offline install/upgrade bundles, one
`TPPUB1 <kid> <base64>` line per key, in files named `<kid>.pub` (created with
`lictool keygen` in taktaplus-lic). They are embedded into the binary at build
time; a bundle is accepted from the panel or `stage-upgrade` only if one of
them signed it.

Only **public** keys belong here. Never add a `.key` file.
