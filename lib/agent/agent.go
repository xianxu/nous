// Package agent manages gpg-agent lifecycle for nous: keygrip discovery,
// passphrase prewarm/flush, and cache-state queries. Used by both
// brain-sync (gcrypt push/pull needs decrypted GPG access) and the
// charon proxy (vault unlocks GPG-encrypted credentials), so it lives
// as a leaf lib in the lib-first hierarchy — used by lib/brain* and
// lib/identity, doesn't import either of them.
//
// nous#14 M3d ships only the foundation: types + keygrip discovery.
// nous#14 M4 will add prewarm, flush, status using these primitives:
//
//	prewarm: read passphrase from macOS Keychain → present to gpg-agent
//	         via `gpg-connect-agent PRESET_PASSPHRASE <keygrip> ...`
//	         for each keygrip of the active key. After prewarm, gcrypt
//	         and vault ops within the cache TTL don't prompt.
//	flush:   `gpg-connect-agent reloadagent /bye` (wipes all cached
//	         passphrases). Used at session-end as security hygiene.
//	status:  parse `gpg-connect-agent KEYINFO --list /bye` to enumerate
//	         which keygrips are cached + their cache TTLs.
//
// Charon#21 (gpg-agent lifecycle integration) was tracked as a separate
// charon issue; it's now absorbed into nous#14's M3d (foundation) +
// M4 (the verbs).
package agent

// Keygrip is the SHA-1 hex of a public-key parameter blob — gpg-agent's
// native identifier for keys. Distinct from a key fingerprint: a single
// fingerprint typically maps to multiple keygrips (one per primary key
// + each subkey). The 40-char uppercase hex form is what gpg-agent's
// PRESET_PASSPHRASE / KEYINFO commands expect.
type Keygrip string

// Identity bundles a GPG key's fingerprint with all its keygrips. A
// brain operation (gcrypt push/pull) typically needs the encryption
// subkey's keygrip; a signing operation needs the signing key's. Rather
// than enumerate the use case at the call site, prewarm presents the
// passphrase to all keygrips of the identity at once.
type Identity struct {
	// Fingerprint is the 40-char uppercase hex of the primary key.
	// Same form as `gpg --fingerprint` output (without spaces).
	Fingerprint string

	// UID is the human-readable identity string ("Name <email>").
	// Empty if not present in the keyring.
	UID string

	// Keygrips lists the primary key's keygrip plus every subkey's
	// keygrip. Order is gpg's output order (primary first, then
	// subkeys in their listed order).
	Keygrips []Keygrip
}
