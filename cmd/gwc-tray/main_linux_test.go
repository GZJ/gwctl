//go:build linux

package main

import (
	"fmt"
	"os"
	"os/exec"
	"reflect"
	"testing"
	"time"

	"github.com/BurntSushi/xgb"
	"github.com/BurntSushi/xgb/xproto"
)

func TestWindowsInVisibilityOrder(t *testing.T) {
	windows := []xproto.Window{0x101, 0x102, 0x103}

	if got := windowsInVisibilityOrder(windows, false); !reflect.DeepEqual(got, windows) {
		t.Fatalf("hide order = %v, want %v", got, windows)
	}

	wantShow := []xproto.Window{0x103, 0x102, 0x101}
	if got := windowsInVisibilityOrder(windows, true); !reflect.DeepEqual(got, wantShow) {
		t.Fatalf("show order = %v, want %v", got, wantShow)
	}

	if !reflect.DeepEqual(windows, []xproto.Window{0x101, 0x102, 0x103}) {
		t.Fatalf("input was modified: %v", windows)
	}
}

func TestTargetFlagsPreserveMixedCommandLineOrder(t *testing.T) {
	var specs []targetSpec
	titleFlag := targetFlag{kind: "title", specs: &specs}
	idFlag := targetFlag{kind: "id", specs: &specs}
	_ = titleFlag.Set("Editor")
	_ = idFlag.Set("0x123")
	_ = titleFlag.Set("Terminal")

	want := []targetSpec{
		{kind: "title", value: "Editor"},
		{kind: "id", value: "0x123"},
		{kind: "title", value: "Terminal"},
	}
	if !reflect.DeepEqual(specs, want) {
		t.Fatalf("target order = %#v, want %#v", specs, want)
	}
}

func TestRemoveTargetWindowKeepsRemainingOrder(t *testing.T) {
	initAppState()
	state.targetWins = []xproto.Window{0x101, 0x102, 0x103}
	state.targetWin = state.targetWins[0]

	if removed, empty := removeTargetWindow(0x102); !removed || empty {
		t.Fatal("reported empty while targets remain")
	}
	want := []xproto.Window{0x101, 0x103}
	if !reflect.DeepEqual(state.targetWins, want) {
		t.Fatalf("remaining windows = %v, want %v", state.targetWins, want)
	}
	if state.targetWin != 0x101 {
		t.Fatalf("primary target = %#x, want 0x101", state.targetWin)
	}
}

