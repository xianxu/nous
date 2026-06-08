// Command oauth-conformance-provision obtains a Google OAuth refresh token via
// charon's OWN OAuth client (the interactive consent flow) and stores it in the
// macOS Keychain entry the grounding test reads
// (lib/provider/oauth/contract_real_test.go).
//
// Why a tool and not a pasted token: a Google refresh token is bound to the
// client that issued it, so it must come from charon's embedded client_id —
// Google's OAuth Playground or any other client won't refresh under it. And
// consent is interactive (non-headless), so it can't be scripted headlessly.
//
// Usage:
//
//	go run ./cmd/oauth-conformance-provision [-account hint@gmail.com]
//
// Opens a browser; consent with a THROWAWAY Google account. The conformance test
// is Refresh-only (read-only, never Revoke), so the account is never mutated.
// Re-run to refresh the stored token (~monthly, or on suspected drift).
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"

	"github.com/xianxu/nous/lib/provider/oauth"
)

func main() {
	account := flag.String("account", "", "Google account login hint (optional)")
	service := flag.String("service", oauth.ConformanceKeychainService, "Keychain service to store the refresh token under")
	flag.Parse()

	gp, err := oauth.NewGoogleProvider()
	if err != nil {
		fatalf("init google provider: %v", err)
	}

	fmt.Fprintln(os.Stderr, "Opening browser — consent with a THROWAWAY Google account…")
	// openid only: enough to refresh + extract the account email; the cert
	// never needs data scopes (it only calls Refresh/CheckHealth).
	cred, err := gp.Auth(*account, []string{"openid"}, nil, false)
	if err != nil {
		fatalf("oauth consent: %v", err)
	}
	if cred.RefreshToken == "" {
		fatalf("consent returned no refresh token (offline access not granted)")
	}

	// cred.Account is the ID-token email already extracted by the library —
	// don't re-parse the JWT here.
	cmd := exec.Command("security", keychainStoreArgs(*service, cred.Account, cred.RefreshToken)...)
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fatalf("store refresh token in Keychain (%s): %v", *service, err)
	}

	fmt.Printf("✓ stored refresh token for %s in Keychain service %q\n", cred.Account, *service)
	fmt.Println("Certify the fake against real Google with:")
	fmt.Println("  go test -tags conformance ./lib/provider/oauth/ -run Contract_Real -v")
}

// keychainStoreArgs builds the `security` argv that upserts (-U) a generic
// password. Pure so the argument wiring is unit-testable without touching the
// real Keychain; exec stays at the boundary in main.
func keychainStoreArgs(service, account, secret string) []string {
	return []string{
		"add-generic-password",
		"-U", // update in place if the entry already exists
		"-s", service,
		"-a", account,
		"-w", secret,
	}
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "error: "+format+"\n", args...)
	os.Exit(1)
}
