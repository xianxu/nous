package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/xianxu/nous/internal/charon/providers/gcp"
)

// fakeGCPClient drives gcpSetupModel through its states without
// touching the network. Each method captures inputs and returns
// pre-canned responses; tests assemble per-scenario.
type fakeGCPClient struct {
	listResult    []gcp.Project
	listErr       error
	createOp      *gcp.Operation
	createErr     error
	waitErr       error
	enableErr     error
	billing       *gcp.BillingInfo
	billingErr    error

	// AI Studio key mint / revoke
	createAPIKeyOp  *gcp.Operation
	createAPIKeyErr error
	waitAPIKeyOp    *gcp.Operation
	waitAPIKeyErr   error
	deleteAPIKeyOp  *gcp.Operation
	deleteAPIKeyErr error

	listCalls           int
	createCalls         int
	waitCalls           int
	enableCalls         int
	billingCalls        int
	createAPIKeyCalls   int
	waitAPIKeyCalls     int
	deleteAPIKeyCalls   int
	lastDeletedAPIKey   string
}

func (f *fakeGCPClient) ListProjects(ctx context.Context) ([]gcp.Project, error) {
	f.listCalls++
	return f.listResult, f.listErr
}
func (f *fakeGCPClient) CreateProject(ctx context.Context, projectID, displayName string, parent *gcp.Parent) (*gcp.Operation, error) {
	f.createCalls++
	if f.createErr != nil {
		return nil, f.createErr
	}
	return f.createOp, nil
}
func (f *fakeGCPClient) WaitOperation(ctx context.Context, opName string, pollInterval time.Duration) error {
	f.waitCalls++
	return f.waitErr
}
func (f *fakeGCPClient) BatchEnableServices(ctx context.Context, projectID string, services []string) error {
	f.enableCalls++
	return f.enableErr
}
func (f *fakeGCPClient) GetBillingInfo(ctx context.Context, projectID string) (*gcp.BillingInfo, error) {
	f.billingCalls++
	return f.billing, f.billingErr
}
func (f *fakeGCPClient) CreateAPIKey(ctx context.Context, projectID, displayName string, restrictedTo []string) (*gcp.Operation, error) {
	f.createAPIKeyCalls++
	if f.createAPIKeyErr != nil {
		return nil, f.createAPIKeyErr
	}
	return f.createAPIKeyOp, nil
}
func (f *fakeGCPClient) WaitAPIKeyOperation(ctx context.Context, opName string) (*gcp.Operation, error) {
	f.waitAPIKeyCalls++
	if f.waitAPIKeyErr != nil {
		return nil, f.waitAPIKeyErr
	}
	return f.waitAPIKeyOp, nil
}
func (f *fakeGCPClient) DeleteAPIKey(ctx context.Context, name string) (*gcp.Operation, error) {
	f.deleteAPIKeyCalls++
	f.lastDeletedAPIKey = name
	if f.deleteAPIKeyErr != nil {
		return nil, f.deleteAPIKeyErr
	}
	if f.deleteAPIKeyOp != nil {
		return f.deleteAPIKeyOp, nil
	}
	return &gcp.Operation{Done: true}, nil
}

// runCmd executes a tea.Cmd synchronously and returns the resulting
// message. Bubbletea normally schedules cmds on the program loop; in
// tests we just want the message.
func runCmd(cmd tea.Cmd) tea.Msg {
	if cmd == nil {
		return nil
	}
	return cmd()
}

