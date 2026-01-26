package main

import (
	"flag"
	"fmt"
	"syscall"
	"unsafe"
)

var (
	modUser32         = syscall.NewLazyDLL("user32.dll")
	procFindWindow    = modUser32.NewProc("FindWindowW")
	procSetWindowPos  = modUser32.NewProc("SetWindowPos")
	procGetWindowRect = modUser32.NewProc("GetWindowRect")
	SWP_NOMOVE        = 0x0002
	SWP_NOZORDER      = 0x0004
	SWP_NOACTIVATE    = 0x0010
)

func findWindow(windowName *uint16) (syscall.Handle, error) {
	ret, _, err := procFindWindow.Call(uintptr(unsafe.Pointer(nil)), uintptr(unsafe.Pointer(windowName)))
	if ret == 0 {
		return 0, err
	}
	return syscall.Handle(ret), nil
}

func getWindowRect(hwnd syscall.Handle) (int32, int32, int32, int32, error) {
	var rect struct {
		left, top, right, bottom int32
	}
	ret, _, _ := procGetWindowRect.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&rect)))
	if ret == 0 {
		return 0, 0, 0, 0, fmt.Errorf("error getting window rect")
	}
	return rect.left, rect.top, rect.right, rect.bottom, nil
}

func setWindowPos(hwnd syscall.Handle, x, y, width, height int) {
	procSetWindowPos.Call(
		uintptr(hwnd),
		0,
		uintptr(x),
		uintptr(y),
		uintptr(width),
		uintptr(height),
		uintptr(SWP_NOZORDER|SWP_NOACTIVATE),
	)
}

func main() {
	var windowTitle string
	var x, y int
	flag.StringVar(&windowTitle, "title", "", "Window title to set position")
	flag.IntVar(&x, "x", 0, "Specifies the x-coordinate of the window")
	flag.IntVar(&y, "y", 0, "Specifies the y-coordinate of the window")
	flag.Parse()

	windowName := syscall.StringToUTF16Ptr(windowTitle)

	hwnd, err := findWindow(windowName)
	if err != nil {
		fmt.Println("Error finding window:", err)
		return
	}

	left, top, right, bottom, err := getWindowRect(hwnd)
	if err != nil {
		fmt.Println("Error getting window rect:", err)
		return
	}
	width := right - left
	height := bottom - top

	setWindowPos(hwnd, x, y, int(width), int(height))
}
