package dag

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/veschin/GoLeM/internal/artifact"
)

// stubExecutor is a test executor that records calls and returns canned artifacts.
type stubExecutor struct {
	mu       sync.Mutex
	calls    []string // ordered list of executed step IDs
	results  map[string][]*artifact.Artifact
	errForID map[string]error // if set, return this error for the given step ID
	delay    time.Duration    // optional delay per step
}

func newStubExecutor() *stubExecutor {
	return &stubExecutor{
		results:  make(map[string][]*artifact.Artifact),
		errForID: make(map[string]error),
	}
}

func (e *stubExecutor) Execute(ctx context.Context, step Step, inputs []*artifact.Artifact) ([]*artifact.Artifact, error) {
	if e.delay > 0 {
		select {
		case <-time.After(e.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	e.calls = append(e.calls, step.ID)

	if err, ok := e.errForID[step.ID]; ok {
		return nil, err
	}

	if arts, ok := e.results[step.ID]; ok {
		return arts, nil
	}

	// Default: return a text artifact with the step ID.
	return []*artifact.Artifact{artifact.NewText(step.ID, "output of "+step.ID)}, nil
}

func (e *stubExecutor) getCalls() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	result := make([]string, len(e.calls))
	copy(result, e.calls)
	return result
}

func TestScheduler_SingleStep(t *testing.T) {
	d := &DAG{
		Steps: []Step{
			{ID: "s1", Prompt: "run me"},
		},
	}

	exec := newStubExecutor()
	s := NewScheduler(exec, 4)

	results, _, err := s.Run(context.Background(), d)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results["s1"] == nil {
		t.Fatal("s1 results are nil")
	}
	if len(results["s1"]) != 1 {
		t.Fatalf("s1 artifacts = %d, want 1", len(results["s1"]))
	}
}

func TestScheduler_LinearDAG_ExecutesInOrder(t *testing.T) {
	d := &DAG{
		Steps: []Step{
			{ID: "s1", Prompt: "first"},
			{ID: "s2", Prompt: "second", DependsOn: []string{"s1"}},
			{ID: "s3", Prompt: "third", DependsOn: []string{"s2"}},
		},
	}

	exec := newStubExecutor()
	s := NewScheduler(exec, 4)

	results, _, err := s.Run(context.Background(), d)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	calls := exec.getCalls()
	if len(calls) != 3 {
		t.Fatalf("expected 3 calls, got %d: %v", len(calls), calls)
	}

	// s1 must come before s2, s2 before s3.
	idx := make(map[string]int)
	for i, id := range calls {
		idx[id] = i
	}

	if idx["s1"] >= idx["s2"] {
		t.Errorf("s1 (idx %d) should come before s2 (idx %d)", idx["s1"], idx["s2"])
	}
	if idx["s2"] >= idx["s3"] {
		t.Errorf("s2 (idx %d) should come before s3 (idx %d)", idx["s2"], idx["s3"])
	}

	if len(results) != 3 {
		t.Errorf("expected 3 results, got %d", len(results))
	}
}

func TestScheduler_ParallelDAG_IndependentStepsRunConcurrently(t *testing.T) {
	d := &DAG{
		Steps: []Step{
			{ID: "s1", Prompt: "parallel 1"},
			{ID: "s2", Prompt: "parallel 2"},
			{ID: "s3", Prompt: "parallel 3"},
			{ID: "s4", Prompt: "join", DependsOn: []string{"s1", "s2", "s3"}},
		},
	}

	// Use an executor with a small delay to make concurrency observable.
	exec := newStubExecutor()
	exec.delay = 50 * time.Millisecond

	s := NewScheduler(exec, 4)

	start := time.Now()
	results, _, err := s.Run(context.Background(), d)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(results) != 4 {
		t.Errorf("expected 4 results, got %d", len(results))
	}

	// If s1, s2, s3 ran sequentially (3 * 50ms = 150ms), total would be ~200ms.
	// If they ran in parallel, total should be ~100ms (50ms parallel + 50ms s4).
	if elapsed > 200*time.Millisecond {
		t.Errorf("steps did not run in parallel: elapsed %v", elapsed)
	}
}

func TestScheduler_StepFailure_SkipsDependents(t *testing.T) {
	d := &DAG{
		Steps: []Step{
			{ID: "s1", Prompt: "ok"},
			{ID: "s2", Prompt: "fail", DependsOn: []string{"s1"}},
			{ID: "s3", Prompt: "should skip", DependsOn: []string{"s2"}},
		},
	}

	exec := newStubExecutor()
	exec.errForID["s2"] = fmt.Errorf("step s2 failed")

	s := NewScheduler(exec, 4)
	results, stepErrors, err := s.Run(context.Background(), d)

	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// s1 should have succeeded.
	if results["s1"] == nil {
		t.Error("s1 should have results")
	}

	// s2 should have failed (nil results).
	if results["s2"] != nil {
		t.Error("s2 should have nil results (failed)")
	}

	// s2 error should be recorded.
	if stepErrors["s2"] == nil {
		t.Error("s2 should have an error in stepErrors")
	}

	// s3 should have been skipped (nil results).
	if results["s3"] != nil {
		t.Error("s3 should have nil results (skipped)")
	}

	// s3 should have a skip error.
	if stepErrors["s3"] == nil {
		t.Error("s3 should have an error in stepErrors")
	}

	// Only s1 and s2 should have been called.
	calls := exec.getCalls()
	if len(calls) != 2 {
		t.Errorf("expected 2 calls, got %d: %v", len(calls), calls)
	}
}

func TestScheduler_StepFailure_IndependentStepsContinue(t *testing.T) {
	d := &DAG{
		Steps: []Step{
			{ID: "s1", Prompt: "will fail"},
			{ID: "s2", Prompt: "independent"},
			{ID: "s3", Prompt: "depends on s1", DependsOn: []string{"s1"}},
		},
	}

	exec := newStubExecutor()
	exec.errForID["s1"] = fmt.Errorf("s1 failed")

	s := NewScheduler(exec, 4)
	results, _, err := s.Run(context.Background(), d)

	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// s2 should succeed (independent of s1).
	if results["s2"] == nil {
		t.Error("s2 should have results (independent)")
	}

	// s3 should be skipped (depends on failed s1).
	if results["s3"] != nil {
		t.Error("s3 should have nil results (skipped)")
	}
}

func TestScheduler_ContextCancellation(t *testing.T) {
	d := &DAG{
		Steps: []Step{
			{ID: "s1", Prompt: "slow step"},
			{ID: "s2", Prompt: "dependent", DependsOn: []string{"s1"}},
		},
	}

	exec := newStubExecutor()
	exec.delay = 5 * time.Second // very slow

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	s := NewScheduler(exec, 4)
	_, _, err := s.Run(ctx, d)

	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
	if ctx.Err() != context.DeadlineExceeded {
		t.Errorf("error = %v, want DeadlineExceeded", err)
	}
}

func TestScheduler_InvalidDAG_ReturnsError(t *testing.T) {
	d := &DAG{
		Steps: []Step{
			{ID: "s1", Prompt: "self-loop", DependsOn: []string{"s1"}},
		},
	}

	exec := newStubExecutor()
	s := NewScheduler(exec, 4)

	_, _, err := s.Run(context.Background(), d)
	if err == nil {
		t.Fatal("expected error for invalid DAG")
	}
}

func TestScheduler_MaxConcurrency(t *testing.T) {
	d := &DAG{
		Steps: []Step{
			{ID: "s1", Prompt: "p1"},
			{ID: "s2", Prompt: "p2"},
			{ID: "s3", Prompt: "p3"},
			{ID: "s4", Prompt: "p4"},
		},
	}

	var maxConcurrent int64
	var currentConcurrent int64

	// Custom executor that tracks concurrency.
	trackingExec := &concurrencyTracker{
		delay:             50 * time.Millisecond,
		maxConcurrent:     &maxConcurrent,
		currentConcurrent: &currentConcurrent,
	}

	// Only 2 concurrent steps allowed.
	s := NewScheduler(trackingExec, 2)

	_, _, err := s.Run(context.Background(), d)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	peak := atomic.LoadInt64(&maxConcurrent)
	if peak > 2 {
		t.Errorf("peak concurrency = %d, expected max 2", peak)
	}
}

// concurrencyTracker is a StepExecutor that measures peak concurrency.
type concurrencyTracker struct {
	delay             time.Duration
	maxConcurrent     *int64
	currentConcurrent *int64
}

func (ct *concurrencyTracker) Execute(ctx context.Context, step Step, inputs []*artifact.Artifact) ([]*artifact.Artifact, error) {
	c := atomic.AddInt64(ct.currentConcurrent, 1)
	// Track peak concurrency with CAS loop.
	for {
		old := atomic.LoadInt64(ct.maxConcurrent)
		if c <= old || atomic.CompareAndSwapInt64(ct.maxConcurrent, old, c) {
			break
		}
	}
	defer atomic.AddInt64(ct.currentConcurrent, -1)

	select {
	case <-time.After(ct.delay):
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	return []*artifact.Artifact{artifact.NewText(step.ID, "output of "+step.ID)}, nil
}

func TestScheduler_ArtifactPropagation(t *testing.T) {
	d := &DAG{
		Steps: []Step{
			{ID: "s1", Prompt: "producer"},
			{ID: "s2", Prompt: "consumer", DependsOn: []string{"s1"}},
		},
	}

	// s1 produces a specific artifact. s2 should receive it as input.
	s1Artifact := artifact.NewText("s1", "s1 output data")

	var mu sync.Mutex
	var receivedInputs []*artifact.Artifact

	captureExec := &inputCapture{
		canned: map[string][]*artifact.Artifact{
			"s1": {s1Artifact},
		},
		onExecute: func(step Step, inputs []*artifact.Artifact) {
			if step.ID == "s2" {
				mu.Lock()
				receivedInputs = inputs
				mu.Unlock()
			}
		},
	}

	s := NewScheduler(captureExec, 4)
	_, _, err := s.Run(context.Background(), d)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()

	if len(receivedInputs) != 1 {
		t.Fatalf("s2 received %d inputs, want 1", len(receivedInputs))
	}
	if string(receivedInputs[0].Content) != "s1 output data" {
		t.Errorf("s2 input content = %q, want 's1 output data'", string(receivedInputs[0].Content))
	}
}

// inputCapture is a StepExecutor that captures inputs for inspection
// and returns canned results.
type inputCapture struct {
	canned    map[string][]*artifact.Artifact
	onExecute func(step Step, inputs []*artifact.Artifact)
}

func (ic *inputCapture) Execute(ctx context.Context, step Step, inputs []*artifact.Artifact) ([]*artifact.Artifact, error) {
	if ic.onExecute != nil {
		ic.onExecute(step, inputs)
	}
	if arts, ok := ic.canned[step.ID]; ok {
		return arts, nil
	}
	return []*artifact.Artifact{artifact.NewText(step.ID, "output of "+step.ID)}, nil
}

func TestScheduler_DiamondDAG(t *testing.T) {
	// s1 -> s2, s1 -> s3, s2+s3 -> s4
	d := &DAG{
		Steps: []Step{
			{ID: "s1", Prompt: "root"},
			{ID: "s2", Prompt: "left", DependsOn: []string{"s1"}},
			{ID: "s3", Prompt: "right", DependsOn: []string{"s1"}},
			{ID: "s4", Prompt: "join", DependsOn: []string{"s2", "s3"}},
		},
	}

	exec := newStubExecutor()
	exec.delay = 30 * time.Millisecond

	s := NewScheduler(exec, 4)
	results, _, err := s.Run(context.Background(), d)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(results) != 4 {
		t.Errorf("expected 4 results, got %d", len(results))
	}

	calls := exec.getCalls()
	if len(calls) != 4 {
		t.Fatalf("expected 4 calls, got %d", len(calls))
	}

	// s1 must be first, s4 must be last.
	if calls[0] != "s1" {
		t.Errorf("first call = %s, want s1", calls[0])
	}
	if calls[len(calls)-1] != "s4" {
		t.Errorf("last call = %s, want s4", calls[len(calls)-1])
	}
}

func TestScheduler_TopologicalSortReturnsOrder(t *testing.T) {
	d := &DAG{
		Steps: []Step{
			{ID: "c", Prompt: "third", DependsOn: []string{"b"}},
			{ID: "a", Prompt: "first"},
			{ID: "b", Prompt: "second", DependsOn: []string{"a"}},
		},
	}

	sorted, err := d.TopologicalSort()
	if err != nil {
		t.Fatalf("TopologicalSort: %v", err)
	}

	if len(sorted) != 3 {
		t.Fatalf("expected 3 steps, got %d", len(sorted))
	}

	// "a" must come before "b", "b" before "c".
	idxMap := make(map[string]int)
	for i, s := range sorted {
		idxMap[s.ID] = i
	}

	if idxMap["a"] >= idxMap["b"] {
		t.Errorf("a (idx %d) should come before b (idx %d)", idxMap["a"], idxMap["b"])
	}
	if idxMap["b"] >= idxMap["c"] {
		t.Errorf("b (idx %d) should come before c (idx %d)", idxMap["b"], idxMap["c"])
	}
}

func TestScheduler_DefaultConcurrencyClamp(t *testing.T) {
	exec := newStubExecutor()

	// maxConcurrent == 0 means unlimited: stored as-is.
	s := NewScheduler(exec, 0)
	if s.maxConcurrent != 0 {
		t.Errorf("maxConcurrent = %d, want 0 (unlimited)", s.maxConcurrent)
	}

	// Negative values are clamped to 1 (sequential fallback).
	s = NewScheduler(exec, -5)
	if s.maxConcurrent != 1 {
		t.Errorf("maxConcurrent = %d, want 1 (clamped from -5)", s.maxConcurrent)
	}
}

// TestScheduler_UnlimitedConcurrency verifies that maxConcurrent == 0 does not
// block parallel execution. All independent steps should complete concurrently.
func TestScheduler_UnlimitedConcurrency(t *testing.T) {
	d := &DAG{
		Steps: []Step{
			{ID: "s1", Prompt: "p1"},
			{ID: "s2", Prompt: "p2"},
			{ID: "s3", Prompt: "p3"},
		},
	}

	exec := newStubExecutor()
	exec.delay = 50 * time.Millisecond

	// maxConcurrent == 0 means unlimited.
	s := NewScheduler(exec, 0)

	start := time.Now()
	results, _, err := s.Run(context.Background(), d)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(results) != 3 {
		t.Errorf("expected 3 results, got %d", len(results))
	}
	// 3 steps * 50ms sequential = 150ms; in parallel ≈ 50ms. Allow generous margin.
	if elapsed > 200*time.Millisecond {
		t.Errorf("unlimited scheduler did not run steps in parallel: elapsed %v", elapsed)
	}
}

func TestScheduler_StepErrorsMap(t *testing.T) {
	d := &DAG{
		Steps: []Step{
			{ID: "s1", Prompt: "will fail"},
			{ID: "s2", Prompt: "depends on s1", DependsOn: []string{"s1"}},
		},
	}

	exec := newStubExecutor()
	exec.errForID["s1"] = fmt.Errorf("err:dag s1 exploded")

	s := NewScheduler(exec, 4)
	_, stepErrors, err := s.Run(context.Background(), d)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// s1 should have the original error.
	if stepErrors["s1"] == nil {
		t.Fatal("expected s1 error in stepErrors")
	}
	if stepErrors["s1"].Error() != "err:dag s1 exploded" {
		t.Errorf("s1 error = %v, want 'err:dag s1 exploded'", stepErrors["s1"])
	}

	// s2 should have a skip error.
	if stepErrors["s2"] == nil {
		t.Fatal("expected s2 error in stepErrors")
	}
}
