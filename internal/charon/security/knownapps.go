package security

// AppCategory groups known apps for severity decisions: a terminal or
// editor with FDA is a Critical finding; an IDE with screen recording
// is Important.
type AppCategory int

const (
	CatTerminal AppCategory = iota
	CatEditor
	CatIDE
)

func (c AppCategory) String() string {
	switch c {
	case CatTerminal:
		return "terminal"
	case CatEditor:
		return "editor"
	case CatIDE:
		return "IDE"
	default:
		return "unknown"
	}
}

type KnownApp struct {
	BundleID string
	Name     string
	Category AppCategory
}

// KnownApps is the curated list of terminals/editors/IDEs that should
// not normally hold dangerous TCC grants. Adding to this list expands
// the surface the audit warns on.
//
// The list is hardcoded — bundle IDs change rarely (annually-ish);
// externalizing to YAML would add complexity without payoff for ~30
// entries. Use --apps-extra to inject extras at runtime.
var KnownApps = []KnownApp{
	// Terminals
	{"com.apple.Terminal", "Terminal", CatTerminal},
	{"com.googlecode.iterm2", "iTerm2", CatTerminal},
	{"com.mitchellh.ghostty", "Ghostty", CatTerminal},
	{"dev.warp.Warp-Stable", "Warp", CatTerminal},
	{"co.zeit.hyper", "Hyper", CatTerminal},
	{"org.alacritty", "Alacritty", CatTerminal},
	{"com.github.wez.wezterm", "WezTerm", CatTerminal},
	{"net.kovidgoyal.kitty", "Kitty", CatTerminal},
	{"org.tabby", "Tabby", CatTerminal},
	{"com.cmuxterm.app", "cmux", CatTerminal}, // verified empirically via TCC AttributionChain log

	// Editors
	{"com.microsoft.VSCode", "VS Code", CatEditor},
	{"com.todesktop.230313mzl4w4u92", "Cursor", CatEditor},
	{"com.exafunction.windsurf", "Windsurf", CatEditor},
	{"dev.zed.Zed", "Zed", CatEditor},
	{"com.sublimetext.4", "Sublime Text", CatEditor},
	{"com.panic.Nova", "Nova", CatEditor},

	// IDEs
	{"com.apple.dt.Xcode", "Xcode", CatIDE},
	{"com.jetbrains.intellij", "IntelliJ IDEA", CatIDE},
	{"com.jetbrains.intellij.ce", "IntelliJ IDEA CE", CatIDE},
	{"com.jetbrains.goland", "GoLand", CatIDE},
	{"com.jetbrains.pycharm", "PyCharm", CatIDE},
	{"com.jetbrains.WebStorm", "WebStorm", CatIDE},
	{"com.jetbrains.rubymine", "RubyMine", CatIDE},
	{"com.jetbrains.CLion", "CLion", CatIDE},
	{"com.jetbrains.rider", "Rider", CatIDE},
}

// LookupApp returns the KnownApp for a given bundle ID, or zero-value
// + false if it isn't on the curated list.
func LookupApp(bundleID string) (KnownApp, bool) {
	for _, a := range KnownApps {
		if a.BundleID == bundleID {
			return a, true
		}
	}
	return KnownApp{}, false
}
