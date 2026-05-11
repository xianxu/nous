package security

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/charmbracelet/glamour"
	"golang.org/x/term"
)

// RemedyEntry is the long-form prose for one finding class. The Ref
// matches Finding.RemedyRef so multiple findings collapse to one
// remedy entry (e.g. every codesign-weak-* finding shares ref="codesign").
//
// Why, Fix, and SeeAlso are markdown source. They render through
// glamour for terminal output.
type RemedyEntry struct {
	Ref     string
	Title   string
	Why     string
	Fix     string
	SeeAlso string
}

// RenderOptions controls remedy output. Threaded down from the CLI so
// the package has no global state.
type RenderOptions struct {
	NoColor bool // true → render to plain ASCII (no escape codes)
	Width   int  // 0 → autodetect from TTY, fallback 80
}

// Remedies is the curated remedy catalog. Order is meaningful for the
// "print all" playbook — group by area (system → tcc → charon).
var Remedies = []RemedyEntry{
	{
		Ref:   "sip",
		Title: "System Integrity Protection (SIP)",
		Why: `SIP is macOS's kernel-level barrier against root-equivalent code modifying system files, attaching debuggers to signed binaries, or loading unsigned kexts. Charon's threat model assumes SIP is on (assumption 3 in **docs/threat-model.md**). Without SIP, an attacker with sudo can attach lldb to a running charon, read decrypted secrets straight from process memory, replace charon's binary on disk, or load a malicious dylib — defeating every layer below it.`,
		Fix: "**Verify**:\n" +
			"```bash\n" +
			"csrutil status   # expect: System Integrity Protection status: enabled.\n" +
			"```\n\n" +
			"**If disabled** — reboot into Recovery and re-enable:\n\n" +
			"1. Apple Silicon: hold the power button until \"Loading startup options\" appears.\n" +
			"   Intel: hold ⌘-R during boot.\n" +
			"2. Utilities → Terminal\n" +
			"3. Enable SIP:\n" +
			"```bash\n" +
			"csrutil enable\n" +
			"```\n" +
			"4. Reboot.\n\n" +
			"**If \"Custom Configuration\"**: `csrutil status` lists which subsystem is relaxed. Common case is dev work that ran `csrutil enable --without debug` (or similar) and forgot to undo. Re-enable fully when finished.",
		SeeAlso: "`docs/threat-model.md` → **Adversary C** (Local root / SIP-disabled).",
	},
	{
		Ref:   "sudo",
		Title: "Cached sudo credentials in this shell",
		Why:   `**sudo** caches authentication per-tty for ~5 minutes by default. Any subprocess in that tty — including an agent you launch — can call ` + "`sudo -n <anything>`" + ` and succeed without prompting. The cache is per-tty, not per-process, so the agent doesn't have to be a descendant of the original sudo command. Footgun: you ` + "`sudo make install`" + `, then in the same window run ` + "`agent-cli`" + ` — that agent now has unattended sudo for the next few minutes.`,
		Fix: "**Immediate** — invalidate the cached credential in this tty:\n" +
			"```bash\n" +
			"sudo -k\n" +
			"```\n\n" +
			"**Habit**: launch agent shells from a freshly opened terminal window where you haven't sudo'd.\n\n" +
			"**Stricter default** — disable caching entirely. Edit `/etc/sudoers` via `sudo visudo` and add:\n" +
			"```\n" +
			"Defaults timestamp_timeout=0\n" +
			"```\n" +
			"Every sudo will now prompt — annoying for repeat work but unambiguous.",
	},
	{
		Ref:   "launchd",
		Title: "Third-party launchd plists",
		Why: `Plists in ` + "`~/Library/LaunchAgents`" + ` (user-scope) and ` + "`/Library/LaunchAgents`" + `, ` + "`/Library/LaunchDaemons`" + ` (system-scope) make their owners auto-start at login or boot. A compromised tool can install one to persist across reboots; most users never audit. Charon's own ` + "`com.charon.proxy.plist`" + ` shows up here, which is expected — the audit can't tell yours from someone else's, so it lists everything non-Apple/Homebrew/Docker for you to review.`,
		Fix: "**Inspect** each plist's `ProgramArguments` / `Program` key:\n" +
			"```bash\n" +
			"defaults read ~/Library/LaunchAgents/<name>.plist\n" +
			"# or for binary plists:\n" +
			"/usr/libexec/PlistBuddy -c 'Print' ~/Library/LaunchAgents/<name>.plist\n" +
			"```\n\n" +
			"**Remove** what you don't recognize:\n" +
			"```bash\n" +
			"launchctl bootout gui/$(id -u) ~/Library/LaunchAgents/<name>.plist\n" +
			"rm ~/Library/LaunchAgents/<name>.plist\n" +
			"```\n\n" +
			"For system-scope plists, prepend `sudo` and use `launchctl bootout system/<label>`.\n\n" +
			"**Recognized noise** on a charon dev box:\n\n" +
			"- `com.charon.proxy.plist` — charon's own service\n" +
			"- `com.google.GoogleUpdater.wake.plist` — Chrome/Drive auto-updater\n",
		SeeAlso: "`docs/threat-model.md` → **A7** (persistence beachhead).",
	},
	{
		Ref:   "codesign",
		Title: "Terminal/IDE ships hardened-runtime-weakening entitlements",
		Why: `Apple's hardened runtime is a per-app opt-in that blocks ` + "`DYLD_INSERT_LIBRARIES`" + `, requires entitlements for debugger attach, and blocks library hijacking. Apps that need to load user code (shells reading dotfiles, IDEs loading plugins, debugger frontends) sometimes ship with weakening entitlements:

- ` + "`com.apple.security.cs.allow-dyld-environment-variables`" + `
- ` + "`com.apple.security.cs.disable-library-validation`" + `
- ` + "`com.apple.security.cs.allow-unsigned-executable-memory`" + `
- ` + "`com.apple.security.cs.allow-jit`" + `

When charon runs alongside such a terminal/IDE, **A5-class injection** becomes viable: an agent can load a dylib into the parent process's address space, satisfy charon's DR by association, and read keychain entries silently.`,
		Fix: "You can't fix this without repackaging the third-party app. Practical mitigations:\n\n" +
			"**Inspect** any app yourself:\n" +
			"```bash\n" +
			"codesign -d --entitlements - --xml /Applications/<App>.app\n" +
			"```\n\n" +
			"**Prefer a stricter terminal** for agentic work. Apple Terminal.app and iTerm2 generally use hardened runtime without weakening entitlements.\n\n" +
			"**Compartmentalize**: if a particular IDE needs the entitlements for a plugin you use, run that IDE for non-credential work and use a stricter terminal when launching agents that talk to charon.",
		SeeAlso: "`docs/threat-model.md` → **A5** (in-process injection).",
	},

	// --- TCC family (M4 will fill these in for real findings) ---

	{
		Ref:   "tcc-fda",
		Title: "Terminal/IDE has Full Disk Access",
		Why: `TCC permissions inherit from the launching process. If Terminal.app has Full Disk Access, every shell command spawned from Terminal — including the AI agent — has FDA. With FDA the agent can read ` + "`~/Library/Keychains/login.keychain-db`" + ` raw, attempt offline brute-force of the master key, and access adjacent secrets (Mail, Messages, Safari cookies, ` + "`~/.ssh`" + `, Notes). Charon's M4 keychain ACL still gates the Security-framework API path, but the broader process boundary collapses.

This is the **single most damaging TCC grant** for agentic workflows.`,
		Fix: "**Open the pane** directly:\n" +
			"```bash\n" +
			"open \"x-apple.systempreferences:com.apple.preference.security?Privacy_AllFiles\"\n" +
			"```\n" +
			"Or: System Settings → Privacy & Security → Full Disk Access.\n\n" +
			"**Toggle off** every terminal/editor/IDE listed. Keep FDA only for tools that genuinely need it (Time Machine helpers, Arq, Backblaze, etc.).\n\n" +
			"**Nuclear reset** — clears every app's FDA:\n" +
			"```bash\n" +
			"tccutil reset SystemPolicyAllFiles\n" +
			"```\n" +
			"Apps that legitimately need FDA will re-prompt next use; everything else stays revoked. After two weeks of normal use you'll have a minimal set.",
		SeeAlso: "`docs/threat-model.md` → **Adversary B** (TCC-grants), **B1**.",
	},
	{
		Ref:   "tcc-a11y",
		Title: "Terminal/IDE has Accessibility",
		Why: `Arguably **worse than FDA** for charon's threat model — and almost no one audits it. A process with Accessibility can synthesize keystrokes and mouse clicks. If an agent triggers a keychain Allow/Deny dialog, the agent (running inside an Accessibility-granted terminal) can click "Allow" itself, defeating the M4 ACL boundary entirely.

The whole point of layer 3 (Allow/Deny prompt) is that a human looks at it; Accessibility lets the attacker **be** the human.`,
		Fix: "**Open the pane**:\n" +
			"```bash\n" +
			"open \"x-apple.systempreferences:com.apple.preference.security?Privacy_Accessibility\"\n" +
			"```\n\n" +
			"**Toggle off** every terminal, editor, and IDE.\n\n" +
			"Window managers and input remappers (Rectangle, Hammerspoon, BetterTouchTool, Karabiner) legitimately need Accessibility — that's fine, since those tools don't run shells.\n\n" +
			"**Reset all**:\n" +
			"```bash\n" +
			"tccutil reset Accessibility\n" +
			"```\n" +
			"Then re-grant only to dedicated automation apps.",
	},
	{
		Ref:   "tcc-screen",
		Title: "Terminal/IDE has Screen Recording",
		Why:   `A process with Screen Recording can capture whatever charon's TUI or audit log prints — including, during debugging or normal operation, token prefixes, account headers, scope strings. Less catastrophic than FDA or Accessibility but a steady leak channel for sensitive output that the user assumed was ephemeral.`,
		Fix: "**Open the pane**:\n" +
			"```bash\n" +
			"open \"x-apple.systempreferences:com.apple.preference.security?Privacy_ScreenCapture\"\n" +
			"```\n\n" +
			"Toggle off terminals/IDEs unless you have an active reason for them to record screen content (e.g., demo recording, screencasting).\n\n" +
			"**Reset**:\n" +
			"```bash\n" +
			"tccutil reset ScreenCapture\n" +
			"```",
	},
	{
		Ref:   "tcc-events",
		Title: "Terminal/IDE has Automation/AppleEvents grants to credential apps",
		Why:   `**AppleEvents** lets app A drive app B's UI. If your terminal has Automation rights to control Keychain Access, 1Password, Bitwarden, or Mail, an agent running inside that terminal can script those apps to extract secrets via their UI — bypassing direct keychain ACLs entirely. The credential app sees the requests as legitimate user-initiated actions because they come from a process the user previously authorized.`,
		Fix: "**Open the pane**:\n" +
			"```bash\n" +
			"open \"x-apple.systempreferences:com.apple.preference.security?Privacy_Automation\"\n" +
			"```\n\n" +
			"This pane is hierarchical: each entry shows app A → controllable apps. Pay particular attention to terminal/IDE entries that can drive Keychain Access, password managers, or Mail. Toggle those off.\n\n" +
			"**Reset all** AppleEvents grants:\n" +
			"```bash\n" +
			"tccutil reset AppleEvents\n" +
			"```",
	},

	// --- Charon-specific (M5 fills the wiring; this prose is stable) ---

	{
		Ref:   "charon-signing-acl",
		Title: "Charon signing key has populated trusted-applications list",
		Why: `The ` + "`Charon Self-Signed`" + ` private key in your login keychain should have an **empty trusted-applications list** — every use should prompt Allow/Deny. If ` + "`/usr/bin/codesign`" + ` is in the list, codesign can sign arbitrary binaries with charon's identity without prompting, defeating defense layer 5 (**A10** in the threat model). An attacker who shells out to ` + "`codesign --sign \"Charon Self-Signed\" /tmp/agent-impostor`" + ` then gets a Mach-O whose DR matches charon's M4 ACL predicate.

The bootstrap script intentionally omits ` + "`-T /usr/bin/codesign`" + `. The list typically gets polluted by clicking **"Always Allow"** on a codesign prompt during a previous ` + "`make install`" + ` — the warning in the bootstrap output and README exists for exactly this reason.`,
		Fix: "**Inspect**:\n\n" +
			"1. Open Keychain Access:\n" +
			"```bash\n" +
			"open /System/Applications/Utilities/Keychain\\ Access.app\n" +
			"```\n" +
			"2. Find the identity (`Charon Self-Signed` or `Developer ID Application: ...`). For Dev ID, click the disclosure triangle (▶) to expand the cert and reveal the matching private key beneath it (Apple labels it with the CSR's Common Name, often just your name).\n" +
			"3. Right-click the **private key** → Get Info → **Access Control** tab.\n\n" +
			"Expected: \"Confirm before allowing access\" selected, lower list **empty** (Charon Self-Signed) or **Apple defaults only** (Dev ID).\n\n" +
			"### Classifying entries\n\n" +
			"**✓ Benign — Apple defaults from key generation. Safe to leave or remove for strict hygiene.**\n\n" +
			"- `Certificate Assistant` — the app that created the key (CSR generator). May appear twice (one ACL slot per key operation).\n" +
			"- `racoon` — deprecated Apple IPsec daemon, vestigial. The binary may not even exist on macOS 13+.\n" +
			"- `com.apple.ServerManagerDaemon` — vestigial macOS Server daemon. Apple killed macOS Server in 2022.\n" +
			"- `SecurityAgent` — Apple's auth dialog presenter.\n" +
			"- `Keychain Access` — Apple's keychain UI itself.\n\n" +
			"**✗ Catastrophic — REMOVE IMMEDIATELY.**\n\n" +
			"- `/usr/bin/codesign` — any process can sign a Mach-O satisfying charon's M4 ACL DR. This is the A10 case.\n" +
			"- `/usr/bin/security` — any process can read keychain entries by shelling out to the security CLI.\n" +
			"- Anything **outside** `/System/Library/`, `/usr/sbin/`, or `/System/Applications/Utilities/` — these paths are Apple system tools; anything else is suspicious.\n\n" +
			"### Acting on it\n\n" +
			"**For the catastrophic case** (codesign / security in the list): regenerate the identity to be safe.\n\n" +
			"```bash\n" +
			"make signing-identity   # bootstrap a fresh self-signed cert\n" +
			"make install            # re-sign + re-create keychain entries\n" +
			"```\n" +
			"Old charon entries (signed by the previous cert) become unreadable. Recovery: revoke + re-auth your OAuth accounts.\n\n" +
			"**For the benign Apple defaults**: pick strict or pragmatic. Strict = highlight each row and click `−`, then Save Changes; list goes empty, audit passes. Pragmatic = leave them; the Important finding stays until #12A names them and auto-classifies.\n\n" +
			"**Going forward**: during every `make install`, click **Allow** (single-use), never **Always Allow** — the latter is what would add codesign to the trust list and flip benign → catastrophic.",
		SeeAlso: "`docs/threat-model.md` → **A10** (signing key abuse via codesign).",
	},
	{
		Ref:   "charon-binary",
		Title: "Installed nous CLI codesign attestation",
		Why: `The installed nous binary at ` + "`~/.local/bin/nous`" + ` is what reads and writes your OAuth tokens and CA private key (nous embeds the credential proxy + vault since nous#20 retired the separate ` + "`charon`" + ` binary). Its codesign properties matter for two reasons:

1. **Identifier + signer**: must match what the keychain ACL expects (` + "`identifier \"com.charon.cli\"`" + ` plus the signer's anchor / leaf hash). A binary with a different identifier or signed by an unauthorized identity won't be able to read the entries silently — and it shouldn't be at this path either.
2. **Hardened runtime**: blocks DYLD_INSERT_LIBRARIES injection, debugger-attach without entitlement, unsigned dylib loading. Without it, a hostile process running as your user can inject code into a running nous and read decrypted tokens from memory.

The audit checks both via ` + "`codesign -dvv`" + ` on the installed binary. Findings:

- ` + "`charon-binary-not-installed`" + ` — Info, just means you haven't run ` + "`make nous-install`" + `.
- ` + "`charon-binary-unsigned`" + ` — Critical. Anyone could replace the binary; no signature to verify.
- ` + "`charon-binary-wrong-identifier`" + ` — Critical. Either misconfigured or impostor.
- ` + "`charon-binary-not-hardened`" + ` — Important. Lacks hardened runtime, A5 mitigation missing.`,
		Fix: "**The fix for almost every variant** is the same: re-run `make nous-install` from the nous repo. That signs with the auto-detected identity, sets `Identifier=com.charon.cli`, and enables `--options runtime`.\n\n" +
			"```bash\n" +
			"cd /path/to/nous\n" +
			"make nous-install\n" +
			"# Click Allow on the keychain dialog (single-use, never Always Allow)\n" +
			"```\n\n" +
			"After re-signing, restart the service so it picks up the new binary:\n" +
			"```bash\n" +
			"launchctl kickstart -k gui/$(id -u)/com.42shots.nous\n" +
			"# or:\n" +
			"nous service uninstall && nous service install\n" +
			"```\n\n" +
			"**If `charon-binary-wrong-identifier` shows an unexpected name** — for example `Identifier=com.attacker.foo` — that's an active compromise. Treat the machine as suspect, revoke OAuth tokens at the provider, and inspect what other binaries in `~/.local/bin/` may have been replaced.\n\n" +
			"**Verify the result**:\n" +
			"```bash\n" +
			"codesign -dvv ~/.local/bin/nous 2>&1 | grep -E 'Identifier|Authority|flags'\n" +
			"```\n" +
			"Expected:\n" +
			"```\n" +
			"Identifier=com.charon.cli\n" +
			"Authority=Developer ID Application: <Name> (<TEAMID>)   # or Charon Self-Signed\n" +
			"CodeDirectory v=... flags=0x10000(runtime) ...\n" +
			"```",
		SeeAlso: "`docs/threat-model.md` → Defense layers 4 & 5; **A5** (in-process injection); **A10** (signing-key abuse).",
	},
	{
		Ref:   "filevault",
		Title: "FileVault enabled (disk encryption at rest)",
		Why: `Charon's M4 keychain ACL gates **live API access** to your tokens — but it doesn't encrypt the on-disk keychain database itself. An attacker with physical access to your Mac (stolen laptop, border seizure, "evil maid" attack, repair shop with bad faith) can:

1. Boot from another Mac with the disk attached as external storage.
2. Read ` + "`~/Library/Keychains/login.keychain-db`" + ` directly — it's just a file.
3. Attempt offline brute-force of the keychain master key.

FileVault closes this path by encrypting the entire boot volume with a key derived from your account password. Without the password (or the recovery key), the disk's bytes are unreadable noise. This is the primary defense for **adversary C1** (stolen device / unencrypted backup) in the threat model.`,
		Fix: "**Enable FileVault**:\n\n" +
			"1. System Settings → Privacy & Security → FileVault\n" +
			"2. Click **Turn On**, authenticate with your account password.\n" +
			"3. Choose recovery option:\n" +
			"   - **iCloud account** — Apple stores your recovery key (convenient; tied to Apple ID security).\n" +
			"   - **Local recovery key** — printed for you; **store it somewhere safe** (1Password, paper in a safe, etc.). Without it, password loss = data loss.\n" +
			"4. Initial encryption runs in the background; you can keep using the Mac. Takes minutes to hours depending on disk size.\n\n" +
			"**Verify** at any time:\n" +
			"```bash\n" +
			"fdesetup status\n" +
			"# Expected: FileVault is On.\n" +
			"```\n\n" +
			"**Don't forget Time Machine**: Time Machine backups inherit encryption only if the destination volume is encrypted. For external drives: Disk Utility → select volume → Erase as APFS Encrypted. For network destinations: configure encrypted backups in System Settings → Time Machine → Options → \"Encrypt backups\".",
		SeeAlso: "`docs/threat-model.md` → **Adversary C1** (stolen device / unencrypted backup).",
	},
	{
		Ref:   "charon-entries-acl",
		Title: "Charon keychain entry has missing/weak ACL or extra trusted apps",
		Why: `Each entry in the ` + "`charon`" + ` keychain namespace should have a ` + "`SecAccess`" + ` whose trusted-applications list contains exactly one entry: charon itself. An entry without that ACL — or with extra trusted apps beyond charon — opens the M4 boundary that's supposed to keep your tokens private from foreign readers.

**Three failure modes the audit distinguishes**:

- ` + "`charon-entries-acl-missing-*`" + ` — entry has no SecAccess at all. Critical. Any process can read via ` + "`security find-generic-password`" + `. Cause: stale ` + "`charon serve`" + ` daemon wrote it before M4 landed (or after a regression).
- ` + "`charon-entries-acl-extra-*`" + ` (catastrophic detail) — Critical. Extra trusted app is ` + "`/usr/bin/codesign`" + ` or ` + "`/usr/bin/security`" + ` — silent reads possible by any process. Same kind of issue as the signing-key A10 case but per-entry.
- ` + "`charon-entries-acl-extra-*`" + ` (unrecognized detail) — Important. Extra trusted app is something the classifier doesn't recognize. Could be benign (a tool you intentionally Always-Allowed) or hostile (an attacker added itself); user judgment.`,
		Fix: "### For `charon-entries-acl-missing-*`\n\n" +
			"Stop any stale `charon serve` instances and re-write the affected entries:\n\n" +
			"```bash\n" +
			"launchctl bootout gui/$(id -u)/com.charon.proxy   # if launched via launchd\n" +
			"pkill -f \"charon serve\"\n" +
			"```\n\n" +
			"For OAuth tokens, the safest reset is **revoke + re-auth** the affected account — drops the old entry and creates a fresh one with a proper ACL. For `_ca:cert` / `_ca:key`, deleting them and restarting `charon serve` regenerates them.\n\n" +
			"### For `charon-entries-acl-extra-*`\n\n" +
			"Open Keychain Access:\n" +
			"```bash\n" +
			"open /System/Applications/Utilities/Keychain\\ Access.app\n" +
			"```\n" +
			"Search for `charon` (the service name). Right-click the affected entry → Get Info → Access Control. Highlight every row that ISN'T charon and click `−` → Save Changes.\n\n" +
			"For the catastrophic case (`/usr/bin/codesign` or `/usr/bin/security` in the list): consider revoking + re-authing the affected OAuth account, since you don't know how long the silent-read window has been open.\n\n" +
			"### Verify\n\n" +
			"```bash\n" +
			"security find-generic-password -s charon -a <account> -g\n" +
			"```\n" +
			"Healthy output includes an `Access:` block listing exactly one trusted application: charon.",
		SeeAlso: "`docs/threat-model.md` → **Defense layer 3** (Keychain ACL); related: `charon-signing-acl` for the same pattern on the signing key.",
	},
}

