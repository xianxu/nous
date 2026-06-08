package charoncli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/xianxu/nous/lib/provider/oauth"
	"github.com/xianxu/nous/lib/provider/providers/gcp"
	"github.com/xianxu/nous/lib/provider/vault"
)

// GcpCmd groups Google Cloud project management subcommands. The
// flow is OAuth-token-driven: every subcommand needs an existing
// google:<account> credential with cloud-platform granted.
func GcpCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "gcp",
		Short: "Manage Google Cloud projects for Gemini access",
	}
	cmd.AddCommand(gcpSetupCmd())
	return cmd
}

func gcpSetupCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "setup <account>",
		Short: "Pick or create a GCP project and store its metadata for Gemini access",
		Long: `Interactive flow: list the user's GCP projects, let them pick or
create one, enable Vertex / AI Studio / API Keys APIs, check
billing, pick a Vertex region, and persist the result onto the
account's existing google:<account> credential as a sidecar.

Prerequisites: the account must already be authenticated via
'charon auth' and have the cloud-platform scope granted.

Example:
  charon gcp setup xianxu@gmail.com`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			account := args[0]
			return runGCPSetup(cmd.Context(), cmd.InOrStdin(), cmd.OutOrStdout(), newVault(), account, resolveAddr(cmd))
		},
	}
}

// runGCPSetup is the CLI entry: builds the production gcp.Client
// and a stdin Picker, then defers to executeGCPSetup. Production
// path lives here; tests target executeGCPSetup directly.
func runGCPSetup(ctx context.Context, in io.Reader, out io.Writer, v vault.Store, account string, addr string) error {
	if ctx == nil {
		ctx = context.Background()
	}

	cred, err := v.Get("google", account)
	if err != nil {
		return fmt.Errorf("load credential for google/%s: %w (run 'charon auth' first)", account, err)
	}
	if !hasCloudPlatformScope(cred) {
		return fmt.Errorf("account %s does not have the cloud-platform scope. Grant it via 'charon auth' first.", account)
	}

	gp, err := oauth.NewGoogleProvider()
	if err != nil {
		return fmt.Errorf("init google oauth provider: %w", err)
	}
	tokens := tokenSupplierFromVault(v, gp, "google", account)
	client := gcp.New(tokens)

	if err := executeGCPSetup(ctx, v, account, client, newStdinPicker(in, out), out); err != nil {
		return err
	}
	// Proxy cache invalidation: the credential we just persisted
	// has new GCP/AIStudio sidecars; a still-running proxy may have
	// cached tokens that predate the change. Best-effort flush
	// (silent if proxy isn't running).
	notifyProxyCacheClear(addr)
	return nil
}

// executeGCPSetup runs the orchestrator and persists the result.
// Pure modulo the vault and the picker.
//
// On success also auto-mints an AI Studio API key under the chosen
// project — but only if cred.AIStudio is empty (one-key-per-account
// per #14 design; user re-running setup keeps their existing key).
// Mint failure is non-fatal: project setup is still useful for
// Vertex even if AI Studio mint fails (e.g. apikeys API quota,
// transient network).
func executeGCPSetup(
	ctx context.Context,
	v vault.Store,
	account string,
	client *gcp.Client,
	picker gcp.Picker,
	out io.Writer,
) error {
	res, err := gcp.Setup(ctx, client, picker)
	if err != nil {
		return fmt.Errorf("gcp setup: %w", err)
	}
	cred, err := v.Get("google", account)
	if err != nil {
		return fmt.Errorf("re-load credential to persist GCP data: %w", err)
	}
	cred.GCP = &vault.GCPData{
		ProjectID:       res.Project.ProjectID,
		ProjectName:     res.Project.Name,
		Parent:          parentToVault(res.Project.Parent),
		VertexRegion:    res.Region,
		CreatedByCharon: res.CreatedNew,
		BillingEnabled:  res.BillingEnabled,
		UpdatedAt:       time.Now().UTC(),
	}
	mintedKey, mintErr := maybeMintAIStudio(ctx, client, picker, cred, res.Project.ProjectID)
	if mintedKey != nil {
		cred.AIStudio = mintedKey
	}
	if err := v.Set(cred); err != nil {
		return fmt.Errorf("persist credential: %w", err)
	}
	fmt.Fprintf(out, "\n✓ Stored project %s (%s) on google:%s with region %s.\n",
		res.Project.ProjectID, res.Project.Name, account, res.Region)
	if !res.BillingEnabled {
		fmt.Fprintln(out, "  Reminder: billing is not linked. Vertex calls will fail until you link a billing account.")
	}
	switch {
	case cred.AIStudio != nil && mintedKey != nil:
		fmt.Fprintf(out, "  Minted AI Studio key %s (charon-aistudio).\n", mintedKey.UID)
	case cred.AIStudio != nil:
		fmt.Fprintf(out, "  AI Studio key already configured (uid: %s) — kept as-is.\n", cred.AIStudio.UID)
	case mintErr != nil:
		fmt.Fprintf(out, "  AI Studio key mint failed (%v); Vertex still works. Re-run 'charon auth' to retry.\n", mintErr)
	}
	return nil
}

