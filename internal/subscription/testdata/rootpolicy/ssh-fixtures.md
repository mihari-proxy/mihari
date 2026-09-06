# Synthetic SSH fixtures

`ssh-ed25519.pem` contains the public test Ed25519 key derived from a 32-byte
zero seed, generated locally with already-installed Python cryptography 46.0.3,
PEM / OpenSSH / NoEncryption. Other `ssh-*.pem` fixtures are extracted from
[metacubex/ssh v0.1.0 testdata/keys.go](https://github.com/MetaCubeX/ssh/blob/v0.1.0/testdata/keys.go),
Copyright 2014 The Go Authors; the BSD license is included as `SSH-LICENSE`.
Public fixture passwords are in the tests and upstream source. These are test
data, never credentials. No SSH executable or connection was used. An attempted
local encrypted-fixture generation required unavailable bcrypt; no dependency
was installed, and the real upstream encrypted fixtures were used instead.

The encrypted OpenSSH envelopes are aes256-ctr and aes256-cbc / bcrypt, matching the actual pinned
github.com/metacubex/ssh v0.1.0 `keys.go` consumer. Policy checks inline framing;
it deliberately does not add a bcrypt/SSH implementation or claim to verify
encrypted key crypto. The trusted candidate core must perform that check.
