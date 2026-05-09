package gcp

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
)

// RequiredServices is the canonical list of GCP APIs charon enables
// on a Gemini-hosting project. Vertex AI (aiplatform), AI Studio
// data plane (generativelanguage), and the API Keys API used by M4
// to mint AI Studio keys.
//
// Order is stable so test assertions and audit logs render
// deterministically.
var RequiredServices = []string{
	"aiplatform.googleapis.com",
	"apikeys.googleapis.com",
	"generativelanguage.googleapis.com",
}

// DefaultVertexRegion is the region charon picks when the user has
// no preference. us-central1 has the broadest model availability and
// is in the lowest pricing tier.
const DefaultVertexRegion = "us-central1"

// SupportedVertexRegions is the canonical region list charon offers.
// Not exhaustive — the API supports more — but covers the common
// choices. Tests and pickers iterate this in stable order.
var SupportedVertexRegions = []string{
	"us-central1",
	"us-east1",
	"us-east4",
	"us-west1",
	"europe-west1",
	"europe-west4",
	"asia-northeast1",
	"asia-southeast1",
}

// Picker is the user-facing interaction during Setup. The
// orchestrator delegates every UI decision to a Picker so the same
// flow can drive a CLI prompt, a bubbletea TUI, or a test stub.
type Picker interface {
	// PickProject is called with the list of ACTIVE projects the
	// authenticated user has access to. Implementations return a
	// Choice indicating an existing project or the user's intent
	// to create a new one.
	PickProject(ctx context.Context, existing []Project) (Choice, error)

	// PickRegion picks a Vertex region. Implementations may show
	// SupportedVertexRegions or accept arbitrary input; the
	// orchestrator only requires a non-empty string.
	PickRegion(ctx context.Context) (string, error)

	// Notify is called for status messages the user should see
	// during setup (e.g. "creating project, this may take 30s").
	// Implementations may discard, print, or render in a TUI panel.
	Notify(format string, args ...any)

	// HandleBillingBlock is called when the project's billing is
	// not linked. The picker should:
	//   - surface fixURL prominently to the user,
	//   - allow the user to call recheck() one or more times after
	//     linking billing in another tab,
	//   - return proceed=true if the user is OK going forward
	//     (either billing is now enabled, or they explicitly chose
	//     to skip the check).
	// proceed=false aborts the whole Setup flow.
	HandleBillingBlock(ctx context.Context, projectID, fixURL string, recheck func(context.Context) (bool, error)) (proceed bool, err error)
}

// BillingFixURL builds the canonical Cloud Console URL for linking
// a billing account to a project. Used by the orchestrator when
// surfacing billing-blocked state, and by manifest output when
// telling agents how to instruct the user.
func BillingFixURL(projectID string) string {
	return fmt.Sprintf("https://console.cloud.google.com/billing/linkedaccount?project=%s", projectID)
}

// Choice carries the user's PickProject decision back to the
// orchestrator. Exactly one of Existing / NewName is meaningful.
type Choice struct {
	// Existing is non-nil when the user picked from the list.
	Existing *Project

	// NewName is non-empty when the user wants a fresh project.
	// Required if Existing is nil; freeform display name (1-30
	// chars). The orchestrator generates a project ID — callers
	// don't pre-fill NewID.
	NewName string

	// NewID is optionally pre-supplied by the picker. When empty,
	// the orchestrator generates a `charon-gemini-<random>` id.
	// Useful for tests that want deterministic IDs.
	NewID string
}

// Result is what Setup returns on success. Callers (CLI, TUI) map
// this onto vault.GCPData when persisting.
type Result struct {
	// Project is the chosen or newly-created project. Always
	// populated on success (filled from the create response /
	// list entry).
	Project Project

	// Region is the Vertex region the user picked.
	Region string

	// EnabledServices is the set of API IDs charon ensured were
	// enabled. May exceed what was newly enabled — the call is
	// idempotent and the response doesn't distinguish.
	EnabledServices []string

	// BillingEnabled is the project's billing state at the end of
	// setup. False is non-fatal (AI Studio still works); the
	// picker is notified so the user can act.
	BillingEnabled bool

	// CreatedNew is true when Setup ran projects.create rather
	// than picking an existing project. Drives the
	// CreatedByCharon flag on the persisted vault.GCPData.
	CreatedNew bool
}

