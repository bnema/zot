//go:build windows

package tui

import (
	"encoding/binary"
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
// be put back safely), so those handles report no data instead of corrupting
// the escape parser. In particular, do not replace this with a blocking Read
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
		return 0, false, nil
	default:
		return 0, false, nil
	}
}

const consoleKeyEvent = 0x0001

type consoleInputRecord struct {
	eventType uint16
	_         uint16
	event     [16]byte
}

var (
	peekConsoleInputProc = windows.NewLazySystemDLL("kernel32.dll").NewProc("PeekConsoleInputW")
	readConsoleInputProc = windows.NewLazySystemDLL("kernel32.dll").NewProc("ReadConsoleInputW")
)

func consoleInputRecordCall(proc *windows.LazyProc, handle windows.Handle, record *consoleInputRecord) (uint32, error) {
	if err := proc.Find(); err != nil {
		return 0, err
	}
	var read uint32
	r1, _, callErr := proc.Call(
		uintptr(handle),
		uintptr(unsafe.Pointer(record)),
		1,
		uintptr(unsafe.Pointer(&read)),
	)
	if r1 == 0 {
		if callErr != nil && callErr != windows.ERROR_SUCCESS {
			return 0, callErr
		}
		return 0, windows.ERROR_INVALID_FUNCTION
	}
	return read, nil
}

func peekConsole(in *os.File, handle windows.Handle, d time.Duration) (byte, bool, error) {
	deadline := time.Now().Add(d)
	for {
		remaining := d
		if d > 0 {
			remaining = time.Until(deadline)
			if remaining <= 0 {
				return 0, false, nil
			}
		}
		event, err := windows.WaitForSingleObject(handle, waitMilliseconds(remaining))
		if err != nil {
			return 0, false, err
		}
		switch event {
		case uint32(windows.WAIT_TIMEOUT):
			return 0, false, nil
		case windows.WAIT_OBJECT_0:
			var record consoleInputRecord
			count, err := consoleInputRecordCall(peekConsoleInputProc, handle, &record)
			if err != nil {
				return 0, false, err
			}
			if count == 0 {
				if d <= 0 {
					return 0, false, nil
				}
				continue
			}
			if record.eventType == consoleKeyEvent {
				keyDown := binary.LittleEndian.Uint32(record.event[0:4]) != 0
				char := binary.LittleEndian.Uint16(record.event[10:12])
				if keyDown && char != 0 {
					// The callback returns one byte, so leave non-ASCII
					// characters queued for the normal reader rather than
					// consuming them and truncating their encoding.
					if char > 0xff {
						return 0, false, nil
					}
					count, err := consoleInputRecordCall(readConsoleInputProc, handle, &record)
					if err != nil {
						return 0, false, err
					}
					if count != 0 {
						return byte(char), true, nil
					}
					continue
				}
			}
			// Consume non-character, key-up, and non-printing records so
			// a ready console handle cannot make the next wait spin.
			if _, err := consoleInputRecordCall(readConsoleInputProc, handle, &record); err != nil {
				return 0, false, err
			}
			if d <= 0 || time.Until(deadline) <= 0 {
				return 0, false, nil
			}
			continue
		default:
			return 0, false, windows.ERROR_INVALID_FUNCTION
		}
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
	if err := peekNamedPipe.Find(); err != nil {
		return 0, false, err
	}
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
			if callErr != nil && callErr != windows.ERROR_SUCCESS {
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