func TestGCPSetup_PickExistingFlowEmitsDoneMsg(t *testing.T) {
	fake := &fakeGCPClient{
		listResult: []gcp.Project{
			{ProjectID: "alpha", Name: "Alpha", LifecycleState: "ACTIVE"},
		},
		billing: &gcp.BillingInfo{BillingEnabled: true},
		createAPIKeyOp: &gcp.Operation{
			Name: "operations/k.x",
			Done: true,
			Response: map[string]any{
				"name":      "projects/alpha/locations/global/keys/abc",
				"uid":       "abc",
				"keyString": "AIzaSy_FAKE",
			},
		},
	}
	// hasAIStudioKey=true skips mint and emits done directly (simpler
	// happy-path assertion). The mint-flow test is separate.
	m := newGCPSetupModel(fake, "user@gmail.com", nil, true)
	if msg := runCmd(m.initCmd()); msg != nil {
		var cmd tea.Cmd
		m, cmd = m.Update(msg)
		_ = cmd
	}
	if m.state != gcpStatePickingProject {
		t.Fatalf("after list, state = %d, want pickingProject", m.state)
	}

	// Press enter to pick alpha.
	m, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.state != gcpStateEnabling {
		t.Fatalf("after pick, state = %d, want enabling", m.state)
	}
	// Run the enable cmd, feed result back.
	m, _ = m.Update(runCmd(cmd))
	if m.state != gcpStateBillingCheck {
		t.Fatalf("after enable, state = %d, want billingCheck", m.state)
	}

	// Run the billing cmd, feed result back. Need the billing cmd —
	// it was returned by the gcpServicesEnabledMsg handler.
	// Re-issue from the model.
	billingMsg := m.checkBillingCmd()()
	m, _ = m.Update(billingMsg)
	if m.state != gcpStatePickingRegion {
		t.Fatalf("after billing, state = %d, want pickingRegion", m.state)
	}

	// Press enter on default region (cursor already on us-central1).
	_, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	doneMsg, ok := runCmd(cmd).(gcpSetupDoneMsg)
	if !ok {
		t.Fatalf("expected gcpSetupDoneMsg, got %T", runCmd(cmd))
	}
	if doneMsg.account != "user@gmail.com" {
		t.Errorf("account = %q", doneMsg.account)
	}
	if doneMsg.projectID != "alpha" {
		t.Errorf("projectID = %q", doneMsg.projectID)
	}
	if doneMsg.region != gcp.DefaultVertexRegion {
		t.Errorf("region = %q, want %s", doneMsg.region, gcp.DefaultVertexRegion)
	}
	if doneMsg.createdNew {
		t.Error("createdNew should be false for existing pick")
	}
	if !doneMsg.billing {
		t.Error("billing should be true")
	}
}

func TestGCPSetup_ListErrorTransitionsToError(t *testing.T) {
	fake := &fakeGCPClient{listErr: errors.New("403 forbidden")}
	m := newGCPSetupModel(fake, "user@gmail.com", nil, false)
	m, _ = m.Update(runCmd(m.initCmd()))
	if m.state != gcpStateError {
		t.Fatalf("state = %d, want error", m.state)
	}
	if !strings.Contains(m.err.Error(), "403") {
		t.Errorf("err missing context: %v", m.err)
	}
}

func TestGCPSetup_CreateNewProjectFlow(t *testing.T) {
	fake := &fakeGCPClient{
		listResult: nil,
		createOp:   &gcp.Operation{Name: "operations/x", Done: true},
		billing:    &gcp.BillingInfo{BillingEnabled: false},
	}
	m := newGCPSetupModel(fake, "user@gmail.com", nil, false)
	m, _ = m.Update(runCmd(m.initCmd()))

	// Move cursor to the synthetic "+ new project" row (index =
	// len(projects); 0 with an empty list).
	for m.projectCur < len(m.projects) {
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	}
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.state != gcpStateEditingNewName {
		t.Fatalf("state = %d, want editingNewName", m.state)
	}

	// Type a name. textinput accepts runes via tea.KeyMsg{Type: KeyRunes}.
	m.nameInput.SetValue("My Charon")

	// Press enter to create.
	var cmd tea.Cmd
	m, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.state != gcpStateCreatingProject {
		t.Fatalf("state = %d, want creatingProject", m.state)
	}
	// Run the create cmd. createOp.Done=true means no Wait call.
	m, _ = m.Update(runCmd(cmd))
	if m.state != gcpStateEnabling {
		t.Fatalf("after create, state = %d, want enabling", m.state)
	}
	if !m.createdNew {
		t.Error("createdNew should be true after projects.create")
	}
	if fake.waitCalls != 0 {
		t.Error("Wait should not be called when op is already Done")
	}
}

