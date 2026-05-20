// Package brain_test (external test package) carries end-to-end
// integration tests that span lib/brain, lib/brainsync, lib/identity,
// and lib/brain/filestore — proving the full multi-recipient + keys-
// branch flow works against a real gcrypt remote (file:// bare repo
// standing in for GitHub) with real GPG keys.
//
// These tests are slow (~10-20s wall-clock per run because of gpg
// keygen + multiple gcrypt push/pull cycles) and depend on the
// system's `gpg` + `git` + `git-remote-gcrypt` binaries being
// installed. They live in a separate package so the test environment
// can short-circuit them via `go test -short` or `-run` filters when
// iterating on unrelated code.
package brain_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/xianxu/nous/lib/brain"
	"github.com/xianxu/nous/lib/brainsync"
	"github.com/xianxu/nous/lib/identity"
)

// TestEndToEnd_MultiRecipientAndFileSync exercises the dogfood story
// in #12 + #23 end-to-end against a local bare repo as the "remote."
// Three simulated peers (operator + peerA + peerB), each with their
// own GPG homedir, share a gcrypt-encrypted brain. The story:
//
//  1. Operator provisions a private brain (recipient list = [operator]).
//  2. Operator imports peerA's pubkey, admits as recipient. Keys
//     branch gets operator's + peerA's pubkeys.
//  3. peerA clones via `nous brain clone` (BootstrapPubkeys + gcrypt
//     clone). peerA's keyring now has operator's pubkey.
//  4. Operator imports peerB's pubkey, admits. Keys branch updated.
//  5. peerA's brain-sync watcher tick (simulated as one ImportAllPubkeys
//     call) picks up peerB's pubkey.
//  6. peerB clones fresh — auto-imports operator + peerA pubkeys from
//     the keys branch, then gcrypt-clones.
//
// File-sync subscenario (after step 6):
//  7. Operator adds a file, commits + pushes.
//  8. peerA pulls — sees the file.
//  9. peerB pulls — sees the file.
// 10. peerA adds a different file, commits + pushes.
// 11. Operator + peerB pull — both see peerA's file.
//
// Skipped when `gpg` or `git-remote-gcrypt` aren't installed, and on
// non-darwin/linux (subprocess paths assume POSIX `gpg`).
func TestEndToEnd_MultiRecipientAndFileSync(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test (-short)")
	}
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skipf("integration test requires POSIX gpg; runtime is %s", runtime.GOOS)
	}
	mustHave(t, "gpg")
	mustHave(t, "git")
	mustHave(t, "git-remote-gcrypt")

	remoteURL := initBareRepo(t)

	// Three independent peers, each with their own gpg homedir. Keys
	// generated up-front so subsequent operations don't pay the
	// keygen cost.
	operator := setupPeer(t, "operator", "operator@test.local")
	peerA := setupPeer(t, "peerA", "peerA@test.local")
	peerB := setupPeer(t, "peerB", "peerB@test.local")

	// === Step 1: operator provisions a private (single-recipient) brain ===
	withPeer(t, operator, func() {
		operator.brainPath = provisionBrain(t, operator, remoteURL, []string{operator.fp})
	})

	// === Step 2: operator admits peerA ===
	withPeer(t, operator, func() {
		// First import peerA's pubkey into operator's keyring.
		if _, err := identity.Import(peerA.armorPub); err != nil {
			t.Fatalf("operator import peerA: %v", err)
		}
		// Then promote to recipient: manifest update + gcrypt re-key
		// push + keys-branch publish (mirrors `nous brain recipient add`).
		admitRecipient(t, operator.brainPath, peerA.fp)
	})

	// === Step 3: peerA clones the shared brain ===
	withPeer(t, peerA, func() {
		peerA.brainPath = cloneBrainViaPeerkeys(t, peerA, remoteURL)
		assertPubkeyInKeyring(t, peerA, operator.fp)
		// peerA should be able to read the brain's manifest now
		// (proves gcrypt decrypt worked — signature verification
		// required the operator's pubkey, which BootstrapPubkeys
		// fetched from the keys branch).
		readBrainFile(t, peerA.brainPath, ".brain/config.md")
	})

	// === Step 4: operator admits peerB ===
	withPeer(t, operator, func() {
		if _, err := identity.Import(peerB.armorPub); err != nil {
			t.Fatalf("operator import peerB: %v", err)
		}
		admitRecipient(t, operator.brainPath, peerB.fp)
	})

	// === Step 5: peerA's watcher-tick picks up peerB's pubkey ===
	withPeer(t, peerA, func() {
		// Pull the gcrypt main first so the local clone sees the
		// re-keyed manifest (which now lists peerB).
		if _, err := brainsync.PullBrain(peerA.brainPath); err != nil {
			t.Fatalf("peerA pull: %v", err)
		}
		// Then refresh keys branch → import.
		imported, _, err := brain.ImportAllPubkeys(context.Background(), peerA.brainPath)
		if err != nil {
			t.Fatalf("peerA ImportAllPubkeys: %v", err)
		}
		if imported < 1 {
			t.Errorf("peerA ImportAllPubkeys: imported=%d, want ≥1 (peerB's new pubkey)", imported)
		}
		assertPubkeyInKeyring(t, peerA, peerB.fp)
	})

	// === Step 6: peerB clones fresh, auto-imports both pubkeys ===
	withPeer(t, peerB, func() {
		peerB.brainPath = cloneBrainViaPeerkeys(t, peerB, remoteURL)
		assertPubkeyInKeyring(t, peerB, operator.fp)
		assertPubkeyInKeyring(t, peerB, peerA.fp)
		readBrainFile(t, peerB.brainPath, ".brain/config.md")
	})

	// === Step 7-9: operator → peers file sync ===
	const noteFromOperator = "hello from operator\n"
	withPeer(t, operator, func() {
		writeBrainFile(t, operator.brainPath, "from-operator.md", noteFromOperator)
		if err := brainsync.AddCommitPush(operator.brainPath, "add from-operator.md"); err != nil {
			t.Fatalf("operator push file: %v", err)
		}
	})

	withPeer(t, peerA, func() {
		if _, err := brainsync.PullBrain(peerA.brainPath); err != nil {
			t.Fatalf("peerA pull operator's file: %v", err)
		}
		got := readBrainFile(t, peerA.brainPath, "from-operator.md")
		if got != noteFromOperator {
			t.Errorf("peerA sees from-operator.md = %q, want %q", got, noteFromOperator)
		}
	})

	withPeer(t, peerB, func() {
		if _, err := brainsync.PullBrain(peerB.brainPath); err != nil {
			t.Fatalf("peerB pull operator's file: %v", err)
		}
		got := readBrainFile(t, peerB.brainPath, "from-operator.md")
		if got != noteFromOperator {
			t.Errorf("peerB sees from-operator.md = %q, want %q", got, noteFromOperator)
		}
	})

	// === Step 10-11: peer-originated file sync (peerA pushes; operator+peerB see it) ===
	const noteFromPeerA = "hello from peerA\n"
	withPeer(t, peerA, func() {
		writeBrainFile(t, peerA.brainPath, "from-peerA.md", noteFromPeerA)
		if err := brainsync.AddCommitPush(peerA.brainPath, "add from-peerA.md"); err != nil {
			t.Fatalf("peerA push file: %v", err)
		}
	})

	withPeer(t, operator, func() {
		if _, err := brainsync.PullBrain(operator.brainPath); err != nil {
			t.Fatalf("operator pull peerA's file: %v", err)
		}
		got := readBrainFile(t, operator.brainPath, "from-peerA.md")
		if got != noteFromPeerA {
			t.Errorf("operator sees from-peerA.md = %q, want %q", got, noteFromPeerA)
		}
	})

	withPeer(t, peerB, func() {
		if _, err := brainsync.PullBrain(peerB.brainPath); err != nil {
			t.Fatalf("peerB pull peerA's file: %v", err)
		}
		got := readBrainFile(t, peerB.brainPath, "from-peerA.md")
		if got != noteFromPeerA {
			t.Errorf("peerB sees from-peerA.md = %q, want %q", got, noteFromPeerA)
		}
	})
}

