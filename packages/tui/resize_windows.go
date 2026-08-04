//go:build windows

package tui

import (
	"os"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Windows doesn't have SIGWINCH; resize events are signaled via input
// records. For v1 we just ignore resize and rely on the next render
// cycle to pick up the new size via term.GetSize.
func (p *ProcTerm) installResizeHandler() {}

func (p *ProcTerm) SetNonblock(enable bool) error { return nil }

// peekStdin waits for one byte on a supported Windows stdin handle without
// consuming anything unless a byte is available. Console input handles can be
// waited on directly. Pipes are checked with PeekNamedPipe, which does not
// consume data; polling that non-consuming check keeps the wait bounded.
//
// Disk files and other character handles are deliberately unsupported. A
// speculative os.File.Read on either can block (or consume a byte that cannot
// be put back safely), so those handles fail closed instead of corrupting the
// escape parser. In particular, do not replace this with a blocking Read
// fallback: an isolated Esc must remain observable within d.
func peekStdin(in *os.File, d time.Duration) (byte, bool, error) {
	handle := windows.Handle(in.Fd())
	fileType, err := windows.GetFileType(handle)
	if err != nil {
		return 0, false, err
	}

	switch fileType {
	case windows.FILE_TYPE_CHAR:
		// FILE_TYPE_CHAR also includes devices such as NUL. GetConsoleMode
		// distinguishes an actual console input handle from those devices.
		var mode uint32
		if err := windows.GetConsoleMode(handle, &mode); err != nil {
			return 0, false, windows.ERROR_INVALID_FUNCTION
		}
		return peekConsole(in, handle, d)
	case windows.FILE_TYPE_PIPE:
		return peekPipe(in, handle, d)
	case windows.FILE_TYPE_DISK:
		return 0, false, windows.ERROR_INVALID_FUNCTION
	default:
		return 0, false, windows.ERROR_INVALID_FUNCTION
	}
}

func peekConsole(in *os.File, handle windows.Handle, d time.Duration) (byte, bool, error) {
	event, err := windows.WaitForSingleObject(handle, waitMilliseconds(d))
	if err != nil {
		return 0, false, err
	}
	switch event {
	case uint32(windows.WAIT_TIMEOUT):
		return 0, false, nil
	case windows.WAIT_OBJECT_0:
		var b [1]byte
		n, err := in.Read(b[:])
		if err != nil {
			return 0, false, err
		}
		if n != len(b) {
			return 0, false, windows.ERROR_INVALID_FUNCTION
		}
		return b[0], true, nil
	default:
		return 0, false, windows.ERROR_INVALID_FUNCTION
	}
}

func waitMilliseconds(d time.Duration) uint32 {
	if d <= 0 {
		return 0
	}
	milliseconds := uint64(d / time.Millisecond)
	if d%time.Millisecond != 0 {
		milliseconds++
	}
	const maxWaitMilliseconds = uint64(^uint32(0) - 1) // reserve INFINITE
	if milliseconds > maxWaitMilliseconds {
		return uint32(maxWaitMilliseconds)
	}
	return uint32(milliseconds)
}

var peekNamedPipe = windows.NewLazySystemDLL("kernel32.dll").NewProc("PeekNamedPipe")

func peekPipe(in *os.File, handle windows.Handle, d time.Duration) (byte, bool, error) {
	deadline := time.Now().Add(d)
	for {
		var available uint32
		ok, _, callErr := peekNamedPipe.Call(
			uintptr(handle),
			0, 0, 0,
			uintptr(unsafe.Pointer(&available)),
			0,
		)
		if ok == 0 {
			if callErr != nil {
				return 0, false, callErr
			}
			return 0, false, windows.ERROR_INVALID_FUNCTION
		}
		if available != 0 {
			var b [1]byte
			n, err := in.Read(b[:])
			if err != nil {
				return 0, false, err
			}
			if n != len(b) {
				return 0, false, windows.ERROR_INVALID_FUNCTION
			}
			return b[0], true, nil
		}
		if d <= 0 {
			return 0, false, nil
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return 0, false, nil
		}
		// PeekNamedPipe is a non-consuming availability check. A short
		// sleep avoids a busy loop while the deadline remains authoritative.
		if remaining > time.Millisecond {
			remaining = time.Millisecond
		}
		time.Sleep(remaining)
	}
}