func TestGCPSetup_BillingReadFailureNonFatal(t *testing.T) {
	fake := &fakeGCPClient{
		listResult: []gcp.Project{{ProjectID: "p", Name: "P", LifecycleState: "ACTIVE"}},
		billingErr: errors.New("permission denied"),
	}
	m := newGCPSetupModel(fake, "u@gmail.com", nil, false)
	m, _ = m.Update(runCmd(m.initCmd()))
	m, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // pick project
	m, _ = m.Update(runCmd(cmd))                       // enable result
	m, _ = m.Update(m.checkBillingCmd()())             // billing result
	if m.state != gcpStatePickingRegion {
		t.Fatalf("state = %d, want pickingRegion (billing failure must be non-fatal)", m.state)
	}
	if !strings.Contains(m.notice, "Couldn't read billing info") {
		t.Errorf("expected billing notice, got: %q", m.notice)
	}
}

func TestGCPSetup_EscFromPickerCancels(t *testing.T) {
	fake := &fakeGCPClient{listResult: []gcp.Project{
		{ProjectID: "p", Name: "P", LifecycleState: "ACTIVE"},
	}}
	m := newGCPSetupModel(fake, "u@gmail.com", nil, false)
	m, _ = m.Update(runCmd(m.initCmd()))
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if _, ok := runCmd(cmd).(gcpSetupCancelMsg); !ok {
		t.Errorf("expected gcpSetupCancelMsg from esc, got %T", runCmd(cmd))
	}
}

// ctrl+c must quit the program from any sub-state — every TUI
// screen honors this contract, and the GCP setup screen got it
// wrong on first cut.
func TestGCPSetup_CtrlCQuitsFromAnyState(t *testing.T) {
	cases := []struct {
		name  string
		setup func() gcpSetupModel
	}{
		{
			name: "loading",
			setup: func() gcpSetupModel {
				return newGCPSetupModel(&fakeGCPClient{}, "u@gmail.com", nil, false)
			},
		},
		{
			name: "pickingProject",
			setup: func() gcpSetupModel {
				m := newGCPSetupModel(&fakeGCPClient{
					listResult: []gcp.Project{{ProjectID: "p", Name: "P", LifecycleState: "ACTIVE"}},
				}, "u@gmail.com", nil, false)
				m, _ = m.Update(runCmd(m.initCmd()))
				return m
			},
		},
		{
			name: "editingNewName",
			setup: func() gcpSetupModel {
				m := newGCPSetupModel(&fakeGCPClient{}, "u@gmail.com", nil, false)
				m, _ = m.Update(runCmd(m.initCmd()))
				m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // synthetic + new project row
				return m
			},
		},
		{
			name: "pickingRegion",
			setup: func() gcpSetupModel {
				m := newGCPSetupModel(&fakeGCPClient{
					listResult: []gcp.Project{{ProjectID: "p", Name: "P", LifecycleState: "ACTIVE"}},
					billing:    &gcp.BillingInfo{BillingEnabled: true},
				}, "u@gmail.com", nil, false)
				m, _ = m.Update(runCmd(m.initCmd()))
				m, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
				m, _ = m.Update(runCmd(cmd))
				m, _ = m.Update(m.checkBillingCmd()())
				return m
			},
		},
		{
			name: "billingBlocked",
			setup: func() gcpSetupModel {
				m := newGCPSetupModel(&fakeGCPClient{
					listResult: []gcp.Project{{ProjectID: "p", Name: "P", LifecycleState: "ACTIVE"}},
					billing:    &gcp.BillingInfo{BillingEnabled: false},
				}, "u@gmail.com", nil, false)
				m, _ = m.Update(runCmd(m.initCmd()))
				m, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
				m, _ = m.Update(runCmd(cmd))
				m, _ = m.Update(m.checkBillingCmd()())
				if m.state != gcpStateBillingBlocked {
					panic(fmt.Sprintf("setup wrong: state = %d, want billingBlocked", m.state))
				}
				return m
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := tc.setup()
			_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
			if cmd == nil {
				t.Fatal("expected a cmd from ctrl+c")
			}
			// tea.Quit is a sentinel func; we can't compare directly,
			// but invoking it returns a tea.QuitMsg.
			if _, ok := cmd().(tea.QuitMsg); !ok {
				t.Errorf("expected tea.QuitMsg from ctrl+c, got %T", cmd())
			}
		})
	}
}

