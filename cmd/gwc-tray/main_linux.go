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
	conn         *xgb.Conn
	winTitle     string
	winID        string
	targetWin    xproto.Window
	isVisible    bool
	hiddenByTray bool
	keyCombo     string
	keyMods      uint16
	keyCode      xproto.Keycode
	root         xproto.Window
	exitSignal   chan struct{}
	mutex        sync.Mutex
	wg           sync.WaitGroup
	exitOnce     sync.Once
}

var state AppState

var ignoredModifierMasks = []uint16{
	xproto.ModMaskLock,
	xproto.ModMask2,
	xproto.ModMask3,
}

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
		if strings.EqualFold(windowName, title) {
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
		log.Printf("No window found with title exactly matching '%s'\n", title)
		return 0, fmt.Errorf("window not found")
	}

	log.Printf("Found window with title exactly matching '%s'\n", title)
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
		state.hiddenByTray = false
	} else {
		xproto.UnmapWindow(conn, window)
		log.Println("Window unmapped (hidden)")
		state.isVisible = false
		state.hiddenByTray = true
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

func internAtom(name string) (xproto.Atom, error) {
	reply, err := xproto.InternAtom(state.conn, false, uint16(len(name)), name).Reply()
	if err != nil {
		return 0, err
	}
	return reply.Atom, nil
}

func targetSupportsProtocol(protocolsAtom, protocolAtom xproto.Atom) (bool, error) {
	reply, err := xproto.GetProperty(
		state.conn,
		false,
		state.targetWin,
		protocolsAtom,
		xproto.AtomAtom,
		0,
		32,
	).Reply()
	if err != nil {
		return false, err
	}

	for offset := 0; offset+4 <= len(reply.Value); offset += 4 {
		if xproto.Atom(xgb.Get32(reply.Value[offset:])) == protocolAtom {
			return true, nil
		}
	}

	return false, nil
}

func closeTargetWindow() {
	state.mutex.Lock()
	defer state.mutex.Unlock()

	if state.conn == nil || state.targetWin == 0 {
		return
	}

	_, err := xproto.GetWindowAttributes(state.conn, state.targetWin).Reply()
	if err != nil {
		log.Printf("Skipping close request for target window: %v\n", err)
		return
	}

	wmProtocolsAtom, err := internAtom("WM_PROTOCOLS")
	if err != nil {
		log.Printf("Failed to resolve WM_PROTOCOLS atom: %v\n", err)
		return
	}

	wmDeleteWindowAtom, err := internAtom("WM_DELETE_WINDOW")
	if err != nil {
		log.Printf("Failed to resolve WM_DELETE_WINDOW atom: %v\n", err)
		return
	}

	supported, err := targetSupportsProtocol(wmProtocolsAtom, wmDeleteWindowAtom)
	if err != nil {
		log.Printf("Failed to read WM_PROTOCOLS from target window: %v\n", err)
		return
	}

	if supported {
		event := xproto.ClientMessageEvent{
			Format: 32,
			Window: state.targetWin,
			Type:   wmProtocolsAtom,
			Data: xproto.ClientMessageDataUnionData32New([]uint32{
				uint32(wmDeleteWindowAtom),
				0,
				0,
				0,
				0,
			}),
		}

		err = xproto.SendEventChecked(state.conn, false, state.targetWin, 0, string(event.Bytes())).Check()
		if err != nil {
			log.Printf("Failed to send WM_DELETE_WINDOW to target window: %v\n", err)
			return
		}

		log.Println("Requested graceful shutdown of target window")
		return
	}

	err = xproto.DestroyWindowChecked(state.conn, state.targetWin).Check()
	if err != nil {
		log.Printf("Failed to destroy target window without WM_DELETE_WINDOW support: %v\n", err)
		return
	}

	log.Println("Destroyed target window without WM_DELETE_WINDOW support")
}

func cleanupAndExit() {
	state.exitOnce.Do(func() {
		closeTargetWindow()
		close(state.exitSignal)
		systray.Quit()
		if state.conn != nil {
			state.conn.Close()
			state.conn = nil
		}
		state.wg.Wait()
	})
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

	return grabKeyWithModifierVariants()
}

func subscribeTargetWindowLifecycleEvents() error {
	values := []uint32{uint32(xproto.EventMaskStructureNotify)}
	return xproto.ChangeWindowAttributesChecked(
		state.conn,
		state.targetWin,
		xproto.CwEventMask,
		values,
	).Check()
}

func listenForXEvents() {
	state.wg.Add(1)
	defer state.wg.Done()
	for {
		select {
		case <-state.exitSignal:
			return
		default:
			ev, err := state.conn.WaitForEvent()
			if err != nil {
				select {
				case <-state.exitSignal:
					return
				default:
				}
				log.Printf("Error waiting for X event: %v\n", err)
				continue
			}

			switch e := ev.(type) {
			case xproto.KeyPressEvent:
				if state.keyCombo != "" && e.Detail == state.keyCode && normalizeEventModifiers(e.State) == state.keyMods {
					log.Println("Shortcut detected, toggling window visibility")
					toggleWindowVisibility()
				}
			case xproto.DestroyNotifyEvent:
				if e.Window == state.targetWin {
					log.Println("Target window closed, exiting tray")
					cleanupAndExit()
					return
				}
			}
		}
	}
}

func grabKeyWithModifierVariants() error {
	variants := expandedModifierVariants(state.keyMods)
	for _, mods := range variants {
		err := xproto.GrabKeyChecked(
			state.conn,
			true,
			state.root,
			mods,
			state.keyCode,
			xproto.GrabModeAsync,
			xproto.GrabModeAsync,
		).Check()
		if err != nil {
			return err
		}
	}
	return nil
}

func expandedModifierVariants(base uint16) []uint16 {
	variants := []uint16{base}
	for _, optional := range ignoredModifierMasks {
		currentLen := len(variants)
		for i := 0; i < currentLen; i++ {
			variant := variants[i] | optional
			variants = append(variants, variant)
		}
	}
	return dedupeVariants(variants)
}

func dedupeVariants(variants []uint16) []uint16 {
	seen := make(map[uint16]struct{}, len(variants))
	unique := make([]uint16, 0, len(variants))
	for _, v := range variants {
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		unique = append(unique, v)
	}
	return unique
}

func normalizeEventModifiers(mods uint16) uint16 {
	for _, mask := range ignoredModifierMasks {
		mods &^= mask
	}
	return mods
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
	state.root = xproto.Setup(state.conn).DefaultScreen(state.conn).Root

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
		state.conn.Close()
		return
	}

	attrs, err := xproto.GetWindowAttributes(state.conn, state.targetWin).Reply()
	if err == nil {
		state.isVisible = attrs.MapState != 0
	}

	err = subscribeTargetWindowLifecycleEvents()
	if err != nil {
		log.Printf("Warning: Failed to subscribe to target window lifecycle events: %v\n", err)
	}

	if state.keyCombo != "" {
		err = setupKeyboardShortcut()
		if err != nil {
			log.Printf("Warning: Failed to set up keyboard shortcut: %v\n", err)
		} else {
			log.Printf("Keyboard shortcut '%s' registered\n", state.keyCombo)
		}
	}

	go listenForXEvents()

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
