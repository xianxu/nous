package oauth

import "testing"

func TestCodeChallenge_RFC7636Vector(t *testing.T) {
	// RFC 7636 Appendix B canonical vector.
	verifier := "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	want := "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"
	if got := codeChallenge(verifier); got != want {
		t.Errorf("codeChallenge = %q, want %q", got, want)
	}
}

func TestCodeVerifier_Unique43Plus(t *testing.T) {
	a, b := codeVerifier(), codeVerifier()
	if a == b {
		t.Error("codeVerifier returned identical values")
	}
	if len(a) < 43 {
		t.Errorf("codeVerifier len = %d, want >= 43 (RFC 7636 §4.1)", len(a))
	}
}