var remedyByRef = func() map[string]*RemedyEntry {
	m := make(map[string]*RemedyEntry, len(Remedies))
	for i := range Remedies {
		m[Remedies[i].Ref] = &Remedies[i]
	}
	return m
}()

// LookupRemedy returns the RemedyEntry for a given Ref, or nil.
func LookupRemedy(ref string) *RemedyEntry {
	return remedyByRef[ref]
}

// AllRemedyRefs returns the canonical refs in curated order.
func AllRemedyRefs() []string {
	out := make([]string, len(Remedies))
	for i, r := range Remedies {
		out[i] = r.Ref
	}
	return out
}

// PrintRemedy renders one entry through glamour. Title becomes a top-
// level markdown heading.
func PrintRemedy(w io.Writer, e *RemedyEntry, opts RenderOptions) {
	md := buildEntryMarkdown(e, "# "+e.Title+" `"+e.Ref+"`")
	fmt.Fprint(w, render(md, opts))
}

// PrintAllRemedies renders the playbook: a top-level title + total
// count, then every entry as an ## section with a [N/M] position
// prefix so a long scroll keeps the reader oriented.
func PrintAllRemedies(w io.Writer, opts RenderOptions) {
	total := len(Remedies)
	var b strings.Builder
	fmt.Fprintf(&b, "# Charon Security remedy playbook\n\n%d entries.\n\n", total)
	for i := range Remedies {
		entry := &Remedies[i]
		header := fmt.Sprintf("## [%d/%d] %s `%s`", i+1, total, entry.Title, entry.Ref)
		b.WriteString(buildEntryMarkdown(entry, header))
	}
	fmt.Fprint(w, render(b.String(), opts))
}

