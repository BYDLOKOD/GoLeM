package slot

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	// socketName is the filename for the Unix domain socket used for slot
	// notifications. Placed in the same directory as the counter and lock files.
	socketName = ".slot.sock"

	// fallbackWaitDuration is the sleep duration used when the socket notifier
	// is unavailable (Start failed or not called). Matches the original PollInterval.
	fallbackWaitDuration = PollInterval * time.Second

	// notifySignal is the single byte written to wake waiting goroutines.
	notifySignal byte = 0x01
)

// SlotNotifier provides instant wake-up for goroutines waiting for a slot.
// When a slot is released, Notify() sends a signal to all waiting goroutines
// via a Unix domain socket, replacing the 2-second polling loop.
//
// If the socket cannot be created (permissions, unsupported OS, etc.),
// Wait() falls back to time.Sleep with the original PollInterval.
type SlotNotifier struct {
	dir      string       // directory for the socket file
	sockPath string       // full path to socket file
	mu       sync.Mutex   // protects listener and connections
	listener net.Listener // Unix socket listener
	conns    []net.Conn   // accepted connections from waiting goroutines
	started  bool         // whether Start succeeded
}

// NewSlotNotifier creates a SlotNotifier that will place its socket in dir.
func NewSlotNotifier(dir string) *SlotNotifier {
	return &SlotNotifier{
		dir:      dir,
		sockPath: filepath.Join(dir, socketName),
	}
}

// Start creates and binds the Unix domain socket listener.
// If the socket file already exists (stale from a previous process), it is
// removed before binding. Returns an error if the socket cannot be created;
// callers should use the fallback polling path in that case.
func (n *SlotNotifier) Start() error {
	n.mu.Lock()
	defer n.mu.Unlock()

	// Remove stale socket file if present.
	if _, err := os.Stat(n.sockPath); err == nil {
		_ = os.Remove(n.sockPath)
	}

	// Ensure the directory exists.
	if err := os.MkdirAll(n.dir, 0o755); err != nil {
		return fmt.Errorf("slot notify: create dir: %w", err)
	}

	addr := &net.UnixAddr{Name: n.sockPath, Net: "unix"}
	ln, err := net.ListenUnix("unix", addr)
	if err != nil {
		return fmt.Errorf("slot notify: listen: %w", err)
	}

	n.listener = ln
	n.started = true

	// Background goroutine: accept connections from waiters.
	// Pass ln directly so the goroutine does not race on n.listener.
	go n.acceptLoop(ln)

	return nil
}

// acceptLoop continuously accepts connections from waiting goroutines.
// Connections are stored in n.conns so Notify() can write to them.
// The listener is passed as a parameter to avoid a data race with Stop()
// which sets n.listener to nil under the mutex.
func (n *SlotNotifier) acceptLoop(ln net.Listener) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			// Listener closed (Stop was called).
			return
		}

		n.mu.Lock()
		n.conns = append(n.conns, conn)
		n.mu.Unlock()
	}
}

// Stop closes the listener, all connections, and removes the socket file.
// Stop is idempotent: calling it multiple times is safe.
func (n *SlotNotifier) Stop() {
	n.mu.Lock()
	defer n.mu.Unlock()

	if !n.started {
		return
	}
	n.started = false

	// Close all client connections.
	for _, conn := range n.conns {
		_ = conn.Close()
	}
	n.conns = nil

	// Close the listener.
	if n.listener != nil {
		_ = n.listener.Close()
		n.listener = nil
	}

	// Remove the socket file.
	_ = os.Remove(n.sockPath)
}

// Notify wakes all waiting goroutines by sending a single byte to each
// connected client. Each connection is single-use: after writing the
// notification byte, the connection is removed from n.conns and closed.
// The waiter in Wait() reads the byte and returns; keeping the connection
// open after that would leak it.
//
// Notify is non-blocking: it does not wait for waiters to read the signal.
// If no waiters are connected, Notify is a no-op.
func (n *SlotNotifier) Notify() {
	n.mu.Lock()
	defer n.mu.Unlock()

	if !n.started {
		return
	}

	for _, conn := range n.conns {
		// Set a short write deadline to avoid blocking on broken connections.
		_ = conn.SetWriteDeadline(time.Now().Add(100 * time.Millisecond))
		_, _ = conn.Write([]byte{notifySignal})
		// Each connection is single-use: close it after writing the signal.
		// The waiter's read goroutine will see EOF or the byte and return.
		_ = conn.Close()
	}
	n.conns = n.conns[:0]
}

// Wait blocks until a notification is received or the context is cancelled.
// It connects to the Unix domain socket and blocks reading one byte.
//
// If the notifier has not been started (Start failed or was not called),
// Wait falls back to sleeping for fallbackWaitDuration and returns nil.
func (n *SlotNotifier) Wait(ctx context.Context) error {
	n.mu.Lock()
	started := n.started
	sockPath := n.sockPath
	n.mu.Unlock()

	if !started {
		// Fallback: poll-based waiting.
		return n.fallbackWait(ctx)
	}

	// Connect to the notification socket.
	dialer := net.Dialer{}
	conn, err := dialer.DialContext(ctx, "unix", sockPath)
	if err != nil {
		// Socket unavailable -- fallback to polling.
		log.Printf("[slot] notify: dial failed: %v, falling back to poll", err)
		return n.fallbackWait(ctx)
	}
	defer func() { _ = conn.Close() }()

	// Block reading one byte. The read will return when:
	// 1. Notify() writes the notifySignal byte (success).
	// 2. The connection is closed without writing (notifier stopped -- treat as retry).
	// 3. The context is cancelled (via dialer deadline or manual cancel).
	buf := make([]byte, 1)

	type readResult struct {
		n   int
		err error
	}
	readDone := make(chan readResult, 1)
	go func() {
		n, err := conn.Read(buf)
		readDone <- readResult{n, err}
	}()

	select {
	case res := <-readDone:
		// Notify() writes notifySignal (0x01) then closes the connection.
		// If we read the signal byte before the close, this is a successful wake.
		if res.n > 0 && buf[0] == notifySignal {
			return nil // woken by notification
		}
		// Connection closed without the notify byte (notifier stopped,
		// stale connection, etc). Fall through to retry.
		if res.err != nil && ctx.Err() != nil {
			return ctx.Err()
		}
		// Connection was closed without notification -- retry with a new connection.
		return nil
	case <-ctx.Done():
		// Close the connection to unblock the read goroutine.
		_ = conn.Close()
		// Wait for the read goroutine to exit so it does not leak.
		<-readDone
		return ctx.Err()
	}
}

// fallbackWait sleeps for the original PollInterval duration, respecting
// context cancellation. Used when the socket notifier is unavailable.
func (n *SlotNotifier) fallbackWait(ctx context.Context) error {
	select {
	case <-time.After(fallbackWaitDuration):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
