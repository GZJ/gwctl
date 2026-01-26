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
	procMoveWindow = modUser32.NewProc("MoveWindow")
)

func findWindow(windowName *uint16) (syscall.Handle, error) {
	ret, _, err := procFindWindow.Call(uintptr(unsafe.Pointer(nil)), uintptr(unsafe.Pointer(windowName)))
	if ret == 0 {
		return 0, err
	}
	return syscall.Handle(ret), nil
}

func moveResizeWindow(hwnd syscall.Handle, x, y, width, height int32) {
	procMoveWindow.Call(uintptr(hwnd), uintptr(x), uintptr(y), uintptr(width), uintptr(height), 1)
}

func main() {
	var windowTitle string
	var x, y, width, height int
	flag.StringVar(&windowTitle, "title", "", "Window title to find and move/resize")
	flag.IntVar(&x, "x", 0, "X position")
	flag.IntVar(&y, "y", 0, "Y position")
	flag.IntVar(&width, "width", 800, "Width")
	flag.IntVar(&height, "height", 600, "Height")
	flag.Parse()

	windowName := syscall.StringToUTF16Ptr(windowTitle)

	hwnd, err := findWindow(windowName)
	if err != nil {
		fmt.Println("Error finding window:", err)
		return
	}

	moveResizeWindow(hwnd, int32(x), int32(y), int32(width), int32(height))
}
