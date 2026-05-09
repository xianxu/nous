package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os/exec"
	"sync"
	"time"

	"fyne.io/systray"
	"github.com/spf13/cobra"
	"github.com/xianxu/nous/lib/provider/proxy"
)

// menubarCmd runs Charon Security as a macOS menubar agent. The
// bundle's Info.plist already has LSUIElement=true, so this process
// runs without a Dock icon — just the menubar item.
//
// Talks to the proxy's unix-domain runtime socket (#16 C) for state.
// Connection-per-RPC keeps the peer-DR check fresh on the proxy side.
func menubarCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "menubar",
		Short: "Run as macOS menubar agent (consent oracle for the proxy)",
		Long: `Charon Security's menubar mode: a status icon in the macOS menu bar
that shows whether the proxy session is armed and lets the user
arm/disarm with a click. Talks to the proxy at
~/Library/Caches/charon/runtime.sock.

Started automatically when the .app bundle is double-clicked or
opened via 'open' (LSUIElement=true keeps it dock-less). To exit,
click the menubar item and pick Quit.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			runMenubar()
			return nil
		},
	}
}

// runMenubar blocks on systray.Run until the user picks Quit.
// All UI work happens via systray's API which marshals callbacks
// onto the AppKit main thread.
func runMenubar() {
	systray.Run(menubarReady, menubarExit)
}

// Poll cadence is adaptive: when the session has more than a
// minute of TTL left, polling every 10s is plenty (the title only
// updates per-minute anyway). Inside the last minute, the ticking
// "30s … 29s …" countdown is the whole point — poll every second.
// When disarmed or unreachable, default to the slow cadence.
const (
	pollIntervalSlow = 10 * time.Second
	pollIntervalFast = 1 * time.Second
	pollFastBelow    = 1 * time.Minute
)

// menubarState is shared between the polling goroutine and the
// menu callbacks. mu guards reads/writes of the embedded SessionStatus
// and lastErr.
var menubarState struct {
	mu       sync.Mutex
	armed    bool
	expires  time.Time
	reason   string
	ttlLeft  time.Duration
	lastErr  string

	// Menu items are package-globals because systray's callbacks
	// fire on UI threads and need handles to the items they
	// affect (title updates).
	statusItem *systray.MenuItem
	armed30    *systray.MenuItem
	armed1h    *systray.MenuItem
	armed8h    *systray.MenuItem
	disarm     *systray.MenuItem
	quit       *systray.MenuItem
}

func menubarReady() {
	systray.SetTitle(menubarTitle(false, "starting"))
	systray.SetTooltip("Charon — runtime consent oracle")

	// Pop the system "Charon Security would like to send notifications"
	// prompt on first launch. Idempotent on subsequent launches; the
	// user's answer persists in System Settings → Notifications.
	requestNotificationAuth()

	menubarState.mu.Lock()
	menubarState.statusItem = systray.AddMenuItem("Status: …", "Current session state")
	menubarState.statusItem.Disable()
	systray.AddSeparator()
	menubarState.armed30 = systray.AddMenuItem("Arm for 30 minutes", "Allow proxy traffic for 30m")
	menubarState.armed1h = systray.AddMenuItem("Arm for 1 hour", "Allow proxy traffic for 1h")
	menubarState.armed8h = systray.AddMenuItem("Arm for 8 hours (max)", "Allow proxy traffic for the maximum")
	menubarState.disarm = systray.AddMenuItem("Disarm", "Refuse new CONNECTs immediately")
	systray.AddSeparator()
	menubarState.quit = systray.AddMenuItem("Quit Charon Security", "Exit the menubar agent")
	menubarState.mu.Unlock()

	// Initial poll so the title is accurate before the timer fires.
	refreshState()
	go pollLoop()
	go menuClickLoop()
}

func menubarExit() {
	// Nothing to clean up — connections to the runtime socket are
	// per-RPC and short-lived.
}

// pollLoop refreshes session state at an adaptive cadence: fast
// inside the last minute (so the displayed countdown ticks every
// second) and slow otherwise. We use time.Sleep + recompute rather
// than a ticker so the cadence can change as soon as the new ttl is
// known.
func pollLoop() {
	for {
		time.Sleep(nextPollDelay())
		refreshState()
	}
}

// nextPollDelay picks the wait until the next refresh based on the
// last-known ttl. The < pollFastBelow check is "less than", so
// at exactly 60s remaining we still poll every 10s; only once we
// dip below do we switch to per-second updates.
func nextPollDelay() time.Duration {
	menubarState.mu.Lock()
	armed := menubarState.armed
	ttl := menubarState.ttlLeft
	menubarState.mu.Unlock()
	if armed && ttl > 0 && ttl < pollFastBelow {
		return pollIntervalFast
	}
	return pollIntervalSlow
}

// menuClickLoop dispatches menu item activations onto runtime-socket
// RPCs. systray's API delivers clicks via per-item channels, so we
// fan them in via a select.
func menuClickLoop() {
	for {
		select {
		case <-menubarState.armed30.ClickedCh:
			doArm(30 * time.Minute)
		case <-menubarState.armed1h.ClickedCh:
			doArm(1 * time.Hour)
		case <-menubarState.armed8h.ClickedCh:
			doArm(8 * time.Hour)
		case <-menubarState.disarm.ClickedCh:
			doDisarm()
		case <-menubarState.quit.ClickedCh:
			systray.Quit()
			return
		}
	}
}

func doArm(ttl time.Duration) {
	resp, err := socketRoundTrip(runtimeReq{Op: "arm", TTLSeconds: int64(ttl.Seconds())})
	updateAfterRPC(resp, err)
	if err == nil && !resp.OK {
		notify("Charon: arm failed", resp.Error)
	}
}

func doDisarm() {
	resp, err := socketRoundTrip(runtimeReq{Op: "disarm"})
	updateAfterRPC(resp, err)
}

func refreshState() {
	prevArmed := getArmed()
	resp, err := socketRoundTrip(runtimeReq{Op: "status"})
	updateAfterRPC(resp, err)
	// If the session went from armed→disarmed without the user
	// driving disarm (i.e., this poll discovered an idle/absolute
	// auto-disarm), notify.
	if prevArmed && !getArmed() && err == nil {
		notify("Charon", "Session auto-disarmed (idle or absolute timeout). Click the ○ icon in the menu bar to re-arm.")
	}
}

func getArmed() bool {
	menubarState.mu.Lock()
	defer menubarState.mu.Unlock()
	return menubarState.armed
}

func updateAfterRPC(resp runtimeResp, err error) {
	menubarState.mu.Lock()
	defer menubarState.mu.Unlock()
	if err != nil {
		menubarState.lastErr = err.Error()
		menubarState.armed = false
		systray.SetTitle(menubarTitle(false, "no proxy?"))
		if menubarState.statusItem != nil {
			menubarState.statusItem.SetTitle("Status: cannot reach proxy (" + err.Error() + ")")
		}
		return
	}
	menubarState.lastErr = ""
	if resp.Status != nil {
		menubarState.armed = resp.Status.Armed
		menubarState.expires = resp.Status.ExpiresAt
		menubarState.reason = resp.Status.ExpiresReason
		menubarState.ttlLeft = resp.Status.TTLRemaining
	} else {
		menubarState.armed = false
	}
	systray.SetTitle(menubarTitle(menubarState.armed, summarize(menubarState.armed, menubarState.ttlLeft)))
	if menubarState.statusItem != nil {
		if menubarState.armed {
			menubarState.statusItem.SetTitle(fmt.Sprintf("Status: armed (%s left, %s timer)",
				humanDuration(menubarState.ttlLeft), menubarState.reason))
		} else {
			menubarState.statusItem.SetTitle("Status: disarmed")
		}
	}
}

// menubarTitle returns the menubar's display string. Glyph + short
// state — narrow enough to fit alongside other agents in a
// crowded menubar.
func menubarTitle(armed bool, state string) string {
	if armed {
		return "● " + state
	}
	return "○ " + state
}

func summarize(armed bool, ttl time.Duration) string {
	if !armed {
		return "off"
	}
	return humanDuration(ttl)
}

// humanDuration renders a Duration like "27m", "1h12m", "59s".
// Shorter than time.Duration.String() for menubar-width budgets.
func humanDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	h := int(d / time.Hour)
	m := int((d % time.Hour) / time.Minute)
	if m == 0 {
		return fmt.Sprintf("%dh", h)
	}
	return fmt.Sprintf("%dh%dm", h, m)
}

// runtimeReq mirrors proxy/runtime_socket.go's runtimeRequest. We
// duplicate the shape rather than import to keep the security.app
// bundle's dependency graph minimal — it should not need to drag in
// the full proxy package just to talk to the socket.
type runtimeReq struct {
	Op           string `json:"op"`
	TTLSeconds   int64  `json:"ttl_seconds,omitempty"`
	SinceSeconds int64  `json:"since_seconds,omitempty"`
}

type runtimeResp struct {
	OK      bool                 `json:"ok"`
	Error   string               `json:"error,omitempty"`
	Status  *proxy.SessionStatus `json:"status,omitempty"`
	Entries []proxy.AuditEntry   `json:"entries,omitempty"`
}

// socketRoundTrip dials the runtime socket, sends one request,
// reads one response, closes. Connection-per-RPC matches the
// server side.
func socketRoundTrip(req runtimeReq) (runtimeResp, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	d := net.Dialer{}
	c, err := d.DialContext(ctx, "unix", proxy.RuntimeSocketPath())
	if err != nil {
		return runtimeResp{}, err
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(3 * time.Second))
	if err := json.NewEncoder(c).Encode(req); err != nil {
		return runtimeResp{}, err
	}
	var resp runtimeResp
	if err := json.NewDecoder(c).Decode(&resp); err != nil {
		return runtimeResp{}, err
	}
	return resp, nil
}

// notify shows a macOS notification banner. When running inside the
// Charon Security.app bundle, uses UserNotifications.framework via
// cgo so the banner is attributed to com.charon.security (and the
// user's Banner-vs-Alert preference is scoped to this app rather
// than to Script Editor). Falls back to osascript when run as a
// bare binary during dev iteration. Best-effort: failures are
// swallowed so the menubar stays usable.
func notify(title, msg string) {
	if hasBundle() {
		postNativeNotification(title, msg)
		return
	}
	script := fmt.Sprintf(`display notification %q with title %q`, msg, title)
	go func() {
		err := exec.Command("osascript", "-e", script).Run()
		if err != nil {
			log.Printf("notify osascript: %v", err)
		}
	}()
}
