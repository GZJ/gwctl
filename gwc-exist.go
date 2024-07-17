package main

import (
	"flag"
	"fmt"
	"syscall"
	"unsafe"
)

var (
	modUser32        = syscall.NewLazyDLL("user32.dll")
	procFindWindowEx = modUser32.NewProc("FindWindowExW")
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

func windowExists(windowName string) bool {
	windowNamePtr, _ := syscall.UTF16PtrFromString(windowName)

	hwnd, err := findWindowEx(0, 0, nil, windowNamePtr)
	if err != nil {
		//fmt.Println("Error finding window:", err)
		return false
	}

	return hwnd != 0
}

func main() {
	windowTitle := flag.String("title", "", "Window title to check")
	flag.Parse()

	if *windowTitle == "" {
		flag.PrintDefaults()
		fmt.Println(1)
		return
	}

	exists := windowExists(*windowTitle)
	if exists {
		//fmt.Println("Window exists!")
		fmt.Print(0)
	} else {
		//fmt.Println("Window does not exist.")
		fmt.Print(1)
	}
}
