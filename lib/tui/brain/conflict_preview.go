package brain

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// conflictPreviewModel renders the first N lines of each conflict
// file, with `<<<<<<<` / `=======` / `>>>>>>>` markers highlighted.
// Read-only — actually merging is /nous-resolve's job (nous#5).
type conflictPreviewModel struct {
	root  string
	rels  []string
	limit int // lines per file
	err   error
}

// previewLineLimit caps lines per file in the preview. 20 lines is
// usually enough to see both halves of a typical text conflict
// (header + ours + ===== + theirs + tail); operator runs /nous-resolve
// for full view.
const previewLineLimit = 20

func newConflictPreviewModel(root string, rels []string) conflictPreviewModel {
	return conflictPreviewModel{root: root, rels: rels, limit: previewLineLimit}
}

func (m conflictPreviewModel) Init() tea.Cmd { return nil }

func (m conflictPreviewModel) Update(msg tea.Msg) (conflictPreviewModel, tea.Cmd) {
	keyMsg, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch keyMsg.String() {
	case "esc", "q":
		return m, func() tea.Msg { return popToListMsg{} }
	case "ctrl+c":
		return m, tea.Quit
	}
	return m, nil
}

func (m conflictPreviewModel) View() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render(fmt.Sprintf("Conflicts in %s", m.root)))
	b.WriteString("\n\n")
	if len(m.rels) == 0 {
		b.WriteString(mutedStyle.Render("(none)"))
		b.WriteString("\n")
		b.WriteString(helpStyle.Render("\nesc/q  back    ctrl+c  quit"))
		return b.String()
	}
	for _, rel := range m.rels {
		full := filepath.Join(m.root, rel)
		b.WriteString(sectionHeaderStyle.Render(rel))
		b.WriteString("\n")
		data, err := os.ReadFile(full)
		if err != nil {
			b.WriteString(warnStyle.Render("  error: " + err.Error()))
			b.WriteString("\n")
			continue
		}
		lines := strings.Split(string(data), "\n")
		shown := 0
		for _, ln := range lines {
			if shown >= m.limit {
				b.WriteString(mutedStyle.Render(fmt.Sprintf("  … truncated at %d lines\n", m.limit)))
				break
			}
			b.WriteString("  ")
			b.WriteString(highlightMarker(ln))
			b.WriteString("\n")
			shown++
		}
	}
	b.WriteString("\n")
	b.WriteString(helpStyle.Render("esc/q  back    ctrl+c  quit"))
	return b.String()
}

func highlightMarker(line string) string {
	switch {
	case strings.HasPrefix(line, "<<<<<<<"):
		return conflictHeadStyle.Render(line)
	case strings.HasPrefix(line, "======="):
		return conflictMidStyle.Render(line)
	case strings.HasPrefix(line, ">>>>>>>"):
		return conflictTailStyle.Render(line)
	}
	return line
}
