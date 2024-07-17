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
	procSetWindowLong = modUser32.NewProc("SetWindowLongW")
	procGetWindowLong = modUser32.NewProc("GetWindowLongW")
	GWL_EXSTYLE       = -20
	WS_EX_APPWINDOW   = 0x00040000
	WS_EX_TOOLWINDOW  = 0x00000080
)

func findWindow(windowName *uint16) (syscall.Handle, error) {
	ret, _, err := procFindWindow.Call(uintptr(unsafe.Pointer(nil)), uintptr(unsafe.Pointer(windowName)))
	if ret == 0 {
		return 0, err
	}
	return syscall.Handle(ret), nil
}

func showWindowAltTab(hwnd syscall.Handle) error {
	style, _, _ := procGetWindowLong.Call(uintptr(hwnd), uintptr(GWL_EXSTYLE))
	if style == 0 {
		return fmt.Errorf("failed to get window style")
	}
	_, _, err := procSetWindowLong.Call(uintptr(hwnd), uintptr(GWL_EXSTYLE), (style|uintptr(WS_EX_APPWINDOW)) &^ uintptr(WS_EX_TOOLWINDOW))
	if err != syscall.Errno(0) {
		return fmt.Errorf("failed to set window style: %v", err)
	}
	return nil
}

func main() {
	var windowTitle string
	flag.StringVar(&windowTitle, "title", "", "Window title to find and show in Alt+Tab")
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

	if hwnd == 0 {
		fmt.Println("Window not found.")
		return
	}

	err = showWindowAltTab(hwnd)
	if err != nil {
		fmt.Println("Error showing window in Alt+Tab:", err)
		return
	}

	fmt.Println("Window shown in Alt+Tab successfully.")
}
