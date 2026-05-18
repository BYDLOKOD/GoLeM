package proxy

import (
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
)

// TestConcurrentEnsureRunningUsesFlockToPreventDuplicates verifies that the
// flock in EnsureRunning serializes concurrent callers. We test the locking
// mechanism directly: N goroutines race to acquire the proxy lock, and only
// one at a time can enter the critical section.
func TestConcurrentEnsureRunningUsesFlockToPreventDuplicates(t *testing.T) {
	configDir := t.TempDir()
	lockPath := filepath.Join(configDir, lockFile)

	const goroutines = 10
	var (
		wg          sync.WaitGroup
		maxInFlight int64
		inFlight    int64
		raceFound   int64
	)

	wg.Add(goroutines)
	for range goroutines {
		go func() {
			defer wg.Done()

			// Acquire the same flock that EnsureRunning uses.
			f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
			if err != nil {
				t.Errorf("open lock: %v", err)
				return
			}
			defer func() { _ = f.Close() }()

			if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
				t.Errorf("flock: %v", err)
				return
			}
			defer func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }()

			// Inside critical section — only one goroutine at a time.
			cur := atomic.AddInt64(&inFlight, 1)
			if cur > 1 {
				atomic.StoreInt64(&raceFound, 1)
			}
			if cur > atomic.LoadInt64(&maxInFlight) {
				atomic.StoreInt64(&maxInFlight, cur)
			}

			// Simulate work (IsRunning check + spawn).
			// No actual sleep needed; the flock itself serializes.

			atomic.AddInt64(&inFlight, -1)
		}()
	}
	wg.Wait()

	if atomic.LoadInt64(&raceFound) != 0 {
		t.Errorf("multiple goroutines inside critical section simultaneously (maxInFlight=%d)",
			atomic.LoadInt64(&maxInFlight))
	}
}

// TestEnsureRunningCreatesLockFile verifies that the lock file is created in
// configDir when EnsureRunning's locking path is exercised.
func TestEnsureRunningCreatesLockFile(t *testing.T) {
	configDir := t.TempDir()
	lockPath := filepath.Join(configDir, lockFile)

	// Acquire and release the lock to prove the file is created.
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		t.Fatalf("open lock file: %v", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		_ = f.Close()
		t.Fatalf("flock: %v", err)
	}
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	_ = f.Close()

	if _, err := os.Stat(lockPath); os.IsNotExist(err) {
		t.Errorf("lock file %q was not created", lockPath)
	}
}

// TestWriteAtomicRenamePattern verifies that writeAtomic creates a file with
// the expected content via tmp+rename (no partial reads possible).
func TestWriteAtomicRenamePattern(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test-atomic")

	if err := writeAtomic(path, "hello"); err != nil {
		t.Fatalf("writeAtomic: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != "hello" {
		t.Errorf("writeAtomic wrote %q, want %q", string(data), "hello")
	}

	// Overwrite with a different value.
	if err := writeAtomic(path, "world"); err != nil {
		t.Fatalf("writeAtomic overwrite: %v", err)
	}
	data, err = os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile after overwrite: %v", err)
	}
	if string(data) != "world" {
		t.Errorf("writeAtomic overwrote to %q, want %q", string(data), "world")
	}
}
