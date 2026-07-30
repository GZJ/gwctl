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
	targetSpecs  []targetSpec
	targetWin    xproto.Window
	targetWins   []xproto.Window
	hiddenPos    map[xproto.Window]windowPosition
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

type windowPosition struct {
	x int32
	y int32
}

type targetSpec struct {
	kind  string
	value string
}

type targetFlag struct {
	kind  string
	specs *[]targetSpec
}

func (f targetFlag) String() string { return "" }

func (f targetFlag) Set(value string) error {
	*f.specs = append(*f.specs, targetSpec{kind: f.kind, value: value})
	return nil
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
		hiddenPos:  make(map[xproto.Window]windowPosition),
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

func windowsInVisibilityOrder(windows []xproto.Window, visible bool) []xproto.Window {
	ordered := append([]xproto.Window(nil), windows...)
	if visible {
		for left, right := 0, len(ordered)-1; left < right; left, right = left+1, right-1 {
			ordered[left], ordered[right] = ordered[right], ordered[left]
		}
	}
	return ordered
}

func setWindowsVisibility(conn *xgb.Conn, windows []xproto.Window, visible bool) {
	ordered := windowsInVisibilityOrder(windows, visible)
	for _, window := range ordered {
		if visible {
			xproto.MapWindow(conn, window)
			restoreWindowPosition(conn, window)
			log.Printf("Window 0x%x mapped (shown)\n", window)
		} else {
			rememberWindowPosition(conn, window)
			xproto.UnmapWindow(conn, window)
			log.Printf("Window 0x%x unmapped (hidden)\n", window)
		}
	}
	xproto.GetInputFocus(conn).Reply() // Flush all map/unmap requests.

	state.mutex.Lock()
	state.isVisible = visible
	state.hiddenByTray = !visible
	state.mutex.Unlock()
}

func atomByName(conn *xgb.Conn, name string) (xproto.Atom, error) {
	reply, err := xproto.InternAtom(conn, false, uint16(len(name)), name).Reply()
	if err != nil {
		return 0, err
	}
	return reply.Atom, nil
}

func currentWindowPosition(conn *xgb.Conn, window xproto.Window) (windowPosition, error) {
	root := xproto.Setup(conn).DefaultScreen(conn).Root
	coordinates, err := xproto.TranslateCoordinates(conn, window, root, 0, 0).Reply()
	if err != nil {
		return windowPosition{}, err
	}

	position := windowPosition{x: int32(coordinates.DstX), y: int32(coordinates.DstY)}
	frameExtentsAtom, err := atomByName(conn, "_NET_FRAME_EXTENTS")
	if err != nil {
		return position, nil
	}
	extents, err := xproto.GetProperty(conn, false, window, frameExtentsAtom, xproto.AtomCardinal, 0, 4).Reply()
	if err == nil && len(extents.Value) >= 16 {
		position.x -= int32(xgb.Get32(extents.Value[0:4]))
		position.y -= int32(xgb.Get32(extents.Value[8:12]))
	}
	return position, nil
}

func rememberWindowPosition(conn *xgb.Conn, window xproto.Window) {
	position, err := currentWindowPosition(conn, window)
	if err != nil {
		log.Printf("Failed to remember position for window 0x%x: %v\n", window, err)
		return
	}
	state.mutex.Lock()
	if state.hiddenPos == nil {
		state.hiddenPos = make(map[xproto.Window]windowPosition)
	}
	state.hiddenPos[window] = position
	state.mutex.Unlock()
}

func restoreWindowPosition(conn *xgb.Conn, window xproto.Window) {
	state.mutex.Lock()
	position, ok := state.hiddenPos[window]
	if ok {
		delete(state.hiddenPos, window)
	}
	state.mutex.Unlock()
	if !ok {
		return
	}

	moveResizeAtom, err := atomByName(conn, "_NET_MOVERESIZE_WINDOW")
	if err != nil {
		log.Printf("Failed to resolve _NET_MOVERESIZE_WINDOW for 0x%x: %v\n", window, err)
		return
	}
	event := xproto.ClientMessageEvent{
		Format: 32,
		Window: window,
		Type:   moveResizeAtom,
		Data: xproto.ClientMessageDataUnionData32New([]uint32{
			(1 << 8) | (1 << 9) | (1 << 12), // X, Y, source=application.
			uint32(position.x), uint32(position.y), 0, 0,
		}),
	}
	root := xproto.Setup(conn).DefaultScreen(conn).Root
	mask := uint32(xproto.EventMaskSubstructureRedirect | xproto.EventMaskSubstructureNotify)
	if err := xproto.SendEventChecked(conn, false, root, mask, string(event.Bytes())).Check(); err != nil {
		log.Printf("Failed to restore position for window 0x%x: %v\n", window, err)
	}
}

func toggleWindowVisibility() {
	state.mutex.Lock()
	windows := append([]xproto.Window(nil), state.targetWins...)
	state.mutex.Unlock()
	if len(windows) == 0 {
		log.Printf("No target windows are currently available\n")
		return
	}

	isCurrentlyVisible := false
	for _, window := range windows {
		attrs, err := xproto.GetWindowAttributes(state.conn, window).Reply()
		if err == nil && attrs.MapState != xproto.MapStateUnmapped {
			isCurrentlyVisible = true
			break
		}
	}
	setWindowsVisibility(state.conn, windows, !isCurrentlyVisible)

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
	if len(state.targetSpecs) == 1 {
		spec := state.targetSpecs[0]
		if spec.kind == "title" {
			return fmt.Sprintf("'%s'", spec.value)
		}
		return fmt.Sprintf("window %s", spec.value)
	}
	return fmt.Sprintf("%d windows", len(state.targetSpecs))
}

func resolveTargetWindows(conn *xgb.Conn, specs []targetSpec) ([]xproto.Window, error) {
	windows := make([]xproto.Window, 0, len(specs))
	seen := make(map[xproto.Window]struct{}, len(specs))
	for _, spec := range specs {
		var (
			window xproto.Window
			err    error
		)
		switch spec.kind {
		case "title":
			window, err = findWindowByTitle(conn, spec.value)
		case "id":
			window, err = findWindowByID(conn, spec.value)
		default:
			err = fmt.Errorf("unknown target type %q", spec.kind)
		}
		if err != nil {
			return nil, fmt.Errorf("resolve -%s %q: %w", spec.kind, spec.value, err)
		}
		if _, exists := seen[window]; exists {
			continue
		}
		seen[window] = struct{}{}
		windows = append(windows, window)
	}
	return windows, nil
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
	systray.SetTitle(formatWindowDescription())
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

func targetSupportsProtocol(window xproto.Window, protocolsAtom, protocolAtom xproto.Atom) (bool, error) {
	reply, err := xproto.GetProperty(
		state.conn,
		false,
		window,
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

	if state.conn == nil || len(state.targetWins) == 0 {
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

	for _, window := range state.targetWins {
		if _, err = xproto.GetWindowAttributes(state.conn, window).Reply(); err != nil {
			log.Printf("Skipping close request for window 0x%x: %v\n", window, err)
			continue
		}

		supported, protocolErr := targetSupportsProtocol(window, wmProtocolsAtom, wmDeleteWindowAtom)
		if protocolErr != nil {
			log.Printf("Failed to read WM_PROTOCOLS from window 0x%x: %v\n", window, protocolErr)
			continue
		}

		if supported {
			event := xproto.ClientMessageEvent{
				Format: 32,
				Window: window,
				Type:   wmProtocolsAtom,
				Data: xproto.ClientMessageDataUnionData32New([]uint32{
					uint32(wmDeleteWindowAtom), 0, 0, 0, 0,
				}),
			}
			if sendErr := xproto.SendEventChecked(state.conn, false, window, 0, string(event.Bytes())).Check(); sendErr != nil {
				log.Printf("Failed to send WM_DELETE_WINDOW to window 0x%x: %v\n", window, sendErr)
			} else {
				log.Printf("Requested graceful shutdown of window 0x%x\n", window)
			}
			continue
		}

		if destroyErr := xproto.DestroyWindowChecked(state.conn, window).Check(); destroyErr != nil {
			log.Printf("Failed to destroy window 0x%x: %v\n", window, destroyErr)
		} else {
			log.Printf("Destroyed window 0x%x without WM_DELETE_WINDOW support\n", window)
		}
	}
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
	for _, window := range state.targetWins {
		if err := xproto.ChangeWindowAttributesChecked(
			state.conn, window, xproto.CwEventMask, values,
		).Check(); err != nil {
			return err
		}
	}
	return nil
}

func removeTargetWindow(window xproto.Window) (removed, empty bool) {
	state.mutex.Lock()
	defer state.mutex.Unlock()

	remaining := state.targetWins[:0]
	for _, target := range state.targetWins {
		if target == window {
			removed = true
			continue
		}
		remaining = append(remaining, target)
	}
	if !removed {
		return false, len(state.targetWins) == 0
	}
	delete(state.hiddenPos, window)
	state.targetWins = remaining
	if len(remaining) > 0 {
		state.targetWin = remaining[0]
	} else {
		state.targetWin = 0
	}
	return true, len(remaining) == 0
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
				if removed, empty := removeTargetWindow(e.Window); removed && empty {
					log.Println("All target windows closed, exiting tray")
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

	flag.Var(targetFlag{kind: "title", specs: &state.targetSpecs}, "title", "Window title to control (repeatable)")
	flag.Var(targetFlag{kind: "id", specs: &state.targetSpecs}, "id", "Window ID to control (repeatable; decimal or hex with 0x prefix)")
	flag.StringVar(&state.keyCombo, "key", "", "Keyboard shortcut (e.g., 'ctrl+shift+alt+a')")
	flag.Parse()

	if len(state.targetSpecs) == 0 {
		fmt.Println("Error: At least one -title or -id must be specified")
		fmt.Println("Usage: ")
		fmt.Println("  To control by title: go run main.go -title \"Firefox\" [-key \"ctrl+shift+alt+a\"]")
		fmt.Println("  To control by ID:    go run main.go -id 0x1234567 [-key \"ctrl+shift+alt+a\"]")
		fmt.Println("  Multiple targets:    go run main.go -title \"Alacritty 1\" -title \"Alacritty 2\" -id 0x1234567")
		return
	}

	var err error
	state.conn, err = xgb.NewConn()
	if err != nil {
		log.Fatalf("Cannot open display: %v\n", err)
		return
	}
	state.root = xproto.Setup(state.conn).DefaultScreen(state.conn).Root

	state.targetWins, err = resolveTargetWindows(state.conn, state.targetSpecs)

	if err != nil {
		fmt.Fprintf(os.Stderr, "Window not found: %v\n", err)
		fmt.Println("\nTip: The window might be:")
		fmt.Println("1. Not currently open")
		fmt.Println("2. Using a different title than expected")
		fmt.Println("3. Not accessible to this program")
		listWindows(state.conn)
		state.conn.Close()
		return
	}
	state.targetWin = state.targetWins[0]

	for _, window := range state.targetWins {
		attrs, attrErr := xproto.GetWindowAttributes(state.conn, window).Reply()
		if attrErr == nil && attrs.MapState != xproto.MapStateUnmapped {
			state.isVisible = true
			break
		}
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
	cleanupAndExit()
	state.wg.Wait()
}