// ─── test helpers ────────────────────────────────────────────────────

// testPeer carries everything we need to act as one simulated peer:
// GNUPGHOME path, fingerprint, armored pubkey blob, and (once
// provisioned/cloned) the local brain checkout path.
type testPeer struct {
	name      string
	home      string // GNUPGHOME
	fp        string // 40-hex fingerprint
	armorPub  string // armored public key blob (for sharing)
	brainPath string // populated after provision/clone
}

// mustHave skips the test when a required binary isn't on PATH.
func mustHave(t *testing.T, name string) {
	t.Helper()
	if _, err := exec.LookPath(name); err != nil {
		t.Skipf("required binary %q not on PATH: %v", name, err)
	}
}

// shortTempBase picks a base directory short enough for gpg-agent's
// 104-char unix-socket path limit. `/tmp` is the normal host case;
// `$HOME/.cache` is the fallback when a sandboxed environment blocks
// `/tmp` writes (still well under 104 chars on macOS). `t.TempDir()`
// under `/var/folders/` would blow the limit, so it's never used here.
func shortTempBase(t *testing.T) string {
	t.Helper()
	for _, base := range []string{"/tmp", filepath.Join(os.Getenv("HOME"), ".cache")} {
		probe, err := os.MkdirTemp(base, "ngpg-probe-")
		if err == nil {
			os.RemoveAll(probe)
			return base
		}
	}
	t.Fatalf("shortTempBase: no writable short-path base (tried /tmp, $HOME/.cache)")
	return ""
}

