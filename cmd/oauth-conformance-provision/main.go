// Command oauth-conformance-provision obtains an OAuth refresh token via the
// provider's interactive consent flow and stores it in the macOS Keychain entry
// the grounding test reads (lib/provider/oauth/contract_real_test.go).
//
// Why a tool and not a pasted token: a refresh token is bound to the client that
// issued it, so it must come from charon's own client_id (Google) or the Entra
// app's client_id (Microsoft) — another client's token won't refresh under it.
// And consent is interactive (non-headless), so it can't be scripted headlessly.
//
// Usage:
//
//	# Google (charon's embedded client):
//	go run ./cmd/oauth-conformance-provision [-account hint@gmail.com]
//
//	# Microsoft/Entra (public client, PKCE — your Entra app's IDs):
//	MICROSOFT_CLIENT_ID=<app-id> MICROSOFT_TENANT_ID=<tenant-id> \
//	  go run ./cmd/oauth-conformance-provision -provider microsoft
//
// Opens a browser; consent with a THROWAWAY account. The conformance test is
// Refresh-only (read-only, never Revoke), so the account is never mutated.
// Re-run to refresh the stored token. Note: Microsoft rotates the refresh token
// on every use (single-use), so the conformance test persists the rotation back
// to Keychain — re-provision only if the stored token goes stale (>90 days idle).
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"

	"github.com/xianxu/nous/lib/provider/oauth"
)

func main() {
	provider := flag.String("provider", "google", "OAuth provider: google | microsoft")
	account := flag.String("account", "", "account login hint (optional)")
	service := flag.String("service", "", "Keychain service to store the token under (default: per-provider)")
	clientID := flag.String("client-id", os.Getenv("MICROSOFT_CLIENT_ID"), "Microsoft: Entra app Application (client) ID (or $MICROSOFT_CLIENT_ID)")
	tenant := flag.String("tenant", os.Getenv("MICROSOFT_TENANT_ID"), "Microsoft: Entra Directory (tenant) ID (or $MICROSOFT_TENANT_ID)")
	flag.Parse()

	var (
		gp           oauth.Provider
		scopes       []string
		svc          string
		consentLabel string
		certRun      string
	)
	switch *provider {
	case "google":
		g, err := oauth.NewGoogleProvider()
		if err != nil {
			fatalf("init google provider: %v", err)
		}
		gp = g
		// openid only: enough to refresh + extract the account email; the cert
		// never needs data scopes (it only calls Refresh/CheckHealth).
		scopes = []string{"openid"}
		svc = defaultService(*service, oauth.ConformanceKeychainService)
		consentLabel = "THROWAWAY Google account"
		certRun = "Contract_RealGoogle"
	case "microsoft":
		if *clientID == "" || *tenant == "" {
			fatalf("microsoft needs -client-id and -tenant (or $MICROSOFT_CLIENT_ID / $MICROSOFT_TENANT_ID)")
		}
		gp = oauth.NewMicrosoftProvider(*clientID, *tenant)
		// openid+profile → preferred_username identity; offline_access → refresh
		// token (the dialect merges these required scopes in regardless, but we
		// pass them for clarity / a minimal consent screen).
		scopes = []string{"openid", "profile", "offline_access"}
		svc = defaultService(*service, oauth.ConformanceKeychainServiceMicrosoft)
		consentLabel = "THROWAWAY Microsoft/Entra account"
		certRun = "Contract_RealMicrosoft"
	default:
		fatalf("unknown -provider %q (want google | microsoft)", *provider)
	}

	fmt.Fprintf(os.Stderr, "Opening browser — consent with a %s…\n", consentLabel)
	cred, err := gp.Auth(*account, scopes, nil, false)
	if err != nil {
		fatalf("oauth consent: %v", err)
	}
	if cred.RefreshToken == "" {
		fatalf("consent returned no refresh token (offline access not granted)")
	}

	// cred.Account is the identity the library already extracted from the ID
	// token (Google: email; Microsoft: preferred_username) — don't re-parse.
	cmd := exec.Command("security", keychainStoreArgs(svc, cred.Account, cred.RefreshToken)...)
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fatalf("store refresh token in Keychain (%s): %v", svc, err)
	}

	fmt.Printf("✓ stored refresh token for %s in Keychain service %q\n", cred.Account, svc)
	fmt.Println("Certify the fake against the real provider with:")
	fmt.Printf("  go test -tags conformance ./lib/provider/oauth/ -run %s -v\n", certRun)
}

// defaultService returns the explicit -service override if set, else the
// per-provider default Keychain service.
func defaultService(override, fallback string) string {
	if override != "" {
		return override
	}
	return fallback
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
