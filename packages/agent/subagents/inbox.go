package subagents

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

// Inbox is the supervisor-side handle on a per-agent unix socket.
// The wire format is newline-delimited, versioned JSON commands. The child
// listens on the same socket via Listener (below); the parent never reads
// from it.
//
// Why a socket and not a FIFO: FIFOs are POSIX-only and have
// awkward blocking-open semantics. A unix socket gives us:
//
//   - the ability to fail fast if no child is listening yet,
//     so the supervisor gets a useful error to surface in the TUI;
//   - clean shutdown semantics when the child closes the
//     listener;
//   - works under tests with a temp dir; and
//   - portable enough; Windows support is a follow-up that can
//     swap in a named pipe behind this same interface.
type Inbox struct {
	path string

	mu   sync.Mutex
	conn net.Conn // lazily dialed; one persistent connection per agent
}

// NewInbox returns a handle that will dial the socket at path on
// the first SendCommand call. The socket file is expected to be
// created by the child (Listener.Open below). This split lets the
// parent build the Inbox before the child has booted; sends that
// happen before the child is ready get a clear "not yet" error
// the caller can retry or surface.
func NewInbox(path string) *Inbox { return &Inbox{path: path} }

// Path returns the absolute socket path. Used by the runner to
// pass the path to the child as a flag.
func (b *Inbox) Path() string { return b.path }

// SendCommand sends a versioned JSONL command.
func (b *Inbox) SendCommand(command Envelope) error {
	line, err := MarshalJSONL(command)
	if err != nil {
		return err
	}
	return b.sendBytes(line)
}

func (b *Inbox) sendBytes(data []byte) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.conn == nil {
		c, err := dialUnix(b.path, 200*time.Millisecond)
		if err != nil {
			return err
		}
		b.conn = c
	}
	if _, err := b.conn.Write(data); err != nil {
		// Drop the connection so the next call redials. The
		// previous error is more informative than the redial's
		// would be, so surface this one.
		_ = b.conn.Close()
		b.conn = nil
		return err
	}
	return nil
}

// Close drops any persistent connection. Safe to call repeatedly.
// Does not unlink the socket file — that's the child's job
// (Listener.Close).
func (b *Inbox) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.conn != nil {
		err := b.conn.Close()
		b.conn = nil
		return err
	}
	return nil
}

// ErrNotReady is returned when the child hasn't yet
// opened its listener. Callers can retry after a short backoff;
// the TUI surfaces it as "agent <id> not listening yet".
var ErrNotReady = errors.New("subagent: agent inbox not ready")

