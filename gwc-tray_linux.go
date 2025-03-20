//go:build linux
// +build linux

package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"github.com/BurntSushi/xgb"
	"github.com/BurntSushi/xgb/xproto"
	"github.com/getlantern/systray"
	"github.com/getlantern/systray/example/icon"
)

type AppState struct {
	conn       *xgb.Conn
	winTitle   string
	winID      string
	targetWin  xproto.Window
	isVisible  bool
	keyCombo   string
	keyMods    uint16
	keyCode    xproto.Keycode
	root       xproto.Window
	exitSignal chan struct{}
	mutex      sync.Mutex
	wg         sync.WaitGroup
}

var state AppState

func initAppState() {
	state = AppState{
		exitSignal: make(chan struct{}),
	}
}

// --------------------------------- window ---------------------------------
func findWindowRecursive(conn *xgb.Conn, parent xproto.Window, title string) (xproto.Window, error) {
	nameReply, err := xproto.GetProperty(conn, false, parent,
		xproto.AtomWmName, xproto.AtomString, 0, (1<<32)-1).Reply()

	if err == nil && nameReply != nil && nameReply.ValueLen > 0 {
		windowName := string(nameReply.Value)
		if strings.Contains(strings.ToLower(windowName), strings.ToLower(title)) {
			log.Printf("Window title match: %s\n", windowName)
			return parent, nil
		}
	}

	treeReply, err := xproto.QueryTree(conn, parent).Reply()
	if err != nil {
		return 0, err
	}

	for _, child := range treeReply.Children {
		if found, err := findWindowRecursive(conn, child, title); err == nil && found != 0 {
			return found, nil
		}
	}

	return 0, nil
}

func findWindowByTitle(conn *xgb.Conn, title string) (xproto.Window, error) {
	root := xproto.Setup(conn).DefaultScreen(conn).Root
	target, err := findWindowRecursive(conn, root, title)
	if err != nil {
		log.Printf("Error finding window: %v\n", err)
		return 0, err
	}

	if target == 0 {
		log.Printf("No window found with title containing '%s'\n", title)
		return 0, fmt.Errorf("window not found")
	}

	log.Printf("Found window with title containing '%s'\n", title)
	return target, nil
}

func findWindowByID(conn *xgb.Conn, windowIDStr string) (xproto.Window, error) {
	var windowID uint64
	var err error

	if strings.HasPrefix(windowIDStr, "0x") {
		windowID, err = strconv.ParseUint(windowIDStr[2:], 16, 32)
	} else {
		windowID, err = strconv.ParseUint(windowIDStr, 10, 32)
	}

	if err != nil {
		log.Printf("Invalid window ID format: %s\n", windowIDStr)
		return 0, err
	}

	window := xproto.Window(windowID)

	_, err = xproto.GetWindowAttributes(conn, window).Reply()
	if err != nil {
		log.Printf("Window ID exists but cannot get attributes: 0x%x\n", window)
		return 0, err
	}

	log.Printf("Found window with ID: 0x%x\n", window)
	return window, nil
}

func findWindow(conn *xgb.Conn, identifier string) (xproto.Window, error) {
	window, err := findWindowByID(conn, identifier)
	if err != nil {
		log.Printf("Trying to find window by title instead...\n")
		return findWindowByTitle(conn, identifier)
	}
	return window, nil
}

func setWindowVisibility(conn *xgb.Conn, window xproto.Window, visible bool) {
	state.mutex.Lock()
	defer state.mutex.Unlock()

	if visible {
		xproto.MapWindow(conn, window)
		log.Println("Window mapped (shown)")
		state.isVisible = true
	} else {
		xproto.UnmapWindow(conn, window)
		log.Println("Window unmapped (hidden)")
		state.isVisible = false
	}
}

func toggleWindowVisibility() {
	attrs, err := xproto.GetWindowAttributes(state.conn, state.targetWin).Reply()
	if err != nil {
		log.Printf("Error getting window attributes: %v\n", err)
		updateTargetWindow()
		return
	}

	isCurrentlyVisible := attrs.MapState != 0
	setWindowVisibility(state.conn, state.targetWin, !isCurrentlyVisible)

	updateSystrayTooltip()
}