func TestAlacrittyMultiWindow(t *testing.T) {
	if os.Getenv("DISPLAY") == "" {
		t.Skip("DISPLAY is not set")
	}
	if _, err := exec.LookPath("alacritty"); err != nil {
		t.Skip("alacritty is not installed")
	}
	conn, err := xgb.NewConn()
	if err != nil {
		t.Skipf("X11 display is not accessible: %v", err)
	}
	defer conn.Close()

	titles := []string{
		fmt.Sprintf("gwc-tray-alacritty-first-%d", os.Getpid()),
		fmt.Sprintf("gwc-tray-alacritty-second-%d", os.Getpid()),
	}
	commands := make([]*exec.Cmd, 0, 2)
	for i := 0; i < 2; i++ {
		cmd := exec.Command("alacritty", "--title", titles[i], "--option", "window.dynamic_title=false")
		if err := cmd.Start(); err != nil {
			t.Fatalf("start Alacritty %d: %v", i+1, err)
		}
		commands = append(commands, cmd)
	}
	t.Cleanup(func() {
		for _, cmd := range commands {
			if cmd.Process != nil {
				_ = cmd.Process.Kill()
				_, _ = cmd.Process.Wait()
			}
		}
	})

	deadline := time.Now().Add(10 * time.Second)
	var windows []xproto.Window
	specs := []targetSpec{{kind: "title", value: titles[0]}, {kind: "title", value: titles[1]}}
	for time.Now().Before(deadline) {
		windows, err = resolveTargetWindows(conn, specs)
		if err == nil && len(windows) == 2 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if len(windows) != 2 {
		t.Fatalf("found %d Alacritty windows, want 2 (last error: %v)", len(windows), err)
	}

	// Discovery can win the race with the window manager's initial MapRequest.
	assertMapState(t, conn, windows, xproto.MapStateViewable)
	moveWindowsAwayFromInitialPositions(t, conn, windows)
	positions := windowPositions(t, conn, windows)
	setWindowsVisibility(conn, windows, false)
	assertMapState(t, conn, windows, xproto.MapStateUnmapped)
	setWindowsVisibility(conn, windows, true)
	assertMapState(t, conn, windows, xproto.MapStateViewable)
	assertActiveWindow(t, conn, windows[0])
	assertWindowPositions(t, conn, positions)
}

func moveWindowsAwayFromInitialPositions(t *testing.T, conn *xgb.Conn, windows []xproto.Window) {
	t.Helper()
	initial := windowPositions(t, conn, windows)
	for i, window := range windows {
		position := initial[window]
		values := []uint32{uint32(position.x + int32(90+i*40)), uint32(position.y + int32(70+i*40))}
		mask := uint16(xproto.ConfigWindowX | xproto.ConfigWindowY)
		if err := xproto.ConfigureWindowChecked(conn, window, mask, values).Check(); err != nil {
			t.Fatalf("move window %#x: %v", window, err)
		}
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if !reflect.DeepEqual(windowPositions(t, conn, windows), initial) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("window manager did not move the Alacritty test windows")
}

func windowPositions(t *testing.T, conn *xgb.Conn, windows []xproto.Window) map[xproto.Window]windowPosition {
	t.Helper()
	positions := make(map[xproto.Window]windowPosition, len(windows))
	for _, window := range windows {
		position, err := currentWindowPosition(conn, window)
		if err != nil {
			t.Fatalf("read position for %#x: %v", window, err)
		}
		positions[window] = position
	}
	return positions
}

func assertWindowPositions(t *testing.T, conn *xgb.Conn, want map[xproto.Window]windowPosition) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var got map[xproto.Window]windowPosition
	for time.Now().Before(deadline) {
		windows := make([]xproto.Window, 0, len(want))
		for window := range want {
			windows = append(windows, window)
		}
		got = windowPositions(t, conn, windows)
		if reflect.DeepEqual(got, want) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("window positions after show = %v, want %v", got, want)
}

func assertMapState(t *testing.T, conn *xgb.Conn, windows []xproto.Window, want byte) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	states := make(map[xproto.Window]byte, len(windows))
	for time.Now().Before(deadline) {
		allMatch := true
		for _, window := range windows {
			attrs, err := xproto.GetWindowAttributes(conn, window).Reply()
			if err != nil {
				t.Fatalf("get attributes for %#x: %v", window, err)
			}
			states[window] = attrs.MapState
			if attrs.MapState != want {
				allMatch = false
			}
		}
		if allMatch {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	for _, window := range windows {
		if states[window] != want {
			t.Errorf("window %#x map state = %d, want %d", window, states[window], want)
		}
	}
}

func assertActiveWindow(t *testing.T, conn *xgb.Conn, want xproto.Window) {
	t.Helper()
	name := "_NET_ACTIVE_WINDOW"
	atom, err := xproto.InternAtom(conn, false, uint16(len(name)), name).Reply()
	if err != nil {
		t.Fatalf("intern %s: %v", name, err)
	}
	root := xproto.Setup(conn).DefaultScreen(conn).Root
	deadline := time.Now().Add(5 * time.Second)
	var got xproto.Window
	for time.Now().Before(deadline) {
		reply, propertyErr := xproto.GetProperty(conn, false, root, atom.Atom, xproto.AtomWindow, 0, 1).Reply()
		if propertyErr != nil {
			t.Fatalf("read %s: %v", name, propertyErr)
		}
		if len(reply.Value) >= 4 {
			got = xproto.Window(xgb.Get32(reply.Value))
			if got == want {
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("active window = %#x, want first matched window %#x", got, want)
}
