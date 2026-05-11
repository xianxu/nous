package proxy

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/xianxu/nous/lib/provider/oauth"
	charonruntime "github.com/xianxu/nous/lib/provider/runtime"
	"github.com/xianxu/nous/lib/provider/vault"
)

// Serve runs the HTTPS credential proxy as a context-cancellable
// function. Bundles the bootstrap that previously lived inline in
// lib/charoncli.ServeCmd's RunE: catalog load + CA init + audit
// log + Google OAuth refresher + runtime-file publish + runtime
// socket bind + Server construction + ListenAndServe.
//
// Designed for both `charon serve` (legacy single-binary daemon) and
// `nous serve` (nous#16 M2, where Serve runs as one goroutine
// alongside brainsync.Run in a unified process). Callers own ctx +
// signal handling; this function does NOT install signal traps.
//
// Returns when ctx is cancelled (graceful shutdown via http.Server's
// Shutdown method) or when the server errors out. Best-effort
// resources (runtime file, runtime socket) are torn down before
// returning either way.
func Serve(ctx context.Context, opts ServeOptions) error {
	if opts.Listen == "" {
		return fmt.Errorf("proxy.Serve: Listen address required")
	}
	if opts.Vault == nil {
		return fmt.Errorf("proxy.Serve: Vault store required")
	}

	ca, err := LoadOrCreateCA()
	if err != nil {
		return fmt.Errorf("init CA: %w", err)
	}
	log.Printf("CA loaded from keychain")

	bundlePath, cleanup, err := BuildCABundle(ca.CertPEM)
	if err != nil {
		return fmt.Errorf("build CA bundle: %w", err)
	}
	defer cleanup()
	log.Printf("CA bundle: %s", bundlePath)

	audit, err := NewAuditLog(opts.AuditPath)
	if err != nil {
		return fmt.Errorf("init audit log: %w", err)
	}
	defer audit.Close()

	refreshers := make(map[string]Refresher)
	if gp, err := oauth.NewGoogleProvider(); err == nil {
		refreshers["google"] = gp
	} else {
		log.Printf("warning: Google OAuth not available: %v", err)
	}

	srv := &Server{
		Vault:        opts.Vault,
		Audit:        audit,
		Addr:         opts.Listen,
		CA:           ca,
		Refreshers:   refreshers,
		Verbose:      opts.Verbose,
		ScopeTracker: NewScopeTracker(100, 24*time.Hour),
		// Boots disarmed (issue #16 A spec). User must `charon arm`,
		// `nous provider arm`, or click Charon Security.app's menubar
		// to enable CONNECTs.
		Session: NewSession(),
	}

	// Publish runtime info so other CLI invocations can find the
	// proxy without --addr. Best-effort: write failure logs but
	// doesn't abort serve. Removed on shutdown; stale files from a
	// crash are tolerated since the next serve overwrites and
	// `manifest`'s healthz probe surfaces "running: false".
	if err := charonruntime.Write(opts.Listen); err != nil {
		log.Printf("warning: runtime file write failed: %v", err)
	} else {
		log.Printf("runtime file: %s", charonruntime.Path())
	}
	defer charonruntime.Remove()

	// Bring up the runtime-consent unix socket. DR-pinned to
	// com.charon.security so only Charon Security.app can drive
	// arm/disarm. Best-effort: bind failure logs but doesn't abort
	// serve — the HTTP /session/* endpoints still work as a
	// fallback.
	runtimeSock, sockErr := StartRuntimeSocket(srv)
	if sockErr != nil {
		log.Printf("warning: runtime socket bind failed: %v", sockErr)
	}
	defer func() {
		if runtimeSock != nil {
			_ = runtimeSock.Close()
		}
	}()

	return srv.ListenAndServe(ctx)
}

// ServeOptions bundles config for Serve. Listen and Vault are required.
type ServeOptions struct {
	// Listen is the HTTPS proxy listen address (e.g. "127.0.0.1:8230").
	Listen string

	// Vault is the credential store the proxy reads from.
	Vault vault.Store

	// AuditPath is the audit log file path. Empty → stderr.
	AuditPath string

	// Verbose enables debug-level logging on the proxy.
	Verbose bool
}