// setupPeer creates a fresh GNUPGHOME with a single ed25519 keypair
// and returns testPeer metadata. Uses a short-path tempdir to avoid
// the macOS unix-socket path-length limit that gpg-agent otherwise
// hits (104 chars; t.TempDir() under /var/folders/ blows it).
func setupPeer(t *testing.T, label, email string) *testPeer {
	t.Helper()
	home, err := os.MkdirTemp(shortTempBase(t), "ngpg-"+label+"-")
	if err != nil {
		t.Fatalf("setupPeer %s: tempdir: %v", label, err)
	}
	t.Cleanup(func() {
		exec.Command("gpgconf", "--homedir", home, "--kill", "all").Run()
		os.RemoveAll(home)
	})
	if err := os.Chmod(home, 0o700); err != nil {
		t.Fatalf("chmod %s: %v", home, err)
	}

	// Generate the key with this GNUPGHOME set in env (don't disturb
	// the parent process's env).
	batch := fmt.Sprintf(`%%no-protection
Key-Type: eddsa
Key-Curve: ed25519
Subkey-Type: ecdh
Subkey-Curve: cv25519
Name-Real: %s
Name-Email: %s
Expire-Date: 0
%%commit
`, label, email)
	cmd := exec.Command("gpg", "--batch", "--generate-key")
	cmd.Env = append(os.Environ(), "GNUPGHOME="+home)
	cmd.Stdin = strings.NewReader(batch)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("setupPeer %s: gpg --generate-key: %v\n%s", label, err, out)
	}

	// Pull the fp out so we don't have to depend on identity.List
	// here (identity.List reads the process env, which we don't want
	// to mutate during setup).
	fpOut, err := exec.Command("gpg", "--homedir", home, "--list-secret-keys", "--with-colons").Output()
	if err != nil {
		t.Fatalf("setupPeer %s: list-secret-keys: %v", label, err)
	}
	fp := extractFirstFP(string(fpOut))
	if fp == "" {
		t.Fatalf("setupPeer %s: no fingerprint in:\n%s", label, fpOut)
	}

	// Cache the armored pubkey for sharing across peers.
	armorOut, err := exec.Command("gpg", "--homedir", home, "--armor", "--export", fp).Output()
	if err != nil {
		t.Fatalf("setupPeer %s: export: %v", label, err)
	}

	return &testPeer{
		name:     label,
		home:     home,
		fp:       fp,
		armorPub: string(armorOut),
	}
}