// Billing disabled blocks the flow at gcpStateBillingBlocked
// instead of falling through to region pick. The user can:
//   - press 'r' to re-check (transitions to billingCheck and
//     re-issues the cmd; if billing is now enabled, advance);
//   - press 'c' to continue without billing;
//   - press 'esc' to cancel the whole flow.
func TestGCPSetup_BillingDisabledTransitionsToBlocked(t *testing.T) {
	fake := &fakeGCPClient{
		listResult: []gcp.Project{{ProjectID: "p", Name: "P", LifecycleState: "ACTIVE"}},
		billing:    &gcp.BillingInfo{BillingEnabled: false},
	}
	m := newGCPSetupModel(fake, "u@gmail.com", nil, false)
	m, _ = m.Update(runCmd(m.initCmd()))
	m, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // pick project → enabling
	m, _ = m.Update(runCmd(cmd))                       // enable result → billingCheck
	m, _ = m.Update(m.checkBillingCmd()())             // billing result → blocked
	if m.state != gcpStateBillingBlocked {
		t.Fatalf("state = %d, want billingBlocked (billing returned false)", m.state)
	}
	if !strings.Contains(m.View(), "Billing setup required") {
		t.Errorf("view should announce blocked state: %s", m.View())
	}
}

// Pressing 'r' on the blocked screen re-checks billing. If billing
// is now linked, the flow advances to region pick.
func TestGCPSetup_BillingBlocked_RetryAfterLink_Advances(t *testing.T) {
	fake := &fakeGCPClient{
		listResult: []gcp.Project{{ProjectID: "p", Name: "P", LifecycleState: "ACTIVE"}},
		billing:    &gcp.BillingInfo{BillingEnabled: false},
	}
	m := newGCPSetupModel(fake, "u@gmail.com", nil, false)
	m, _ = m.Update(runCmd(m.initCmd()))
	m, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m, _ = m.Update(runCmd(cmd))
	m, _ = m.Update(m.checkBillingCmd()())
	if m.state != gcpStateBillingBlocked {
		t.Fatalf("setup wrong, state=%d", m.state)
	}

	// User links billing in another tab — flip the fake server's
	// billing response to true, then press 'r'.
	fake.billing.BillingEnabled = true
	m, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if m.state != gcpStateBillingCheck {
		t.Fatalf("after r, state = %d, want billingCheck", m.state)
	}
	m, _ = m.Update(runCmd(cmd)) // re-check fires checkBillingCmd
	if m.state != gcpStatePickingRegion {
		t.Fatalf("after re-check, state = %d, want pickingRegion (billing now enabled)", m.state)
	}
}

// Pressing 'c' on the blocked screen proceeds without billing —
// transitions to region pick with a warning notice.
func TestGCPSetup_BillingBlocked_ContinueWithoutBilling(t *testing.T) {
	fake := &fakeGCPClient{
		listResult: []gcp.Project{{ProjectID: "p", Name: "P", LifecycleState: "ACTIVE"}},
		billing:    &gcp.BillingInfo{BillingEnabled: false},
	}
	m := newGCPSetupModel(fake, "u@gmail.com", nil, false)
	m, _ = m.Update(runCmd(m.initCmd()))
	m, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m, _ = m.Update(runCmd(cmd))
	m, _ = m.Update(m.checkBillingCmd()())

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	if m.state != gcpStatePickingRegion {
		t.Fatalf("after c, state = %d, want pickingRegion", m.state)
	}
	if !strings.Contains(m.notice, "Proceeding without billing") {
		t.Errorf("expected proceeding-without-billing notice, got %q", m.notice)
	}
}

// Esc on the blocked screen cancels the whole setup flow.
func TestGCPSetup_BillingBlocked_EscCancels(t *testing.T) {
	fake := &fakeGCPClient{
		listResult: []gcp.Project{{ProjectID: "p", Name: "P", LifecycleState: "ACTIVE"}},
		billing:    &gcp.BillingInfo{BillingEnabled: false},
	}
	m := newGCPSetupModel(fake, "u@gmail.com", nil, false)
	m, _ = m.Update(runCmd(m.initCmd()))
	m, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m, _ = m.Update(runCmd(cmd))
	m, _ = m.Update(m.checkBillingCmd()())

	_, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if _, ok := runCmd(cmd).(gcpSetupCancelMsg); !ok {
		t.Errorf("expected gcpSetupCancelMsg from esc on blocked screen, got %T", runCmd(cmd))
	}
}

