package slot

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSlotNotifier_WaitUnblocksOnNotify(t *testing.T) {
	dir := t.TempDir()
	n := NewSlotNotifier(dir)
	if err := n.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer n.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Wait in a goroutine.
	waitDone := make(chan error, 1)
	go func() {
		waitDone <- n.Wait(ctx)
	}()

	// Give the waiter time to connect.
	time.Sleep(50 * time.Millisecond)

	// Notify should unblock the waiter.
	n.Notify()

	select {
	case err := <-waitDone:
		if err != nil {
			t.Errorf("Wait returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Wait did not unblock within 2s after Notify")
	}
}

func TestSlotNotifier_WaitReturnsOnContextCancel(t *testing.T) {
	dir := t.TempDir()
	n := NewSlotNotifier(dir)
	if err := n.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer n.Stop()

	ctx, cancel := context.WithCancel(context.Background())

	waitDone := make(chan error, 1)
	go func() {
		waitDone <- n.Wait(ctx)
	}()

	// Cancel after a short delay.
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-waitDone:
		if err == nil {
			t.Error("Wait should return error on context cancel")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Wait did not return after context cancel")
	}
}

func TestSlotNotifier_MultipleWaiters(t *testing.T) {
	dir := t.TempDir()
	n := NewSlotNotifier(dir)
	if err := n.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer n.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	const waiters = 5
	var woke atomic.Int32

	// Start multiple waiters.
	var wg sync.WaitGroup
	for i := 0; i < waiters; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := n.Wait(ctx); err == nil {
				woke.Add(1)
			}
		}()
	}

	// Give waiters time to connect.
	time.Sleep(100 * time.Millisecond)

	// Single notify should wake all waiters.
	n.Notify()

	wg.Wait()

	count := woke.Load()
	if count != waiters {
		t.Errorf("woke = %d, want %d (all waiters should be notified)", count, waiters)
	}
}

func TestSlotNotifier_NotifyWithoutWaitersIsNoOp(t *testing.T) {
	dir := t.TempDir()
	n := NewSlotNotifier(dir)
	if err := n.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer n.Stop()

	// Notify with no waiters should not panic or block.
	n.Notify()
	n.Notify()
	n.Notify()
}

func TestSlotNotifier_StartRemovesStaleSocket(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, ".slot.sock")

	// Create a stale socket file.
	f, err := os.Create(sockPath)
	if err != nil {
		t.Fatalf("create stale file: %v", err)
	}
	_ = f.Close()

	n := NewSlotNotifier(dir)
	if err := n.Start(); err != nil {
		t.Fatalf("Start with stale file: %v", err)
	}
	defer n.Stop()

	// Socket should have been recreated as a real socket.
	if _, err := os.Stat(sockPath); os.IsNotExist(err) {
		t.Error("socket file does not exist after Start")
	}
}

func TestSlotNotifier_StopCleansUpSocket(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, ".slot.sock")

	n := NewSlotNotifier(dir)
	if err := n.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	n.Stop()

	if _, err := os.Stat(sockPath); !os.IsNotExist(err) {
		t.Error("socket file should be removed after Stop")
	}
}

func TestSlotNotifier_StopIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	n := NewSlotNotifier(dir)
	if err := n.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Calling Stop multiple times must not panic.
	n.Stop()
	n.Stop()
	n.Stop()
}

func TestSlotNotifier_FallbackPollingWhenStartFails(t *testing.T) {
	// Use a directory that doesn't exist -- Start will fail.
	dir := "/nonexistent-dir-for-golem-test"
	n := NewSlotNotifier(dir)

	// Start should fail but the notifier should be usable in fallback mode.
	err := n.Start()
	if err == nil {
		n.Stop()
		t.Fatal("expected Start to fail for nonexistent dir")
	}

	// Wait should fall back to a short sleep and return nil.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	start := time.Now()
	if err := n.Wait(ctx); err != nil {
		t.Errorf("fallback Wait returned error: %v", err)
	}
	elapsed := time.Since(start)

	// Fallback wait should be roughly PollInterval (2s).
	if elapsed < 1*time.Second {
		t.Errorf("fallback Wait returned after %v, expected ~2s polling interval", elapsed)
	}
}

func TestSlotNotifier_RapidNotifyDoesNotBlock(t *testing.T) {
	dir := t.TempDir()
	n := NewSlotNotifier(dir)
	if err := n.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer n.Stop()

	// Send many notifications rapidly -- should not block.
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 100; i++ {
			n.Notify()
		}
	}()

	select {
	case <-done:
		// Good -- didn't block.
	case <-time.After(3 * time.Second):
		t.Fatal("rapid Notify calls blocked")
	}
}

func TestWaitForSlot_UsesNotification(t *testing.T) {
	dir := t.TempDir()
	sm := NewSlotManager(dir, 1) // max 1 slot

	if err := sm.StartNotifier(); err != nil {
		t.Fatalf("StartNotifier: %v", err)
	}
	defer sm.StopNotifier()

	if err := sm.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}

	// Claim the only slot.
	if err := sm.WaitForSlot(); err != nil {
		t.Fatalf("first WaitForSlot: %v", err)
	}

	// Release the slot after a short delay in a goroutine.
	go func() {
		time.Sleep(200 * time.Millisecond)
		_ = sm.ReleaseSlot()
	}()

	start := time.Now()
	// This WaitForSlot should wake up via notification, not poll.
	if err := sm.WaitForSlot(); err != nil {
		t.Fatalf("second WaitForSlot: %v", err)
	}
	elapsed := time.Since(start)

	// Should wake up in ~200ms, not 2+ seconds.
	if elapsed > 1500*time.Millisecond {
		t.Errorf("WaitForSlot took %v, expected < 1.5s (notification should wake faster than 2s poll)",
			elapsed)
	}
}

func TestSlotNotifier_ConcurrentWaitAndNotify(t *testing.T) {
	dir := t.TempDir()
	n := NewSlotNotifier(dir)
	if err := n.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer n.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	const rounds = 20
	var woke atomic.Int32

	for i := 0; i < rounds; i++ {
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := n.Wait(ctx); err == nil {
				woke.Add(1)
			}
		}()
		time.Sleep(10 * time.Millisecond)
		n.Notify()
		wg.Wait()
	}

	if got := woke.Load(); got != rounds {
		t.Errorf("woke = %d, want %d (concurrent wait/notify)", got, rounds)
	}
}