func updateSystrayTooltip() {
	state.mutex.Lock()
	defer state.mutex.Unlock()

	if state.isVisible {
		systray.SetTooltip(fmt.Sprintf("Hide %s", formatWindowDescription()))
	} else {
		systray.SetTooltip(fmt.Sprintf("Show %s", formatWindowDescription()))
	}
}

func formatWindowDescription() string {
	if state.winTitle != "" {
		return fmt.Sprintf("'%s'", state.winTitle)
	}
	return fmt.Sprintf("window 0x%x", state.targetWin)
}

func updateTargetWindow() {
	var err error
	if state.winTitle != "" {
		state.targetWin, err = findWindowByTitle(state.conn, state.winTitle)
	} else if state.winID != "" {
		state.targetWin, err = findWindowByID(state.conn, state.winID)
	}
	if err != nil {
		log.Printf("Error refreshing target window: %v\n", err)
	}
}

func listWindows(conn *xgb.Conn) {
	root := xproto.Setup(conn).DefaultScreen(conn).Root

	fmt.Println("\nAvailable windows:")
	fmt.Println("----------------")

	treeReply, err := xproto.QueryTree(conn, root).Reply()
	if err != nil {
		fmt.Printf("Error querying window tree: %v\n", err)
		return
	}

	for _, child := range treeReply.Children {
		nameReply, err := xproto.GetProperty(conn, false, child,
			xproto.AtomWmName, xproto.AtomString, 0, (1<<32)-1).Reply()

		if err == nil && nameReply != nil && nameReply.ValueLen > 0 {
			windowName := string(nameReply.Value)
			fmt.Printf("ID: 0x%x, Title: %s\n", child, windowName)
		}
	}

	fmt.Println("----------------")
}

// --------------------------------- tray ---------------------------------
func onSystrayReady() {
	systray.SetIcon(icon.Data)
	systray.SetTitle(state.winTitle)
	systray.SetTooltip(fmt.Sprintf("Toggle visibility of %s", formatWindowDescription()))

	mToggle := systray.AddMenuItem(fmt.Sprintf("Toggle %s", formatWindowDescription()), "Toggle window visibility")

	if state.keyCombo != "" {
		mShortcut := systray.AddMenuItem(fmt.Sprintf("Shortcut: %s", state.keyCombo), "Keyboard shortcut")
		mShortcut.Disable()
	}

	mQuit := systray.AddMenuItem("Quit", "Quit the application")

	go func() {
		for {
			select {
			case <-mToggle.ClickedCh:
				toggleWindowVisibility()
			case <-mQuit.ClickedCh:
				cleanupAndExit()
				return
			case <-state.exitSignal:
				return
			}
		}
	}()
}

func onSystrayExit() {
	log.Println("Exiting...")
}

func cleanupAndExit() {
	close(state.exitSignal)
	systray.Quit()
	state.wg.Wait()
	if state.conn != nil {
		state.conn.Close()
	}
}

// --------------------------------- hotkey ---------------------------------
func parseKeyCombo(combo string) (uint16, byte, error) {
	parts := strings.Split(strings.ToLower(combo), "+")
	if len(parts) < 1 {
		return 0, 0, fmt.Errorf("invalid key combination format")
	}

	var mods uint16 = 0

	keyName := parts[len(parts)-1]

	if len(keyName) == 1 && keyName[0] >= 'a' && keyName[0] <= 'z' {
		for i := 0; i < len(parts)-1; i++ {
			switch parts[i] {
			case "ctrl":
				mods |= xproto.ModMaskControl
			case "shift":
				mods |= xproto.ModMaskShift
			case "alt":
				mods |= xproto.ModMask1
			case "super":
				mods |= xproto.ModMask4
			default:
				log.Printf("Warning: Unknown modifier '%s'\n", parts[i])
			}
		}
		return mods, keyName[0], nil
	}

	return 0, 0, fmt.Errorf("unsupported key: %s, only a-z keys are supported", keyName)
}