// Setup runs the full M3 flow: list projects → ask picker → maybe
// create → enable required APIs → check billing → ask picker for
// region → return Result. Notifications are emitted via picker so
// the caller controls UX entirely.
//
// Errors abort the flow; partial progress (e.g. project created but
// API enable failed) is surfaced to the picker via Notify so the
// user knows the upstream state, but Setup returns the underlying
// error and the caller decides whether to persist.
func Setup(ctx context.Context, c *Client, picker Picker) (*Result, error) {
	picker.Notify("Listing your Google Cloud projects...")
	projects, err := c.ListProjects(ctx)
	if err != nil {
		return nil, fmt.Errorf("list projects: %w", err)
	}

	choice, err := picker.PickProject(ctx, projects)
	if err != nil {
		return nil, fmt.Errorf("pick project: %w", err)
	}

	res := &Result{}

	switch {
	case choice.Existing != nil:
		res.Project = *choice.Existing
	case choice.NewName != "":
		id := choice.NewID
		if id == "" {
			id = GenerateProjectID()
		}
		picker.Notify("Creating project %q (id: %s) — this typically takes 5-30 seconds.", choice.NewName, id)
		op, err := c.CreateProject(ctx, id, choice.NewName, nil)
		if err != nil {
			return nil, fmt.Errorf("create project: %w", err)
		}
		if !op.Done {
			if err := c.WaitOperation(ctx, op.Name, 0); err != nil {
				return nil, fmt.Errorf("wait create operation: %w", err)
			}
		}
		res.Project = Project{
			ProjectID:      id,
			Name:           choice.NewName,
			LifecycleState: "ACTIVE",
		}
		res.CreatedNew = true
	default:
		return nil, fmt.Errorf("picker returned empty choice (no Existing, no NewName)")
	}

	picker.Notify("Enabling required APIs on %s: %s", res.Project.ProjectID, strings.Join(RequiredServices, ", "))
	if err := c.BatchEnableServices(ctx, res.Project.ProjectID, RequiredServices); err != nil {
		return nil, fmt.Errorf("enable services: %w", err)
	}
	res.EnabledServices = append([]string(nil), RequiredServices...)

	billing, err := c.GetBillingInfo(ctx, res.Project.ProjectID)
	if err != nil {
		// Non-fatal: billing detection itself failed (permission
		// denied, network). Surface and proceed without blocking —
		// charon doesn't know the actual billing state.
		picker.Notify("Couldn't read billing info (%v) — proceeding anyway.", err)
	} else if !billing.BillingEnabled {
		// Charon-created projects get 0 free-tier AI Studio quota
		// and Vertex outright rejects calls without billing. Block
		// here so the user fixes it now rather than discovering
		// the failure at first call.
		fixURL := BillingFixURL(res.Project.ProjectID)
		picker.Notify("Billing not linked on %s. Both Vertex and AI Studio (charon-created projects) require billing.", res.Project.ProjectID)
		recheck := func(ctx context.Context) (bool, error) {
			info, err := c.GetBillingInfo(ctx, res.Project.ProjectID)
			if err != nil {
				return false, err
			}
			return info.BillingEnabled, nil
		}
		proceed, err := picker.HandleBillingBlock(ctx, res.Project.ProjectID, fixURL, recheck)
		if err != nil {
			return nil, fmt.Errorf("billing block: %w", err)
		}
		if !proceed {
			return nil, fmt.Errorf("setup cancelled: billing not linked on %s — re-run after linking at %s", res.Project.ProjectID, fixURL)
		}
		// Picker returned proceed=true. Re-stamp billing state in
		// case the user actually linked while we waited.
		if info, err := c.GetBillingInfo(ctx, res.Project.ProjectID); err == nil {
			res.BillingEnabled = info.BillingEnabled
		}
	} else {
		res.BillingEnabled = true
	}

	region, err := picker.PickRegion(ctx)
	if err != nil {
		return nil, fmt.Errorf("pick region: %w", err)
	}
	if strings.TrimSpace(region) == "" {
		return nil, fmt.Errorf("picker returned empty region")
	}
	res.Region = region

	return res, nil
}

// AIStudioDisplayName is the label charon stamps on every AI Studio
// key it mints — chosen so the user can identify charon-created keys
// in Cloud Console's API Keys list.
const AIStudioDisplayName = "charon-aistudio"

// AIStudioServiceTarget restricts minted keys to AI Studio's data
// plane only. Defense in depth: a leaked key cannot be repurposed
// for other Google APIs.
const AIStudioServiceTarget = "generativelanguage.googleapis.com"

// MintAIStudio creates a new AI Studio API key under the project,
// restricted to generativelanguage.googleapis.com only. Returns the
// minted key including the secret KeyString — captured here on the
// create response, not refetchable from upstream.
//
// Idempotency is the caller's job: this function always mints a
// new key on call. The CLI and TUI orchestrators check whether
// cred.AIStudio is already set before calling.
func MintAIStudio(ctx context.Context, c *Client, projectID string) (*APIKey, error) {
	op, err := c.CreateAPIKey(ctx, projectID, AIStudioDisplayName, []string{AIStudioServiceTarget})
	if err != nil {
		return nil, fmt.Errorf("create api key: %w", err)
	}
	if !op.Done {
		op, err = c.WaitAPIKeyOperation(ctx, op.Name)
		if err != nil {
			return nil, fmt.Errorf("wait api key op: %w", err)
		}
	}
	key, err := ExtractAPIKey(op)
	if err != nil {
		return nil, fmt.Errorf("extract minted key: %w", err)
	}
	if key.KeyString == "" {
		return nil, fmt.Errorf("minted key has empty KeyString — upstream bug or stale operation response")
	}
	return key, nil
}

// GenerateProjectID returns a globally-unique project id starting
// with `charon-gemini-` so the user can identify charon-created
// projects in Cloud Console. 8 hex chars = 32 bits of entropy,
// adequate for the per-user namespace where collisions are
// vanishingly unlikely. Exported for the TUI's parallel orchestrator.
func GenerateProjectID() string {
	var buf [4]byte
	if _, err := rand.Read(buf[:]); err != nil {
		// crypto/rand failing on Darwin/Linux is "machine is
		// extremely broken" — there's no graceful fallback that
		// preserves global uniqueness, so panic.
		panic(fmt.Sprintf("crypto/rand: %v", err))
	}
	return "charon-gemini-" + hex.EncodeToString(buf[:])
}
