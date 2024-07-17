package main

import (
	"flag"
	"fmt"
	"syscall"
	"unsafe"
)

var (
	modUser32       = syscall.NewLazyDLL("user32.dll")
	procFindWindow  = modUser32.NewProc("FindWindowW")
	procMoveWindow  = modUser32.NewProc("MoveWindow")
	procGetWindowRect = modUser32.NewProc("GetWindowRect")
)

type Rect struct {
	Left, Top, Right, Bottom int32
}

func findWindow(windowName *uint16) (syscall.Handle, error) {
	ret, _, err := procFindWindow.Call(uintptr(unsafe.Pointer(nil)), uintptr(unsafe.Pointer(windowName)))
	if ret == 0 {
		return 0, err
	}
	return syscall.Handle(ret), nil
}

func getWindowRect(hwnd syscall.Handle) (Rect, error) {
	var rect Rect
	ret, _, err := procGetWindowRect.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&rect)))
	if ret == 0 {
		return rect, err
	}
	return rect, nil
}

func resizeWindow(hwnd syscall.Handle, width, height int32) {
	rect, err := getWindowRect(hwnd)
	if err != nil {
		fmt.Println("Error getting window rect:", err)
		return
	}
	x, y := rect.Left, rect.Top
	procMoveWindow.Call(
		uintptr(hwnd),
		uintptr(x),
		uintptr(y),
		uintptr(width),
		uintptr(height),
		uintptr(1),
	)
}

func main() {
	var windowTitle string
	var width, height int
	flag.StringVar(&windowTitle, "title", "", "Window title to find and resize")
	flag.IntVar(&width, "width", 800, "Width of the window")
	flag.IntVar(&height, "height", 600, "Height of the window")
	flag.Parse()

	if windowTitle == "" {
		fmt.Println("Please provide a window title using the -title flag.")
		return
	}

	windowName := syscall.StringToUTF16Ptr(windowTitle)

	hwnd, err := findWindow(windowName)
	if err != nil {
		fmt.Println("Error finding window:", err)
		return
	}

	resizeWindow(hwnd, int32(width), int32(height))
	fmt.Println("Window resized successfully.")
}