func getKeycodeFromChar(conn *xgb.Conn, char byte) (xproto.Keycode, error) {
	reply, err := xproto.GetKeyboardMapping(conn, 8, 248).Reply()
	if err != nil {
		return 0, err
	}

	keysymsPerKeycode := int(reply.KeysymsPerKeycode)
	for i := 0; i < 248; i++ {
		for j := 0; j < 2; j++ {
			idx := i*keysymsPerKeycode + j
			if idx < len(reply.Keysyms) {
				keysym := uint32(reply.Keysyms[idx])
				if keysym == uint32(char) || keysym == uint32(char-32) {
					return xproto.Keycode(i + 8), nil
				}
			}
		}
	}

	return 0, fmt.Errorf("keycode not found for char: %c", char)
}

func setupKeyboardShortcut() error {
	state.root = xproto.Setup(state.conn).DefaultScreen(state.conn).Root

	var err error
	var keyChar byte
	state.keyMods, keyChar, err = parseKeyCombo(state.keyCombo)
	if err != nil {
		return err
	}

	state.keyCode, err = getKeycodeFromChar(state.conn, keyChar)
	if err != nil {
		return err
	}

	err = xproto.GrabKeyChecked(
		state.conn,
		true,
		state.root,
		state.keyMods,
		state.keyCode,
		xproto.GrabModeAsync,
		xproto.GrabModeAsync,
	).Check()

	return err
}

func listenForKeyEvents() {
	state.wg.Add(1)
	defer state.wg.Done()
	for {
		select {
		case <-state.exitSignal:
			return
		default:
			ev, err := state.conn.WaitForEvent()
			if err != nil {
				log.Printf("Error waiting for X event: %v\n", err)
				continue
			}

			switch e := ev.(type) {
			case xproto.KeyPressEvent:
				if e.Detail == state.keyCode && e.State == state.keyMods {
					log.Println("Shortcut detected, toggling window visibility")
					toggleWindowVisibility()
				}
			}
		}
	}
}

// --------------------------------- main ---------------------------------
func main() {
	initAppState()

	log.SetOutput(os.Stdout)
	log.SetPrefix("[WindowToggler] ")

	flag.StringVar(&state.winTitle, "title", "", "Window title to control")
	flag.StringVar(&state.winID, "id", "", "Window ID to control (decimal or hex with 0x prefix)")
	flag.StringVar(&state.keyCombo, "key", "", "Keyboard shortcut (e.g., 'ctrl+shift+alt+a')")
	flag.Parse()

	if state.winTitle == "" && state.winID == "" {
		fmt.Println("Error: Either -title or -id must be specified")
		fmt.Println("Usage: ")
		fmt.Println("  To control by title: go run main.go -title \"Firefox\" [-key \"ctrl+shift+alt+a\"]")
		fmt.Println("  To control by ID:    go run main.go -id 0x1234567 [-key \"ctrl+shift+alt+a\"]")
		return
	}

	var err error
	state.conn, err = xgb.NewConn()
	if err != nil {
		log.Fatalf("Cannot open display: %v\n", err)
		return
	}
	defer state.conn.Close()

	if state.winTitle != "" {
		state.targetWin, err = findWindowByTitle(state.conn, state.winTitle)
	} else {
		state.targetWin, err = findWindowByID(state.conn, state.winID)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "Window not found: %s\n", state.winTitle+state.winID)
		fmt.Println("\nTip: The window might be:")
		fmt.Println("1. Not currently open")
		fmt.Println("2. Using a different title than expected")
		fmt.Println("3. Not accessible to this program")
		listWindows(state.conn)
		return
	}

	attrs, err := xproto.GetWindowAttributes(state.conn, state.targetWin).Reply()
	if err == nil {
		state.isVisible = attrs.MapState != 0
	}

	if state.keyCombo != "" {
		err = setupKeyboardShortcut()
		if err != nil {
			log.Printf("Warning: Failed to set up keyboard shortcut: %v\n", err)
		} else {
			log.Printf("Keyboard shortcut '%s' registered\n", state.keyCombo)
			go listenForKeyEvents()
		}
	}

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-c
		log.Println("Interrupt received, exiting...")
		cleanupAndExit()
	}()

	log.Printf("Starting system tray for window %s...\n", formatWindowDescription())
	systray.Run(onSystrayReady, onSystrayExit)
}