// maybeMintAIStudio mints a new AI Studio key under projectID iff the
// credential doesn't already have one. Returns (key, nil) on
// successful mint, (nil, nil) on skip, or (nil, err) on mint failure.
func maybeMintAIStudio(ctx context.Context, client *gcp.Client, picker gcp.Picker, cred *vault.Credential, projectID string) (*vault.AIStudioData, error) {
	if cred.AIStudio != nil {
		return nil, nil
	}
	picker.Notify("Minting AI Studio API key under %s (restricted to generativelanguage.googleapis.com)...", projectID)
	key, err := gcp.MintAIStudio(ctx, client, projectID)
	if err != nil {
		return nil, err
	}
	return &vault.AIStudioData{
		Name:        key.Name,
		UID:         key.UID,
		DisplayName: key.DisplayName,
		KeyMaterial: key.KeyString,
		ProjectID:   projectID,
		CreatedAt:   time.Now().UTC(),
	}, nil
}

func hasCloudPlatformScope(c *vault.Credential) bool {
	const want = "https://www.googleapis.com/auth/cloud-platform"
	for _, s := range c.Scopes {
		if s == want {
			return true
		}
	}
	return false
}

func parentToVault(p *gcp.Parent) *vault.GCPParent {
	if p == nil {
		return nil
	}
	return &vault.GCPParent{Type: p.Type, ID: p.ID}
}

// tokenSupplierFromVault returns a TokenSupplier that reads the
// credential from the vault on each call, refreshes it when the
// access token has expired, and persists the rotated credential
// before returning the access token.
//
// Takes the oauth.Provider port (not the concrete *GoogleProvider) so tests can
// inject oauth.NewFake — this is the seam that makes charon's GCP/token path
// run hermetically (nous#44).
func tokenSupplierFromVault(v vault.Store, gp oauth.Provider, provider, account string) gcp.TokenSupplier {
	return func(ctx context.Context) (string, error) {
		cred, err := v.Get(provider, account)
		if err != nil {
			return "", fmt.Errorf("vault.Get: %w", err)
		}
		if !cred.IsExpired() {
			return cred.AccessToken, nil
		}
		fresh, err := gp.Refresh(cred)
		if err != nil {
			return "", fmt.Errorf("oauth refresh: %w", err)
		}
		if err := v.Set(fresh); err != nil {
			return "", fmt.Errorf("persist refreshed credential: %w", err)
		}
		return fresh.AccessToken, nil
	}
}

// stdinPicker implements gcp.Picker over a Reader (for prompts) and
// Writer (for both prompts and Notify output). Used by `charon gcp
// setup` and exercised end-to-end by tests via in-memory buffers.
type stdinPicker struct {
	in     *bufio.Reader
	out    io.Writer
}

func newStdinPicker(in io.Reader, out io.Writer) *stdinPicker {
	return &stdinPicker{
		in:  bufio.NewReader(in),
		out: out,
	}
}

func (p *stdinPicker) Notify(format string, args ...any) {
	fmt.Fprintf(p.out, "  "+format+"\n", args...)
}