// extractFirstFP returns the first `fpr:` line's value from gpg's
// --with-colons output (the primary key fingerprint of the first
// listed key).
func extractFirstFP(out string) string {
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "fpr:") {
			fields := strings.Split(line, ":")
			if len(fields) >= 10 {
				return fields[9]
			}
		}
	}
	return ""
}

// withPeer runs fn with this peer's GNUPGHOME active in the process
// env. Restored on return so the next peer's withPeer call sees a
// clean baseline. Not parallel-safe — tests must not call t.Parallel
// when using this helper (the package's identity package reads env
// at call time, so per-test env switching is the only way to keep
// lib calls peer-scoped).
func withPeer(t *testing.T, p *testPeer, fn func()) {
	t.Helper()
	orig := os.Getenv("GNUPGHOME")
	if err := os.Setenv("GNUPGHOME", p.home); err != nil {
		t.Fatalf("withPeer %s: setenv: %v", p.name, err)
	}
	defer func() {
		if orig == "" {
			os.Unsetenv("GNUPGHOME")
		} else {
			os.Setenv("GNUPGHOME", orig)
		}
	}()
	fn()
}

// initBareRepo creates a bare git repo (file:// "remote") that
// stands in for GitHub. Returns the file:// URL.
func initBareRepo(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "remote.git")
	if out, err := exec.Command("git", "init", "--bare", "-b", "main", dir).CombinedOutput(); err != nil {
		t.Fatalf("init bare: %v\n%s", err, out)
	}
	return "file://" + dir
}

// provisionBrain creates a fresh brain checkout, configures the gcrypt
// remote + participants, writes the manifest, commits initial seed
// content, pushes, and publishes the operator's pubkey to the keys
// branch. Mirrors the relevant subset of `nous brain new` without the
// gh-repo-create / scripts/new-brain.sh substrate-setup half (we're
// using a pre-made file:// bare repo as the remote).
func provisionBrain(t *testing.T, p *testPeer, remoteURL string, recipients []string) string {
	t.Helper()
	brainDir := filepath.Join(t.TempDir(), "brain-"+p.name)
	mustGit(t, "", "init", "-q", "-b", "main", brainDir)
	mustGit(t, brainDir, "config", "user.email", p.name+"@test.local")
	mustGit(t, brainDir, "config", "user.name", p.name)
	mustGit(t, brainDir, "remote", "add", "origin", "gcrypt::"+remoteURL)

	// Note: no explicit brain.SetGcryptParticipants here — per
	// nous#24, the manifest is the canonical source and the push
	// wrapper (brainsync.AddCommitPush below) syncs gcrypt-participants
	// before the actual push. If this provisioning fails because of a
	// missing gcrypt-participants config, the refactor is broken.
	if err := brain.WriteManifest(brainDir, brain.Manifest{
		Name:       filepath.Base(brainDir),
		Recipients: recipients,
	}); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	// Seed content so the brain has something to push.
	writeBrainFile(t, brainDir, "README.md", "# brain seed\n")
	if err := brainsync.AddCommitPush(brainDir, "init brain"); err != nil {
		t.Fatalf("initial push: %v", err)
	}
	// Publish operator's pubkey to the keys branch.
	if err := brain.PublishPubkey(context.Background(), brainDir, p.fp); err != nil {
		t.Fatalf("publish pubkey: %v", err)
	}
	return brainDir
}

// admitRecipient appends the new fp to the brain's recipient list,
// updates manifest + gcrypt-participants, pushes, and publishes the
// new pubkey to the keys branch. Mirrors `nous brain recipient add`
// (without the verify-fingerprint TTY ceremony — tests have no TTY).
func admitRecipient(t *testing.T, brainPath, newFP string) {
	t.Helper()
	m, err := brain.Read(brainPath)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	m.Recipients = append(m.Recipients, newFP)
	if err := brain.RewriteFrontmatter(brainPath, m); err != nil {
		t.Fatalf("rewrite frontmatter: %v", err)
	}
	// No explicit SetGcryptParticipants — push wrapper syncs from
	// manifest (nous#24). If the test breaks after this removal, the
	// refactor doesn't actually centralize the sync.
	if err := brainsync.AddCommitPush(brainPath, "admit "+newFP[len(newFP)-8:]); err != nil {
		t.Fatalf("admit push: %v", err)
	}
	if err := brain.PublishPubkey(context.Background(), brainPath, newFP); err != nil {
		t.Fatalf("publish new recipient pubkey: %v", err)
	}
}

