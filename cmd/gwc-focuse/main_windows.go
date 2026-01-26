package main

import (
	"flag"
	"fmt"
	"syscall"
	"unsafe"
)

var (
	modUser32               = syscall.NewLazyDLL("user32.dll")
	procFindWindowEx        = modUser32.NewProc("FindWindowExW")
	procSetForegroundWindow = modUser32.NewProc("SetForegroundWindow")
)

func findWindowEx(parentHwnd syscall.Handle, childAfter syscall.Handle, className, windowName *uint16) (syscall.Handle, error) {
	ret, _, err := procFindWindowEx.Call(
		uintptr(parentHwnd),
		uintptr(childAfter),
		uintptr(unsafe.Pointer(className)),
		uintptr(unsafe.Pointer(windowName)),
	)
	if ret == 0 {
		return 0, err
	}
	return syscall.Handle(ret), nil
}

func setForegroundWindow(hwnd syscall.Handle) {
	procSetForegroundWindow.Call(uintptr(hwnd))
}

func main() {
	windowTitle := flag.String("title", "", "Window title to focus")

	flag.Parse()

	parentHwnd := syscall.Handle(0) // 0 for the desktop window
	childAfter := syscall.Handle(0) // 0 to start the search from the beginning of the Z order
	windowName := syscall.StringToUTF16Ptr(*windowTitle)

	hwnd, err := findWindowEx(parentHwnd, childAfter, nil, windowName)
	if err != nil {
		fmt.Println("Error finding window:", err)
		return
	}

	setForegroundWindow(hwnd)
}