func (p *stdinPicker) PickProject(ctx context.Context, existing []gcp.Project) (gcp.Choice, error) {
	if len(existing) == 0 {
		fmt.Fprintln(p.out, "\nYou have no Google Cloud projects yet.")
	} else {
		fmt.Fprintln(p.out, "\nYour Google Cloud projects:")
		for i, prj := range existing {
			fmt.Fprintf(p.out, "  [%d] %-30s  %s\n", i+1, prj.ProjectID, prj.Name)
		}
	}
	fmt.Fprintln(p.out, "  [n] Create a new project")
	fmt.Fprint(p.out, "Pick: ")

	line, err := p.readLine()
	if err != nil {
		return gcp.Choice{}, err
	}
	line = strings.TrimSpace(line)

	if line == "n" || line == "N" {
		fmt.Fprint(p.out, "Display name for the new project (e.g. \"Charon Gemini\"): ")
		name, err := p.readLine()
		if err != nil {
			return gcp.Choice{}, err
		}
		name = strings.TrimSpace(name)
		if name == "" {
			return gcp.Choice{}, errors.New("project name is required")
		}
		return gcp.Choice{NewName: name}, nil
	}

	idx, err := strconv.Atoi(line)
	if err != nil || idx < 1 || idx > len(existing) {
		return gcp.Choice{}, fmt.Errorf("invalid choice %q (pick 1-%d or n)", line, len(existing))
	}
	prj := existing[idx-1]
	return gcp.Choice{Existing: &prj}, nil
}

// HandleBillingBlock prompts the user to link billing in another
// tab and either retry the check or cancel. Loops until the user
// presses 'c' to continue or 'esc' to cancel.
func (p *stdinPicker) HandleBillingBlock(ctx context.Context, projectID, fixURL string, recheck func(context.Context) (bool, error)) (bool, error) {
	fmt.Fprintln(p.out, "")
	fmt.Fprintln(p.out, "  Billing setup required")
	fmt.Fprintln(p.out, "  ─────────────────────")
	fmt.Fprintf(p.out, "  Project %s has no billing account linked.\n", projectID)
	fmt.Fprintln(p.out, "  Vertex calls will return BILLING_DISABLED.")
	fmt.Fprintln(p.out, "  AI Studio's free-tier quota is 0 for charon-created projects.")
	fmt.Fprintln(p.out, "")
	fmt.Fprintln(p.out, "  Open this URL in a browser, link a billing account, then come back:")
	fmt.Fprintf(p.out, "    %s\n", fixURL)
	fmt.Fprintln(p.out, "")
	for {
		fmt.Fprint(p.out, "  [r] re-check    [c] continue without billing    [esc/^C] cancel : ")
		line, err := p.readLine()
		if err != nil {
			return false, err
		}
		switch strings.TrimSpace(line) {
		case "r", "R":
			enabled, err := recheck(ctx)
			if err != nil {
				fmt.Fprintf(p.out, "  re-check failed: %v\n", err)
				continue
			}
			if enabled {
				fmt.Fprintln(p.out, "  ✓ Billing now linked. Continuing.")
				return true, nil
			}
			fmt.Fprintln(p.out, "  Still not linked. Did you save the change in Cloud Console?")
		case "c", "C":
			return true, nil
		case "esc", "":
			return false, nil
		default:
			fmt.Fprintln(p.out, "  Unknown choice; pick r / c / esc.")
		}
	}
}

func (p *stdinPicker) PickRegion(ctx context.Context) (string, error) {
	fmt.Fprintln(p.out, "\nVertex AI region:")
	for i, r := range gcp.SupportedVertexRegions {
		marker := " "
		if r == gcp.DefaultVertexRegion {
			marker = "*"
		}
		fmt.Fprintf(p.out, "  [%d]%s %s\n", i+1, marker, r)
	}
	fmt.Fprintf(p.out, "Pick (or press Enter for %s): ", gcp.DefaultVertexRegion)

	line, err := p.readLine()
	if err != nil {
		return "", err
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return gcp.DefaultVertexRegion, nil
	}
	idx, err := strconv.Atoi(line)
	if err != nil || idx < 1 || idx > len(gcp.SupportedVertexRegions) {
		// Allow free-form region input — Vertex supports more
		// regions than the picker lists, and a typed value lets
		// the user override.
		return line, nil
	}
	return gcp.SupportedVertexRegions[idx-1], nil
}

func (p *stdinPicker) readLine() (string, error) {
	line, err := p.in.ReadString('\n')
	if err != nil && err != io.EOF {
		return "", err
	}
	if err == io.EOF && line == "" {
		// Treat closed stdin like cancellation.
		return "", errors.New("input closed")
	}
	return line, nil
}

