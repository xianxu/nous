package charoncli

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// The embedded instructions must contain enough load-bearing markers
// that an agent can find what it needs. If we ever drop one of these
// from the doc, this test forces a deliberate update.
func TestInstructionsContainsLoadBearingSections(t *testing.T) {
	if agentInstructions == "" {
		t.Fatal("agentInstructions is empty — //go:embed didn't pick up agent_instructions.md")
	}
	for _, want := range []string{
		"charon manifest",
		"charon run",
		"X-Charon-Account",
		"X-Charon-Scope",
		"https://gmail.googleapis.com",
		"aiplatform.googleapis.com",
		"generativelanguage.googleapis.com",
		"BILLING_DISABLED",
		"project_id",
		"region",
	} {
		if !strings.Contains(agentInstructions, want) {
			t.Errorf("instructions missing load-bearing string %q", want)
		}
	}
}

func TestInstructionsCmd_PrintsToStdout(t *testing.T) {
	root := &cobra.Command{Use: "charon"}
	root.AddCommand(InstructionsCmd())
	stdout, _, err := executeCmd(root, "instructions")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, "Charon — Agent Instructions") {
		t.Error("expected header in output")
	}
	if !strings.Contains(stdout, "X-Charon-Account") {
		t.Error("expected X-Charon-Account section in output")
	}
}