// Pinned project must appear in the displayed list even when
// projects.list hasn't returned it (Google's eventual consistency
// after a fresh create). Regression: previously the user had to
// restart `charon auth` to see a project they just created.
func TestGCPSetup_PinnedProjectShownWhenMissingFromList(t *testing.T) {
	fake := &fakeGCPClient{
		listResult: []gcp.Project{
			{ProjectID: "old-1", Name: "Old One", LifecycleState: "ACTIVE"},
		},
	}
	pinned := &gcp.Project{ProjectID: "fresh", Name: "Just Created", LifecycleState: "ACTIVE"}
	m := newGCPSetupModel(fake, "u@gmail.com", pinned, false)
	m, _ = m.Update(runCmd(m.initCmd()))

	if len(m.projects) != 2 {
		t.Fatalf("expected 2 projects (1 listed + 1 pinned), got %d: %v", len(m.projects), m.projects)
	}
	if m.projects[0].ProjectID != "fresh" {
		t.Errorf("pinned project should be first, got %v", m.projects)
	}
}

// Pinned project that's already in the list must not be duplicated
// — Google's list eventually catches up; the merge is a no-op once
// it does.
func TestGCPSetup_PinnedProjectNotDuplicatedWhenAlreadyListed(t *testing.T) {
	fake := &fakeGCPClient{
		listResult: []gcp.Project{
			{ProjectID: "alpha", Name: "Alpha", LifecycleState: "ACTIVE"},
			{ProjectID: "fresh", Name: "Just Created", LifecycleState: "ACTIVE"},
		},
	}
	pinned := &gcp.Project{ProjectID: "fresh", Name: "Just Created", LifecycleState: "ACTIVE"}
	m := newGCPSetupModel(fake, "u@gmail.com", pinned, false)
	m, _ = m.Update(runCmd(m.initCmd()))

	if len(m.projects) != 2 {
		t.Errorf("expected 2 projects (no duplicate), got %d: %v", len(m.projects), m.projects)
	}
}

// Region pick → mint flow: when no AI Studio key exists yet, the
// region pick transitions to gcpStateMintingAIStudio and the mint
// result is carried into gcpSetupDoneMsg.
func TestGCPSetup_MintsAIStudioWhenMissing(t *testing.T) {
	fake := &fakeGCPClient{
		listResult: []gcp.Project{{ProjectID: "p", Name: "P", LifecycleState: "ACTIVE"}},
		billing:    &gcp.BillingInfo{BillingEnabled: true},
		createAPIKeyOp: &gcp.Operation{
			Name: "operations/k.x",
			Done: true,
			Response: map[string]any{
				"name":      "projects/p/locations/global/keys/uid-1",
				"uid":       "uid-1",
				"keyString": "AIzaSy_FAKE",
			},
		},
	}
	m := newGCPSetupModel(fake, "u@gmail.com", nil, false)
	m, _ = m.Update(runCmd(m.initCmd()))
	m, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // pick project
	m, _ = m.Update(runCmd(cmd))                       // enable result
	m, _ = m.Update(m.checkBillingCmd()())             // billing result
	if m.state != gcpStatePickingRegion {
		t.Fatalf("state = %d, want pickingRegion", m.state)
	}
	// Press enter on default region — should kick off mint.
	m, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.state != gcpStateMintingAIStudio {
		t.Fatalf("state = %d, want mintingAIStudio", m.state)
	}
	// Run the mint cmd, feed result back. With createAPIKeyOp.Done=true
	// the wait call is skipped.
	m, cmd = m.Update(runCmd(cmd))
	doneMsg, ok := runCmd(cmd).(gcpSetupDoneMsg)
	if !ok {
		t.Fatalf("expected gcpSetupDoneMsg, got %T", runCmd(cmd))
	}
	if doneMsg.aiStudio == nil {
		t.Fatal("doneMsg.aiStudio should be populated")
	}
	if doneMsg.aiStudio.UID != "uid-1" {
		t.Errorf("UID = %q", doneMsg.aiStudio.UID)
	}
	if doneMsg.aiStudio.KeyString != "AIzaSy_FAKE" {
		t.Errorf("KeyString = %q", doneMsg.aiStudio.KeyString)
	}
	if fake.createAPIKeyCalls != 1 {
		t.Errorf("expected 1 CreateAPIKey call, got %d", fake.createAPIKeyCalls)
	}
}

