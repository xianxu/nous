package oauth

import "testing"

func TestXORRoundTrip(t *testing.T) {
	key := "test-key-123"
	original := "hello-world-secret"

	encoded := XOREncode(original, key)
	decoded, err := XORDecode(encoded, key)
	if err != nil {
		t.Fatal(err)
	}
	if decoded != original {
		t.Errorf("round-trip failed: got %q, want %q", decoded, original)
	}
}

func TestXORDecode_InvalidHex(t *testing.T) {
	_, err := XORDecode("not-hex", "key")
	if err == nil {
		t.Error("expected error for invalid hex")
	}
}

func TestXORDecode_EmptyString(t *testing.T) {
	decoded, err := XORDecode("", "key")
	if err != nil {
		t.Fatal(err)
	}
	if decoded != "" {
		t.Errorf("expected empty string, got %q", decoded)
	}
}
