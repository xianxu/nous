package tui

import (
	"fmt"
	"os"
	"sync"
	"time"
)

// Debug logging for diagnosing TUI rendering issues.
// Writes to /tmp/charon-tui-debug.log. Enable by setting CHARON_TUI_DEBUG=1
// before running. To disable, unset the env var (logging is then a no-op).

var (
	debugFile     *os.File
	debugInitOnce sync.Once
)

func debugInit() {
	debugInitOnce.Do(func() {
		if os.Getenv("CHARON_TUI_DEBUG") == "" {
			return
		}
		f, err := os.OpenFile("/tmp/charon-tui-debug.log",
			os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
		if err == nil {
			debugFile = f
			fmt.Fprintf(f, "\n\n=== charon-tui-debug session start: %s ===\n",
				time.Now().Format(time.RFC3339))
		}
	})
}

func debugf(format string, args ...interface{}) {
	debugInit()
	if debugFile == nil {
		return
	}
	fmt.Fprintf(debugFile, "[%s] "+format+"\n",
		append([]interface{}{time.Now().Format("15:04:05.000")}, args...)...)
}
