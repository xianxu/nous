package oauth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
)

// codeVerifier mints a PKCE code_verifier (RFC 7636 §4.1): 32 random bytes,
// base64url without padding → 43 chars, well within the 43–128 range. Impure
// (crypto/rand) — the thin IO shell of the otherwise-pure PKCE pair. Used by
// the real adapter's Auth when the dialect requests PKCE (Microsoft public
// client); the fake never does PKCE (it's a real-adapter wire concern, not an
// S edge).
func codeVerifier() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// codeChallenge derives the S256 PKCE code_challenge from a verifier
// (RFC 7636 §4.2): base64url(sha256(verifier)), no padding. Pure — unit-tested
// against the RFC 7636 Appendix-B canonical vector.
func codeChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}
