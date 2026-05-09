// Package oauth implements OAuth 2.0 flows for credential providers.
package oauth

import (
	"encoding/hex"
	"fmt"
)

// XORDecode decodes a hex-encoded XOR-obfuscated string.
// This is NOT encryption — it defeats automated secret scanners only.
func XORDecode(hexStr, key string) (string, error) {
	raw, err := hex.DecodeString(hexStr)
	if err != nil {
		return "", fmt.Errorf("invalid hex: %w", err)
	}
	out := make([]byte, len(raw))
	for i, b := range raw {
		out[i] = b ^ key[i%len(key)]
	}
	return string(out), nil
}

// XOREncode encodes a string with XOR obfuscation, returning hex.
func XOREncode(plaintext, key string) string {
	out := make([]byte, len(plaintext))
	for i, c := range []byte(plaintext) {
		out[i] = c ^ key[i%len(key)]
	}
	return hex.EncodeToString(out)
}