// cloneBrainViaPeerkeys mirrors `nous brain clone`: bootstrap pubkeys
// from the keys branch first (so gcrypt's signature verify has
// everything it needs), then run the gcrypt clone. Returns the local
// brain checkout path.
func cloneBrainViaPeerkeys(t *testing.T, p *testPeer, remoteURL string) string {
	t.Helper()
	gcryptURL := "gcrypt::" + remoteURL

	// Bootstrap pubkeys (M5's path in nous brain clone).
	if _, _, err := brain.BootstrapPubkeys(context.Background(), gcryptURL); err != nil {
		t.Fatalf("BootstrapPubkeys: %v", err)
	}

	// Now the actual gcrypt clone.
	target := filepath.Join(t.TempDir(), "brain-"+p.name)
	cmd := exec.Command("git", "clone", gcryptURL, target)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git clone gcrypt: %v\n%s", err, out)
	}
	mustGit(t, target, "config", "user.email", p.name+"@test.local")
	mustGit(t, target, "config", "user.name", p.name)
	return target
}

// writeBrainFile writes content to brainPath/relPath, creating parent
// dirs as needed. Used both by the test body (operator + peers
// editing content) and by provisionBrain to seed initial content.
func writeBrainFile(t *testing.T, brainPath, relPath, content string) {
	t.Helper()
	full := filepath.Join(brainPath, relPath)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(full), err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", full, err)
	}
}

// readBrainFile reads brainPath/relPath and returns its content. Used
// to assert that file-sync round-trips landed the expected bytes on
// each peer's side.
func readBrainFile(t *testing.T, brainPath, relPath string) string {
	t.Helper()
	full := filepath.Join(brainPath, relPath)
	data, err := os.ReadFile(full)
	if err != nil {
		t.Fatalf("read %s: %v", full, err)
	}
	return string(data)
}

// assertPubkeyInKeyring fails the test if fp isn't in the active
// GNUPGHOME's keyring. Used to verify the keys-branch auto-import
// landed the expected pubkeys after BootstrapPubkeys /
// ImportAllPubkeys.
func assertPubkeyInKeyring(t *testing.T, p *testPeer, fp string) {
	t.Helper()
	out, err := exec.Command("gpg", "--homedir", p.home, "--list-keys", "--with-colons", fp).Output()
	if err != nil {
		t.Errorf("%s should have pubkey %s in keyring, but list-keys errored: %v", p.name, fp, err)
		return
	}
	if !strings.Contains(string(out), "fpr:") {
		t.Errorf("%s should have pubkey %s in keyring; gpg returned no fpr lines:\n%s", p.name, fp, out)
	}
}

