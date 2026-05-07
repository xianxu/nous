# Test Bootstrap Fixtures

Throwaway artifacts used by `make test-nous-bootstrap`.

## Files

- `test-key.asc` — passphrase-encrypted ASCII-armored GPG secret key (RSA 4096, sign + encrypt subkey, 1y expiry)
- `test-key.fingerprint` — primary key fingerprint, one line
- `test-key.passphrase` — plaintext passphrase

**These are intentionally NOT real credentials.** The key only ever encrypts test data inside disposable tart VMs. The passphrase is published in this directory. Do not use this key for anything else.

## Why checked in

Reproducibility. `make test-nous-bootstrap` should produce identical fixtures across runs and across machines. Generating a fresh key per-run was rejected because the per-run cost (~5s of CPU + non-determinism in fingerprints) outweighs the rotation benefit for a key whose entire scope is one ephemeral VM.

## Regenerating (e.g. on expiry)

```bash
PASSPHRASE="test-passphrase-do-not-use-for-real-data"
TMPGNUPGHOME=$(mktemp -d) && chmod 700 "$TMPGNUPGHOME"
echo "allow-loopback-pinentry" > "$TMPGNUPGHOME/gpg-agent.conf"
export GNUPGHOME=$TMPGNUPGHOME

gpg --batch --pinentry-mode loopback --passphrase "$PASSPHRASE" \
  --quick-generate-key "Nous Bootstrap Test <test@nous.local>" rsa4096 sign 1y
FP=$(gpg --list-secret-keys --with-colons | awk -F: '/^fpr:/ {print $10; exit}')
gpg --batch --pinentry-mode loopback --passphrase "$PASSPHRASE" \
  --quick-add-key "$FP" rsa4096 encrypt 1y
gpg --batch --pinentry-mode loopback --passphrase "$PASSPHRASE" \
  --armor --export-secret-keys "$FP" > test-key.asc
echo "$FP" > test-key.fingerprint
printf "%s" "$PASSPHRASE" > test-key.passphrase

gpgconf --homedir "$TMPGNUPGHOME" --kill all
rm -rf "$TMPGNUPGHOME"
unset GNUPGHOME
```