// dialUnix wraps net.DialTimeout with a small retry window so the
// happy path "child just started, supervisor sends right away"
// works without forcing the caller to poll.
//
// Errors collapse onto ErrNotReady whenever the failure means "no
// one is listening": either the socket file doesn't exist (child
// hasn't booted) or it does exist but the dial was refused (the
// previous child exited and left a stale inode). Both are
// recoverable by Supervisor.Resume, and both should surface as the same
// user-facing hint rather than as a path-leaking error string.
func dialUnix(path string, timeout time.Duration) (net.Conn, error) {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for {
		c, err := net.DialTimeout("unix", path, 50*time.Millisecond)
		if err == nil {
			return c, nil
		}
		lastErr = err
		if time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, statErr := os.Stat(path); os.IsNotExist(statErr) {
		return nil, ErrNotReady
	}
	if isNoListenerErr(lastErr) {
		return nil, ErrNotReady
	}
	return nil, fmt.Errorf("subagent: dial %s: %w", path, lastErr)
}

// isNoListenerErr reports whether err means "the socket file exists
// but no process is listening on it" — i.e. ECONNREFUSED on unix
// domain sockets. We pattern-match on errno via errors.Is rather
// than the error string so platform variants (linux vs darwin) all
// fold to the same case.
func isNoListenerErr(err error) bool {
	return errors.Is(err, syscall.ECONNREFUSED)
}

// inboxLive reports whether a process currently accepts connections on path.
// A Unix socket inode can survive its owner, so os.Stat alone cannot
// distinguish a live worker from a stale path. Callers use this probe before
// resume/remove operations to avoid racing a detached worker's session and
// workspace ownership.
const inboxProbeLine = "__zot_subagent_probe__"

func inboxLive(path string) bool {
	if path == "" {
		return false
	}
	conn, err := net.DialTimeout("unix", path, 100*time.Millisecond)
	if err != nil {
		return false
	}
	// Listener treats this first line as a health probe and does not
	// replace the current supervisor connection. A bare connect would be
	// enough to detect liveness, but sending an explicit probe lets the
	// listener distinguish this side-effect-free check from a takeover.
	_, writeErr := conn.Write([]byte(inboxProbeLine + "\n"))
	_ = conn.Close()
	return writeErr == nil
}

// Listener is the child-side end of the inbox. The subagent-worker
// daemon mode constructs one with Listen, then iterates Lines
// to receive supervisor messages. Designed so a single goroutine
// in the child can drive prompting without juggling raw net code.
type Listener struct {
	path string
	ln   net.Listener
	// active is the most recent accepted connection. The protocol
	// only expects one supervisor (the parent zot), so newer
	// connections preempt older ones — the previous parent
	// presumably crashed.
	mu        sync.Mutex
	active    net.Conn
	conns     map[net.Conn]struct{}
	out       chan string
	done      chan struct{}
	wg        sync.WaitGroup
	closed    bool
	closeOnce sync.Once
}

// Listen creates the socket at path and starts accepting. The
// caller must call Close to remove the socket file on shutdown.
// A live endpoint is preserved and rejected; only a refused probe
// is treated as a stale socket eligible for removal.
func Listen(path string) (*Listener, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("inbox dir: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return nil, fmt.Errorf("inbox dir permissions: %w", err)
	}
	// Probe an existing endpoint before removing it. Unlinking an active
	// Unix socket does not stop its listener, so blindly removing the path
	// could let Resume start a second worker on the same session and event log.
	if _, statErr := os.Stat(path); statErr == nil {
		if inboxLive(path) {
			return nil, fmt.Errorf("subagent inbox already has a live listener")
		}
		// A refused probe means the socket inode is stale. Remove only that
		// endpoint; a live listener was handled above without unlinking it.
		_ = os.Remove(path)
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, fmt.Errorf("inbox listen: %w", err)
	}
	// Unix sockets inherit the process umask but may otherwise be
	// world-writable. The inbox carries prompts and control commands,
	// so fail closed for other local users.
	_ = os.Chmod(path, 0o600)
	l := &Listener{
		path:  path,
		ln:    ln,
		conns: make(map[net.Conn]struct{}),
		out:   make(chan string, 16),
		done:  make(chan struct{}),
	}
	go l.acceptLoop()
	return l, nil
}

// Lines returns a channel of newline-stripped messages received
// from the supervisor. The channel closes when Close is called.
func (l *Listener) Lines() <-chan string { return l.out }

// Close stops accepting, drops the active connection, removes
// the socket file, and closes Lines. Idempotent.
func (l *Listener) Close() error {
	l.closeOnce.Do(func() {
		l.mu.Lock()
		l.closed = true
		close(l.done)
		for conn := range l.conns {
			_ = conn.Close()
		}
		l.mu.Unlock()
		_ = l.ln.Close()
		_ = os.Remove(l.path)
	})
	return nil
}

func (l *Listener) acceptLoop() {
	defer func() {
		l.wg.Wait()
		close(l.out)
	}()
	for {
		c, err := l.ln.Accept()
		if err != nil {
			select {
			case <-l.done:
				return
			default:
				// Accept can fail transiently (signal); back off briefly.
				time.Sleep(20 * time.Millisecond)
				continue
			}
		}
		l.mu.Lock()
		if l.closed {
			l.mu.Unlock()
			_ = c.Close()
			return
		}
		l.conns[c] = struct{}{}
		l.wg.Add(1)
		l.mu.Unlock()
		go func() {
			defer l.wg.Done()
			l.readLoop(c)
		}()
	}
}

func (l *Listener) readLoop(c net.Conn) {
	defer func() {
		_ = c.Close()
		l.mu.Lock()
		delete(l.conns, c)
		if l.active == c {
			l.active = nil
		}
		l.mu.Unlock()
	}()

	br := bufio.NewReader(c)
	firstLine := true
	for {
		line, err := br.ReadString('\n')
		if line != "" {
			message := trimRightNL(line)
			if firstLine {
				firstLine = false
				if message == inboxProbeLine {
					return
				}
				// The first real command claims ownership. Health probes do
				// not preempt the current supervisor, while a replacement
				// supervisor still gets the historical takeover behavior.
				l.mu.Lock()
				if l.active != nil && l.active != c {
					_ = l.active.Close()
				}
				l.active = c
				l.mu.Unlock()
			}
			select {
			case l.out <- message:
			case <-l.done:
				return
			}
		}
		if err != nil {
			return
		}
	}
}

func trimRightNL(s string) string {
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}
