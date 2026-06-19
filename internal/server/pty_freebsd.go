//go:build freebsd

package server

import (
	"fmt"
	"os"
	"runtime"
	"syscall"
	"unsafe"
)

// FreeBSD support is EXPERIMENTAL. The binary builds and core remote execution
// (exec / rpc / forward / tunnel / facts) works. Server-side interactive PTY
// allocation is not yet wired on FreeBSD, so an interactive remote shell returns
// a clear error; local terminal raw-mode (BSD termios) is implemented.

func openPty() (*os.File, *os.File, error) {
	return nil, nil, fmt.Errorf("interactive PTY not supported on %s yet (experimental build); use exec/run instead", runtime.GOOS)
}

type winsize struct {
	Row    uint16
	Col    uint16
	Xpixel uint16
	Ypixel uint16
}

func setWinsize(f *os.File, rows, cols int) {
	ws := &winsize{Row: uint16(rows), Col: uint16(cols)}
	syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), syscall.TIOCSWINSZ, uintptr(unsafe.Pointer(ws)))
}

func getWinsize() (int, int) {
	ws := &winsize{}
	syscall.Syscall(syscall.SYS_IOCTL, os.Stdout.Fd(), syscall.TIOCGWINSZ, uintptr(unsafe.Pointer(ws)))
	if ws.Row == 0 {
		return 24, 80
	}
	return int(ws.Row), int(ws.Col)
}

type termState struct {
	termios syscall.Termios
}

func makeRaw(fd int) (*termState, error) {
	var oldState termState
	_, _, errno := syscall.Syscall6(syscall.SYS_IOCTL, uintptr(fd), syscall.TIOCGETA, uintptr(unsafe.Pointer(&oldState.termios)), 0, 0, 0)
	if errno != 0 {
		return nil, errno
	}
	n := oldState.termios
	n.Iflag &^= syscall.IGNBRK | syscall.BRKINT | syscall.PARMRK | syscall.ISTRIP | syscall.INLCR | syscall.IGNCR | syscall.ICRNL | syscall.IXON
	n.Oflag &^= syscall.OPOST
	n.Lflag &^= syscall.ECHO | syscall.ECHONL | syscall.ICANON | syscall.ISIG | syscall.IEXTEN
	n.Cflag &^= syscall.CSIZE | syscall.PARENB
	n.Cflag |= syscall.CS8
	n.Cc[syscall.VMIN] = 1
	n.Cc[syscall.VTIME] = 0
	_, _, errno = syscall.Syscall6(syscall.SYS_IOCTL, uintptr(fd), syscall.TIOCSETA, uintptr(unsafe.Pointer(&n)), 0, 0, 0)
	if errno != 0 {
		return nil, errno
	}
	return &oldState, nil
}

func restoreTerminal(fd int, state *termState) {
	if state == nil {
		return
	}
	syscall.Syscall6(syscall.SYS_IOCTL, uintptr(fd), syscall.TIOCSETA, uintptr(unsafe.Pointer(&state.termios)), 0, 0, 0)
}
