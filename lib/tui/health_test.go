package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/xianxu/nous/lib/provider/vault"
)

// nous#15 M5: synthetic verification of the M2 health-surfacing
// path without hitting Google. Stubs AccountHealthChecker, asserts
// AnnotateHealth correctly stamps health onto picker items, asserts
// View() renders the "(needs reauth)" badge inline.

func TestPicker_AnnotateHealth_StampsBadgeOnUnhealthyAccount(t *testing.T) {
	v := setupVault(t,
		&vault.Credential{Provider: "google", Account: "healthy@example.com"},
		&vault.Credential{Provider: "google", Account: "broken@example.com"},
	)
	m, err := newPickerModel(v)
	if err != nil {
		t.Fatalf("newPickerModel: %v", err)
	}

	check := func(c *vault.Credential) AccountHealth {
		if c.Account == "broken@example.com" {
			return AccountHealthNeedsReauth
		}
		return AccountHealthHealthy
	}

	m.AnnotateHealth(v, check)

	// Find each account row and verify the health stamp.
	healthyFound := false
	brokenFound := false
	for _, item := range m.items {
		if item.isNew {
			continue
		}
		switch item.email {
		case "healthy@example.com":
			healthyFound = true
			if item.health != AccountHealthHealthy {
				t.Errorf("healthy account: got %q, want %q", item.health, AccountHealthHealthy)
			}
		case "broken@example.com":
			brokenFound = true
			if item.health != AccountHealthNeedsReauth {
				t.Errorf("broken account: got %q, want %q", item.health, AccountHealthNeedsReauth)
			}
		}
	}
	if !healthyFound || !brokenFound {
		t.Errorf("expected both accounts in picker; healthy=%v broken=%v", healthyFound, brokenFound)
	}
}

func TestPicker_RKey_DispatchesByHealth(t *testing.T) {
	// nous#15 polish: `R` is context-dependent. On a healthy account
	// it opens the revoke confirm modal; on a needs-reauth account it
	// emits reauthRequestedMsg.
	v := setupVault(t,
		&vault.Credential{Provider: "google", Account: "healthy@example.com"},
		&vault.Credential{Provider: "google", Account: "broken@example.com"},
	)
	check := func(c *vault.Credential) AccountHealth {
		if c.Account == "broken@example.com" {
			return AccountHealthNeedsReauth
		}
		return AccountHealthHealthy
	}

	// Healthy account → revoke confirm modal.
	m, _ := newPickerModel(v)
	m.AnnotateHealth(v, check)
	// Cursor 0 = "healthy@example.com" (sorted alphabetically before
	// "broken@example.com"? No — 'b' < 'h'. Find by email.)
	for i, it := range m.items {
		if it.email == "healthy@example.com" {
			m.cursor = i
		}
	}
	updated, cmd := m.Update(tea.KeyMsg{Runes: []rune{'R'}, Type: tea.KeyRunes})
	if updated.state != pickerStateRevokeConfirm {
		t.Errorf("R on healthy: state=%v want pickerStateRevokeConfirm", updated.state)
	}
	if cmd != nil {
		t.Errorf("R on healthy: expected nil cmd, got %T", cmd())
	}

	// Needs-reauth account → reauthRequestedMsg.
	m2, _ := newPickerModel(v)
	m2.AnnotateHealth(v, check)
	for i, it := range m2.items {
		if it.email == "broken@example.com" {
			m2.cursor = i
		}
	}
	_, cmd2 := m2.Update(tea.KeyMsg{Runes: []rune{'R'}, Type: tea.KeyRunes})
	if cmd2 == nil {
		t.Fatal("R on needs-reauth: expected reauthRequestedMsg cmd, got nil")
	}
	msg, ok := cmd2().(reauthRequestedMsg)
	if !ok {
		t.Fatalf("R on needs-reauth: expected reauthRequestedMsg, got %T", cmd2())
	}
	if msg.email != "broken@example.com" {
		t.Errorf("reauthRequestedMsg.email = %q, want broken@example.com", msg.email)
	}
}

func TestPicker_View_RendersNeedsReauthBadge(t *testing.T) {
	v := setupVault(t,
		&vault.Credential{Provider: "google", Account: "broken@example.com"},
	)
	m, err := newPickerModel(v)
	if err != nil {
		t.Fatalf("newPickerModel: %v", err)
	}
	check := func(c *vault.Credential) AccountHealth { return AccountHealthNeedsReauth }
	m.AnnotateHealth(v, check)

	view := m.View()
	if !strings.Contains(view, "broken@example.com") {
		t.Errorf("view missing the account email: %s", view)
	}
	if !strings.Contains(view, "(needs reauth)") {
		t.Errorf("view missing '(needs reauth)' badge:\n%s", view)
	}
}

func TestPicker_AnnotateHealth_NilCheckerIsNoOp(t *testing.T) {
	v := setupVault(t,
		&vault.Credential{Provider: "google", Account: "any@example.com"},
	)
	m, err := newPickerModel(v)
	if err != nil {
		t.Fatalf("newPickerModel: %v", err)
	}
	m.AnnotateHealth(v, nil)
	for _, item := range m.items {
		if item.isNew {
			continue
		}
		if item.health != AccountHealthUnchecked {
			t.Errorf("account %s with nil checker: health=%q, want unchecked", item.email, item.health)
		}
	}
}

func TestProviderPicker_AnnotateHealth_AppendsCountToGoogleSummary(t *testing.T) {
	v := setupVault(t,
		&vault.Credential{Provider: "google", Account: "healthy@example.com"},
		&vault.Credential{Provider: "google", Account: "broken@example.com"},
	)
	m, err := newProviderPickerModel(v, nil, nil)
	if err != nil {
		t.Fatalf("newProviderPickerModel: %v", err)
	}

	// Verify baseline summary doesn't have the badge yet.
	for _, it := range m.items {
		if it.name == "google" && strings.Contains(it.summary, "needs reauth") {
			t.Fatalf("google summary already has badge before AnnotateHealth: %q", it.summary)
		}
	}

	check := func(c *vault.Credential) AccountHealth {
		if c.Account == "broken@example.com" {
			return AccountHealthNeedsReauth
		}
		return AccountHealthHealthy
	}
	m.AnnotateHealth(v, check)

	// Find Google row, assert summary has the badge.
	for _, it := range m.items {
		if it.name == "google" {
			if !strings.Contains(it.summary, "(1 needs reauth)") {
				t.Errorf("google summary missing badge after AnnotateHealth: %q", it.summary)
			}
		}
	}
}

func TestProviderPicker_AnnotateHealth_NoBadgeWhenAllHealthy(t *testing.T) {
	v := setupVault(t,
		&vault.Credential{Provider: "google", Account: "a@example.com"},
		&vault.Credential{Provider: "google", Account: "b@example.com"},
	)
	m, err := newProviderPickerModel(v, nil, nil)
	if err != nil {
		t.Fatalf("newProviderPickerModel: %v", err)
	}
	check := func(c *vault.Credential) AccountHealth { return AccountHealthHealthy }
	m.AnnotateHealth(v, check)

	for _, it := range m.items {
		if it.name == "google" && strings.Contains(it.summary, "needs reauth") {
			t.Errorf("google summary should not have badge when all healthy: %q", it.summary)
		}
	}
}