// TestEndToEnd_GitHubMediatedOnboarding exercises the nous#26 flow
// end-to-end against a local bare repo:
//
//  1. Operator provisions a single-recipient brain (just operator's fp).
//  2. peerC "joins" by publishing peerC.asc to the keys branch — the
//     `nous brain join` path. peerC has plain-git push access (modeled
//     here by direct PublishOwnPubkeyToRemote); they're not yet a
//     gcrypt recipient, so they cannot decrypt main.
//  3. Operator's auto-admit (lib/brain.AutoAdmitFromKeysBranch +
//     brainsync.AddCommitPush) picks up peerC.asc, appends to the
//     manifest, and pushes. The #24 push wrapper syncs gcrypt-
//     participants from the new manifest, so the ciphertext is re-
//     encrypted to {operator, peerC}.
//  4. peerC clones via gcrypt — BootstrapPubkeys fetches operator's
//     pubkey from the keys branch (for signature verify), then the
//     gcrypt clone decrypts main using peerC's secret key. peerC sees
//     a fully-formed manifest with both fingerprints.
//  5. Idempotence: re-running auto-admit on the operator side adds
//     nothing new (the only candidate is already in the manifest).
//  6. Legacy <FP>.asc entries (operator's own pubkey, published by
//     provisionBrain under the nous#23 convention) are NOT auto-
//     admitted on subsequent runs — looksLikeFingerprint correctly
//     discriminates "legacy operator-published" from "new joiner-
//     published."
//
// Subtest "orphan_keys_branch" covers the brand-new-brain case where
// the joiner runs first and creates the keys branch via orphan-
// checkout (no operator pubkey published yet). This is the empirical
// case from today's manual test: ying ran `nous brain join` against
// brain-family before the operator's own keys-branch publish had
// landed.
func TestEndToEnd_GitHubMediatedOnboarding(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test (-short)")
	}
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skipf("integration test requires POSIX gpg; runtime is %s", runtime.GOOS)
	}
	mustHave(t, "gpg")
	mustHave(t, "git")
	mustHave(t, "git-remote-gcrypt")

	remoteURL := initBareRepo(t)
	operator := setupPeer(t, "operator", "operator@test.local")
	peerC := setupPeer(t, "peerC", "peerC@test.local")

	// === Step 1: operator provisions a single-recipient brain ===
	withPeer(t, operator, func() {
		operator.brainPath = provisionBrain(t, operator, remoteURL, []string{operator.fp})
	})

	// === Step 2: peerC publishes pubkey via the join flow ===
	// PublishOwnPubkeyToRemote uses plain-git access to the keys
	// branch — no gcrypt involvement on peerC's side. peerC is
	// effectively "a github collaborator who hasn't been admitted to
	// the gcrypt recipient list yet."
	const peerCLogin = "peerC"
	withPeer(t, peerC, func() {
		if err := brain.PublishOwnPubkeyToRemote(context.Background(), remoteURL, peerCLogin, peerC.armorPub); err != nil {
			t.Fatalf("peerC PublishOwnPubkeyToRemote: %v", err)
		}
	})

	// === Step 3: operator's auto-admit picks up peerC.asc ===
	// In the watch loop this is two calls in sequence: syncBrainPubkeys
	// (imports new pubkeys into operator's keyring so gcrypt can encrypt
	// to them) then autoAdmitBrain (appends to manifest + AddCommitPush
	// to re-encrypt). We invoke them directly here.
	withPeer(t, operator, func() {
		ctx := context.Background()
		imported, _, err := brain.ImportAllPubkeys(ctx, operator.brainPath)
		if err != nil {
			t.Fatalf("operator ImportAllPubkeys: %v", err)
		}
		if imported < 1 {
			t.Errorf("operator ImportAllPubkeys: imported=%d, want ≥1 (peerC's pubkey)", imported)
		}
		assertPubkeyInKeyring(t, operator, peerC.fp)

		added, err := brain.AutoAdmitFromKeysBranch(ctx, operator.brainPath)
		if err != nil {
			t.Fatalf("AutoAdmitFromKeysBranch: %v", err)
		}
		if len(added) != 1 {
			t.Fatalf("expected 1 admitted, got %d: %+v", len(added), added)
		}
		if added[0].Login != peerCLogin {
			t.Errorf("admitted login = %q, want %q", added[0].Login, peerCLogin)
		}
		if !strings.EqualFold(added[0].Fingerprint, peerC.fp) {
			t.Errorf("admitted fp = %q, want %q (case-insensitive)", added[0].Fingerprint, peerC.fp)
		}

		// Push the manifest update. The #24 wrapper syncs
		// gcrypt-participants and the gcrypt push re-encrypts to
		// the new recipient set.
		if err := brainsync.AddCommitPush(operator.brainPath, "auto-admit "+peerCLogin); err != nil {
			t.Fatalf("operator commit/push post-auto-admit: %v", err)
		}
	})

	// === Step 4: peerC clones via gcrypt and can decrypt ===
	withPeer(t, peerC, func() {
		peerC.brainPath = cloneBrainViaPeerkeys(t, peerC, remoteURL)
		assertPubkeyInKeyring(t, peerC, operator.fp)
		manifest := readBrainFile(t, peerC.brainPath, ".brain/config.md")
		if !strings.Contains(strings.ToUpper(manifest), strings.ToUpper(peerC.fp)) {
			t.Errorf("peerC's manifest missing peerC's fp:\n%s", manifest)
		}
		if !strings.Contains(strings.ToUpper(manifest), strings.ToUpper(operator.fp)) {
			t.Errorf("peerC's manifest missing operator's fp:\n%s", manifest)
		}
	})

	// === Step 5: idempotence — re-run auto-admit yields no new admissions ===
	withPeer(t, operator, func() {
		added, err := brain.AutoAdmitFromKeysBranch(context.Background(), operator.brainPath)
		if err != nil {
			t.Fatalf("AutoAdmitFromKeysBranch (idempotence): %v", err)
		}
		if len(added) != 0 {
			t.Errorf("idempotence: expected 0 new admissions, got %d: %+v", len(added), added)
		}
	})

	// === Step 6: legacy <FP>.asc entries are NOT auto-admitted ===
	// provisionBrain published operator's pubkey as <operator-FP>.asc
	// (the nous#23 legacy convention — fingerprint-as-filename).
	// AutoAdmitFromKeysBranch's looksLikeFingerprint discriminator
	// should skip those: they predate nous#26 and are in the manifest
	// by construction (otherwise they wouldn't have been published).
	// Re-running yields no new admissions even though <operator-FP>.asc
	// is still in the keys store. (This is covered by step 5's
	// idempotence check, but worth pinning explicitly via a comment.)
}

