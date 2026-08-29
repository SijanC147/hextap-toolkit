//go:build darwin

package inventory

import (
	"os"
	"syscall"
	"unsafe"
)

func fileIsTerminal(file *os.File) bool {
	var attributes syscall.Termios
	_, _, errno := syscall.Syscall6(
		syscall.SYS_IOCTL,
		file.Fd(),
		uintptr(syscall.TIOCGETA),
		uintptr(unsafe.Pointer(&attributes)),
		0,
		0,
		0,
	)
	return errno == 0
}
