package tui

import (
	"context"
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/xianxu/nous/internal/charon/providers"
	"github.com/xianxu/nous/internal/charon/vault"
	"github.com/xianxu/nous/internal/charon/vault/memory"
)

// typeMint sends each rune in s to the model as a single key press.
func typeMint(t *testing.T, m adminMintModel, s string) adminMintModel {
	t.Helper()
	for _, r := range s {
		updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = updated
	}
	return m
}

// newMintFixture preps a fake provider, an admin-key store with a key
// already configured (mint requires it), and an empty vault.
func newMintFixture(t *testing.T) (adminMintModel, *providers.Fake, *memory.Store) {
	t.Helper()
	v := memory.New()
	store := fakeAdminStore(t, "openai", true, "me@example.com")
	fake := providers.NewFake().WithName("openai")
	m, err := newAdminMintModel("openai", fake, store, v)
	if err != nil {
		t.Fatalf("newAdminMintModel: %v", err)
	}
	return m, fake, v
}

func TestMint_RequiresConfiguredAdminKey(t *testing.T) {
	v := memory.New()
	store := fakeAdminStore(t, "openai", false, "")
	fake := providers.NewFake().WithName("openai")
	_, err := newAdminMintModel("openai", fake, store, v)
	if err == nil {
		t.Error("newAdminMintModel should fail when admin key is not set")
	}
}

func TestMint_HappyPath_ExistingProject(t *testing.T) {
	m, fake, v := newMintFixture(t)
	fake.SeedProject("proj_existing", "existing-project")

	// Step 1: account name.
	if m.state != mintStateEditingAccount {
		t.Fatalf("initial state = %d, want editingAccount", m.state)
	}
	m = typeMint(t, m, "work-project")
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated
	if m.state != mintStateLoadingProjects {
		t.Fatalf("after account-enter: state = %d, want loadingProjects", m.state)
	}
	if cmd == nil {
		t.Fatal("account-enter should fire ListProjects")
	}

	// Drive ListProjects result.
	pl := cmd().(adminMintProjectsLoadedMsg)
	if pl.err != nil {
		t.Fatalf("ListProjects failed: %v", pl.err)
	}
	updated, _ = m.Update(pl)
	m = updated
	if m.state != mintStatePickingProject {
		t.Fatalf("after projects loaded: state = %d, want pickingProject", m.state)
	}
	if len(m.projects) != 1 {
		t.Fatalf("expected 1 project, got %d", len(m.projects))
	}

	// Pick the existing project (cursor at 0).
	updated, mintCmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated
	if m.state != mintStateMinting {
		t.Fatalf("after project-enter: state = %d, want minting", m.state)
	}

	// Drive MintKey result.
	mm := mintCmd().(adminMintMintedMsg)
	if mm.err != nil {
		t.Fatalf("MintKey failed: %v", mm.err)
	}
	updated, doneCmd := m.Update(mm)
	m = updated
	if doneCmd == nil {
		t.Fatal("on success, mint should emit done cmd")
	}
	if _, ok := doneCmd().(adminMintDoneMsg); !ok {
		t.Errorf("expected adminMintDoneMsg, got %T", doneCmd())
	}

	// Vault now has the credential.
	stored, err := v.Get("openai", "work-project")
	if err != nil {
		t.Fatalf("vault.Get: %v", err)
	}
	if stored.CredType() != vault.TypeAdminKey {
		t.Errorf("stored cred type = %q, want admin-key", stored.CredType())
	}
	if stored.AdminKey == nil {
		t.Fatal("stored cred missing AdminKey payload")
	}
	if stored.AdminKey.ProjectID != "proj_existing" {
		t.Errorf("ProjectID = %q, want proj_existing", stored.AdminKey.ProjectID)
	}
	if stored.AdminKey.KeyID == "" || stored.AdminKey.KeyMaterial == "" {
		t.Errorf("KeyID/KeyMaterial empty: %+v", stored.AdminKey)
	}
	if stored.AdminKey.OrgID != "org-test-001" {
		t.Errorf("OrgID = %q, want org-test-001 (inherited from admin meta)", stored.AdminKey.OrgID)
	}
}