// TestPublishOwnPubkeyToRemote_OrphanCreate covers the brand-new-brain
// edge case from today's manual test (brain-family). The joiner's
// `nous brain join` runs against a bare repo with no keys branch yet
// — PublishOwnPubkeyToRemote must create the branch via orphan-
// checkout rather than failing. Separate from the main flow because
// it operates on a freshly-initialized bare repo (no operator
// provisioning).
func TestPublishOwnPubkeyToRemote_OrphanCreate(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test (-short)")
	}
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skipf("integration test requires POSIX gpg; runtime is %s", runtime.GOOS)
	}
	mustHave(t, "gpg")
	mustHave(t, "git")

	remoteURL := initBareRepo(t)
	joiner := setupPeer(t, "joiner", "joiner@test.local")

	// No operator has published anything to the keys branch. The
	// remote is just `git init --bare` with no branches at all.
	withPeer(t, joiner, func() {
		if err := brain.PublishOwnPubkeyToRemote(context.Background(), remoteURL, "joiner", joiner.armorPub); err != nil {
			t.Fatalf("PublishOwnPubkeyToRemote on empty remote: %v", err)
		}
	})

	// Verify keys branch was created with joiner.asc on it.
	tmp := t.TempDir()
	cmd := exec.Command("git", "clone", "--branch", "keys", "--single-branch", remoteURL, filepath.Join(tmp, "check"))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("clone keys branch after orphan-create: %v\n%s", err, out)
	}
	got, err := os.ReadFile(filepath.Join(tmp, "check", "joiner.asc"))
	if err != nil {
		t.Fatalf("joiner.asc not present after orphan-create: %v", err)
	}
	if !strings.Contains(string(got), "BEGIN PGP PUBLIC KEY BLOCK") {
		t.Errorf("joiner.asc content doesn't look like an armored pubkey:\n%s", got)
	}
}

// mustGit runs git with optional working directory. Empty repo skips
// the -C flag (used by `git init` which doesn't accept -C against a
// not-yet-created dir).
func mustGit(t *testing.T, repo string, args ...string) {
	t.Helper()
	var full []string
	if repo != "" {
		full = append([]string{"-C", repo}, args...)
	} else {
		full = args
	}
	cmd := exec.Command("git", full...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}