// buildEntryMarkdown assembles one entry's markdown source. Caller
// supplies the heading line so PrintRemedy and PrintAllRemedies can
// pick `#` vs `##` levels.
func buildEntryMarkdown(e *RemedyEntry, heading string) string {
	var b strings.Builder
	b.WriteString(heading)
	b.WriteString("\n\n### Why\n\n")
	b.WriteString(e.Why)
	b.WriteString("\n\n### Fix\n\n")
	b.WriteString(e.Fix)
	if e.SeeAlso != "" {
		b.WriteString("\n\n### See also\n\n")
		b.WriteString(e.SeeAlso)
	}
	b.WriteString("\n\n---\n\n")
	return b.String()
}

// render runs markdown through glamour, falling back to the source
// markdown on any error so the user always sees something readable.
func render(md string, opts RenderOptions) string {
	style := glamour.WithAutoStyle()
	if opts.NoColor {
		style = glamour.WithStandardStyle("ascii")
	}
	width := opts.Width
	if width == 0 {
		width = detectWidth()
	}
	r, err := glamour.NewTermRenderer(style, glamour.WithWordWrap(width))
	if err != nil {
		return md
	}
	out, err := r.Render(md)
	if err != nil {
		return md
	}
	return out
}

// detectWidth probes stdout for a terminal width, capping at 100 so
// long-form prose stays readable even on ultra-wide terminals.
func detectWidth() int {
	const (
		fallback = 80
		cap      = 100
	)
	if w, _, err := term.GetSize(int(os.Stdout.Fd())); err == nil && w > 0 {
		if w > cap {
			return cap
		}
		return w
	}
	return fallback
}

// PrintUnknownRef prints a friendly error listing valid refs. Plain
// text (not markdown) so it's never misrendered.
func PrintUnknownRef(w io.Writer, ref string) {
	fmt.Fprintf(w, "Unknown remedy ref: %q\n\nKnown refs:\n", ref)
	refs := AllRemedyRefs()
	sort.Strings(refs)
	for _, r := range refs {
		fmt.Fprintf(w, "  %s\n", r)
	}
}
