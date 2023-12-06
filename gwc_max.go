package main

import (
	"flag"
	"fmt"
	"syscall"
	"unsafe"
)

var (
	modUser32      = syscall.NewLazyDLL("user32.dll")
	procFindWindow = modUser32.NewProc("FindWindowW")
	procShowWindow = modUser32.NewProc("ShowWindow")
	SW_MAXIMIZE    = 3
)

func findWindow(windowName *uint16) (syscall.Handle, error) {
	ret, _, err := procFindWindow.Call(uintptr(unsafe.Pointer(nil)), uintptr(unsafe.Pointer(windowName)))
	if ret == 0 {
		return 0, err
	}
	return syscall.Handle(ret), nil
}

func maximizeWindow(hwnd syscall.Handle) {
	procShowWindow.Call(uintptr(hwnd), uintptr(SW_MAXIMIZE))
}

func main() {
	var windowTitle string
	flag.StringVar(&windowTitle, "title", "", "Window title to find and maximize")
	flag.Parse()

	windowName := syscall.StringToUTF16Ptr(windowTitle)

	hwnd, err := findWindow(windowName)
	if err != nil {
		fmt.Println("Error finding window:", err)
		return
	}

	maximizeWindow(hwnd)
}