func TestMint_HappyPath_CreateNewProject(t *testing.T) {
	m, _, v := newMintFixture(t)

	// Step 1: account name.
	m = typeMint(t, m, "work-project")
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated
	pl := cmd().(adminMintProjectsLoadedMsg)
	updated, _ = m.Update(pl)
	m = updated // pickingProject (empty list — only "+ create new" row)

	// Cursor lands on "+ create new" since len(projects)==0.
	if m.projectCur != 0 {
		t.Fatalf("cursor = %d on empty project list, want 0", m.projectCur)
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated
	if m.state != mintStateEditingProjectName {
		t.Fatalf("expected editingProjectName, got %d", m.state)
	}

	// Type a project name and submit.
	m = typeMint(t, m, "acme-prod")
	updated, createCmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated
	if m.state != mintStateCreatingProject {
		t.Fatalf("state = %d, want creatingProject", m.state)
	}
	pc := createCmd().(adminMintProjectCreatedMsg)
	if pc.err != nil {
		t.Fatalf("CreateProject failed: %v", pc.err)
	}
	updated, mintCmd := m.Update(pc)
	m = updated
	if m.state != mintStateMinting {
		t.Fatalf("after project created: state = %d, want minting", m.state)
	}
	mm := mintCmd().(adminMintMintedMsg)
	updated, _ = m.Update(mm)
	m = updated

	// Vault has the credential with the freshly-created project id.
	stored, err := v.Get("openai", "work-project")
	if err != nil {
		t.Fatalf("vault.Get: %v", err)
	}
	if stored.AdminKey.ProjectName != "acme-prod" {
		t.Errorf("ProjectName = %q, want acme-prod", stored.AdminKey.ProjectName)
	}
	if !strings.HasPrefix(stored.AdminKey.ProjectID, "proj_fake_") {
		t.Errorf("ProjectID = %q, want proj_fake_… prefix", stored.AdminKey.ProjectID)
	}
}

func TestMint_RejectsDuplicateAccount(t *testing.T) {
	m, _, v := newMintFixture(t)
	// Pre-existing credential under the same name.
	_ = v.Set(&vault.Credential{
		Type: vault.TypeAdminKey, Provider: "openai", Account: "work",
		AdminKey: &vault.AdminKeyData{OrgID: "org-test-001", ProjectID: "proj_x", KeyMaterial: "sk-x"},
	})

	m = typeMint(t, m, "work")
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated
	if m.state != mintStateError {
		t.Fatalf("duplicate account should land in mintStateError, got %d", m.state)
	}
	if !strings.Contains(m.err.Error(), "already exists") {
		t.Errorf("error should explain duplicate, got %v", m.err)
	}
}

func TestMint_AccountStep_EscCancels(t *testing.T) {
	m, _, _ := newMintFixture(t)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd == nil {
		t.Fatal("esc should emit cancel msg")
	}
	if _, ok := cmd().(adminMintCancelMsg); !ok {
		t.Errorf("expected adminMintCancelMsg, got %T", cmd())
	}
}

func TestMint_PickerStep_EscReturnsToAccount(t *testing.T) {
	m, _, _ := newMintFixture(t)
	m = typeMint(t, m, "work")
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated
	updated, _ = m.Update(cmd().(adminMintProjectsLoadedMsg))
	m = updated // pickingProject

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated
	if m.state != mintStateEditingAccount {
		t.Errorf("esc on picker should return to editingAccount, got %d", m.state)
	}
	// Account name is preserved across the round-trip.
	if strings.TrimSpace(m.accountInput.Value()) != "work" {
		t.Errorf("account input lost across esc: %q", m.accountInput.Value())
	}
}

func TestMint_ProjectNameStep_EscReturnsToPicker(t *testing.T) {
	m, _, _ := newMintFixture(t)
	m = typeMint(t, m, "work")
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated
	updated, _ = m.Update(cmd().(adminMintProjectsLoadedMsg))
	m = updated // pickingProject

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // pick "+ create new"
	m = updated
	m = typeMint(t, m, "wip-name")
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated
	if m.state != mintStatePickingProject {
		t.Errorf("esc on project-name should return to picker, got %d", m.state)
	}
	if m.projectNameInput.Value() != "" {
		t.Errorf("project name input not cleared on esc, got %q", m.projectNameInput.Value())
	}
}

func TestMint_ListProjectsError(t *testing.T) {
	m, fake, _ := newMintFixture(t)
	fake.ValidAdminKey = "different-key" // store has "sk-test-admin", so list fails

	m = typeMint(t, m, "work")
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated
	pl := cmd().(adminMintProjectsLoadedMsg)
	if pl.err == nil {
		t.Fatal("expected ListProjects to error")
	}
	updated, _ = m.Update(pl)
	m = updated
	if m.state != mintStateError {
		t.Errorf("list error should land in mintStateError, got %d", m.state)
	}
}

func TestMint_View_ContainsExpectedChrome(t *testing.T) {
	m, _, _ := newMintFixture(t)

	view := m.View()
	for _, want := range []string{appName() + " › OpenAI › new key", "Step 1/2", "Name>"} {
		if !strings.Contains(view, want) {
			t.Errorf("account-step view missing %q\n%s", want, view)
		}
	}
}

// The mint flow header is provider-agnostic ("new key"). Anthropic-
// specific verbiage ("Anthropic workspace") only appears in Step 2,
// where the user picks the upstream container — which is the place
// the local term genuinely matters.
func TestMint_AnthropicWordingInStep2(t *testing.T) {
	v := memory.New()
	store := fakeAdminStore(t, "anthropic", true, "me")
	fake := providers.NewFake().WithName("anthropic")
	m, err := newAdminMintModel("anthropic", fake, store, v)
	if err != nil {
		t.Fatalf("newAdminMintModel: %v", err)
	}

	// Step 1: header is provider-agnostic.
	step1 := m.View()
	if !strings.Contains(step1, appName()+" › Anthropic › new key") {
		t.Errorf("Step 1 header should be 'new key', got\n%s", step1)
	}

	// Drive to Step 2.
	m = typeMint(t, m, "test")
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated
	pl := cmd().(adminMintProjectsLoadedMsg)
	updated, _ = m.Update(pl)
	m = updated

	// Step 2 should reference 'Anthropic workspace' explicitly.
	step2 := m.View()
	if !strings.Contains(step2, "Anthropic workspace") {
		t.Errorf("Step 2 should reference 'Anthropic workspace', got\n%s", step2)
	}
	if !strings.Contains(step2, "+ create new workspace") {
		t.Errorf("Step 2 should offer 'create new workspace', got\n%s", step2)
	}
}

// failingSetVault wraps memory.Store but returns an error on Set.
// Used to drive the "minted upstream but vault.Set failed" partial-
// failure case in the mint flow.
type failingSetVault struct {
	*memory.Store
}

func (f *failingSetVault) Set(c *vault.Credential) error {
	return fmt.Errorf("simulated keychain failure")
}

// Chunk-2 review #4: when MintKey succeeds upstream but vault.Set
// fails, the mint flow's cancel msg carries a StatusNote that names
// the orphan key id so the user can revoke at the dashboard.
func TestMint_VaultSetFailure_CancelMsgCarriesOrphanNote(t *testing.T) {
	v := &failingSetVault{Store: memory.New()}
	store := fakeAdminStore(t, "openai", true, "me@example.com")
	fake := providers.NewFake().WithName("openai")
	m, err := newAdminMintModel("openai", fake, store, v)
	if err != nil {
		t.Fatalf("newAdminMintModel: %v", err)
	}

	// Drive: name → enter → projects load → pick + create new →
	// project name → create + mint → vault.Set fails → error state.
	m = typeMint(t, m, "ghost-key")
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated
	updated, _ = m.Update(cmd().(adminMintProjectsLoadedMsg))
	m = updated
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // pick + create new
	m = updated
	m = typeMint(t, m, "orphan-project")
	updated, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated
	updated, cmd = m.Update(cmd().(adminMintProjectCreatedMsg)) // creating → minting
	m = updated
	updated, _ = m.Update(cmd().(adminMintMintedMsg)) // minting → vault.Set fails → error state
	m = updated

	if m.state != mintStateError {
		t.Fatalf("expected mintStateError after vault.Set failure, got %d", m.state)
	}
	if !strings.Contains(m.err.Error(), "revoke") {
		t.Errorf("error should advise dashboard revoke, got %v", m.err)
	}
	if m.mintedKeyID == "" {
		t.Error("mintedKeyID should be tracked after MintKey succeeded")
	}
	if m.mintedKeyHasVault {
		t.Error("mintedKeyHasVault should be false after vault.Set failed")
	}

	// Any key dismisses the error and emits cancel msg with the orphan
	// note named.
	_, cancelCmd := m.Update(tea.KeyMsg{Runes: []rune{' '}, Type: tea.KeyRunes})
	if cancelCmd == nil {
		t.Fatal("error dismiss should emit cancel msg")
	}
	cancel, ok := cancelCmd().(adminMintCancelMsg)
	if !ok {
		t.Fatalf("expected adminMintCancelMsg, got %T", cancelCmd())
	}
	if !strings.Contains(cancel.StatusNote, m.mintedKeyID) {
		t.Errorf("StatusNote should name the orphan key id %q, got %q",
			m.mintedKeyID, cancel.StatusNote)
	}
	if !strings.Contains(strings.ToLower(cancel.StatusNote), "revoke") &&
		!strings.Contains(strings.ToLower(cancel.StatusNote), "dashboard") {
		t.Errorf("StatusNote should hint at provider dashboard cleanup, got %q", cancel.StatusNote)
	}
}

// Chunk-2 review #4: when CreateProject succeeds but MintKey fails,
// the cancel note describes the orphan project so the user can clean
// up or re-use.
func TestMint_MintFailure_AfterCreateProject_OrphanProjectInNote(t *testing.T) {
	v := memory.New()
	store := fakeAdminStore(t, "openai", true, "me@example.com")
	fake := providers.NewFake().WithName("openai")
	// Set ValidAdminKey so MintKey rejects (matches the seeded admin
	// key in store; we re-set to a different value so MintKey fails
	// while CreateProject — using the same fake — still works since
	// CreateProject also runs the same check). Hmm — Fake checks
	// ValidAdminKey on every op, so both fail. Use a different
	// approach: seed a project up front and skip CreateProject.
	//
	// Simpler: use a custom provider that lets CreateProject pass but
	// MintKey fail.
	custom := &mintFailureProvider{Fake: fake}

	m, err := newAdminMintModel("openai", custom, store, v)
	if err != nil {
		t.Fatalf("newAdminMintModel: %v", err)
	}
	m = typeMint(t, m, "test-key")
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated
	updated, _ = m.Update(cmd().(adminMintProjectsLoadedMsg))
	m = updated
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // + create new
	m = updated
	m = typeMint(t, m, "test-orphan")
	updated, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated
	updated, cmd = m.Update(cmd().(adminMintProjectCreatedMsg))
	m = updated
	updated, _ = m.Update(cmd().(adminMintMintedMsg))
	m = updated

	if m.state != mintStateError {
		t.Fatalf("expected mintStateError, got %d", m.state)
	}
	if m.createdProjectID == "" {
		t.Error("createdProjectID should be tracked after CreateProject succeeded")
	}
	if m.mintedKeyID != "" {
		t.Error("mintedKeyID should be empty after MintKey failed")
	}

	_, cancelCmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	cancel := cancelCmd().(adminMintCancelMsg)
	if !strings.Contains(cancel.StatusNote, m.createdProjectID) {
		t.Errorf("StatusNote should name the orphan project id, got %q", cancel.StatusNote)
	}
}

// mintFailureProvider lets CreateProject succeed but rejects MintKey.
type mintFailureProvider struct {
	*providers.Fake
}

func (m *mintFailureProvider) MintKey(ctx context.Context, adminKey, projectID, keyName string) (string, string, error) {
	return "", "", fmt.Errorf("simulated mint failure")
}

// End-to-end through the top-level model.
func TestModel_ProviderToMintFlow_PicksExisting(t *testing.T) {
	v := memory.New()
	store := fakeAdminStore(t, "openai", true, "me@example.com")
	fake := providers.NewFake().WithName("openai")
	fake.SeedProject("proj_seed", "seed-project")

	m := model{
		vault:          v,
		adminProviders: map[string]providers.Provider{"openai": fake},
		adminStores:    map[string]*providers.AdminKeyStore{"openai": store},
	}
	pp, _ := newProviderPickerModel(v, m.adminStores, nil)
	m.providerPicker = pp
	// M7 onboarding lands cursor on +add for empty vault; reset.
	m.providerPicker.cursor = 0
	m.current = screenProvider

	// → openai entity list.
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	mm := updated.(model)
	updated, cmd := mm.Update(tea.KeyMsg{Type: tea.KeyEnter})
	mm = updated.(model)
	updated, _ = mm.Update(cmd())
	mm = updated.(model)
	if mm.current != screenAdminKeyList {
		t.Fatalf("expected screenAdminKeyList, got %v", mm.current)
	}

	// Cursor on admin row (0). Move to "+ new project" (last row).
	for mm.adminList.cursor < len(mm.adminList.rows)-1 {
		updated, _ = mm.Update(tea.KeyMsg{Type: tea.KeyDown})
		mm = updated.(model)
	}

	// Enter on "+ new project" → mint flow opens.
	updated, cmd = mm.Update(tea.KeyMsg{Type: tea.KeyEnter})
	mm = updated.(model)
	if cmd == nil {
		t.Fatal("enter on +new project should emit a cmd")
	}
	updated, _ = mm.Update(cmd())
	mm = updated.(model)
	if mm.current != screenAdminMint {
		t.Fatalf("expected screenAdminMint, got %v", mm.current)
	}

	// Drive the mint flow: type account, advance.
	for _, r := range "work" {
		updated, _ = mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		mm = updated.(model)
	}
	updated, cmd = mm.Update(tea.KeyMsg{Type: tea.KeyEnter})
	mm = updated.(model)
	pl := cmd().(adminMintProjectsLoadedMsg)
	updated, _ = mm.Update(pl)
	mm = updated.(model)
	if mm.adminMint.state != mintStatePickingProject {
		t.Fatalf("expected pickingProject, got %d", mm.adminMint.state)
	}

	// Pick existing project (cursor at 0).
	updated, mintCmd := mm.Update(tea.KeyMsg{Type: tea.KeyEnter})
	mm = updated.(model)
	mm2 := mintCmd().(adminMintMintedMsg)
	updated, doneCmd := mm.Update(mm2)
	mm = updated.(model)
	updated, _ = mm.Update(doneCmd())
	mm = updated.(model)

	// Should land back at admin entity list, refreshed with the new
	// project row.
	if mm.current != screenAdminKeyList {
		t.Errorf("after mint done: current = %v, want screenAdminKeyList", mm.current)
	}
	// Vault has the new credential.
	if _, err := v.Get("openai", "work"); err != nil {
		t.Errorf("vault should have new credential after mint: %v", err)
	}
}