// Mint failure is non-fatal: gcpSetupDoneMsg still emits, with
// aiStudio nil. The user keeps Vertex; AI Studio can be retried.
func TestGCPSetup_MintFailureIsNonFatal(t *testing.T) {
	fake := &fakeGCPClient{
		listResult:      []gcp.Project{{ProjectID: "p", Name: "P", LifecycleState: "ACTIVE"}},
		billing:         &gcp.BillingInfo{BillingEnabled: true},
		createAPIKeyErr: errors.New("403 forbidden"),
	}
	m := newGCPSetupModel(fake, "u@gmail.com", nil, false)
	m, _ = m.Update(runCmd(m.initCmd()))
	m, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m, _ = m.Update(runCmd(cmd))
	m, _ = m.Update(m.checkBillingCmd()())
	m, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // region → mint
	m, cmd = m.Update(runCmd(cmd))                    // mint result (error)
	doneMsg, ok := runCmd(cmd).(gcpSetupDoneMsg)
	if !ok {
		t.Fatalf("expected gcpSetupDoneMsg even on mint failure, got %T", runCmd(cmd))
	}
	if doneMsg.aiStudio != nil {
		t.Errorf("aiStudio should be nil on mint failure, got %+v", doneMsg.aiStudio)
	}
	if !strings.Contains(doneMsg.aiStudioErr, "403 forbidden") {
		t.Errorf("expected aiStudioErr to carry the upstream error, got %q", doneMsg.aiStudioErr)
	}
}

// hasAIStudioKey=true → mint state is skipped entirely.
func TestGCPSetup_SkipsMintWhenKeyAlreadyExists(t *testing.T) {
	fake := &fakeGCPClient{
		listResult: []gcp.Project{{ProjectID: "p", Name: "P", LifecycleState: "ACTIVE"}},
		billing:    &gcp.BillingInfo{BillingEnabled: true},
	}
	m := newGCPSetupModel(fake, "u@gmail.com", nil, true)
	m, _ = m.Update(runCmd(m.initCmd()))
	m, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m, _ = m.Update(runCmd(cmd))
	m, _ = m.Update(m.checkBillingCmd()())
	_, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // region pick
	doneMsg, ok := runCmd(cmd).(gcpSetupDoneMsg)
	if !ok {
		t.Fatalf("expected immediate done (no mint), got %T", runCmd(cmd))
	}
	if doneMsg.aiStudio != nil {
		t.Errorf("aiStudio should be nil when skipped, got %+v", doneMsg.aiStudio)
	}
	if fake.createAPIKeyCalls != 0 {
		t.Errorf("CreateAPIKey should not be called when hasAIStudioKey=true, got %d calls", fake.createAPIKeyCalls)
	}
}

func TestGCPSetup_RegionPickerNumericNav(t *testing.T) {
	fake := &fakeGCPClient{
		listResult: []gcp.Project{{ProjectID: "p", Name: "P", LifecycleState: "ACTIVE"}},
		billing:    &gcp.BillingInfo{BillingEnabled: true},
	}
	// hasAIStudioKey=true so region pick goes straight to done
	// without exercising the mint state machine.
	m := newGCPSetupModel(fake, "u@gmail.com", nil, true)
	m, _ = m.Update(runCmd(m.initCmd()))
	m, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m, _ = m.Update(runCmd(cmd))
	m, _ = m.Update(m.checkBillingCmd()())

	// Move down twice, then enter.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	_, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	doneMsg, ok := runCmd(cmd).(gcpSetupDoneMsg)
	if !ok {
		t.Fatalf("expected done msg, got %T", runCmd(cmd))
	}
	if doneMsg.region != gcp.SupportedVertexRegions[2] {
		t.Errorf("region = %q, want %q", doneMsg.region, gcp.SupportedVertexRegions[2])
	}
}
