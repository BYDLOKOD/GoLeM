package dag

import (
	"context"
	"fmt"

	"github.com/veschin/GoLeM/internal/artifact"
)

// StepExecutor is the interface for executing a single DAG step.
// Implementations receive the step definition and artifacts from
// dependency steps, and return the artifacts produced by this step.
type StepExecutor interface {
	Execute(ctx context.Context, step Step, inputs []*artifact.Artifact) ([]*artifact.Artifact, error)
}

// stepCompletion is sent by a step goroutine when it finishes (or fails).
type stepCompletion struct {
	stepID    string
	artifacts []*artifact.Artifact // nil if the step failed
	err       error                // non-nil if the step failed
}

// Scheduler executes the steps of a DAG, respecting dependency ordering
// and a concurrency limit.
type Scheduler struct {
	executor      StepExecutor
	maxConcurrent int
}

// NewScheduler creates a scheduler that uses the given executor and limits
// concurrent step execution to maxConcurrent. If maxConcurrent == 0, execution
// is unlimited (all ready steps run in parallel). Negative values are clamped
// to 1 (sequential execution).
func NewScheduler(executor StepExecutor, maxConcurrent int) *Scheduler {
	if maxConcurrent < 0 {
		maxConcurrent = 1
	}
	return &Scheduler{
		executor:      executor,
		maxConcurrent: maxConcurrent,
	}
}

// Run executes all steps in the DAG, respecting dependencies and the
// concurrency limit. It returns:
//   - A map from step ID to that step's output artifacts (nil for failed/skipped steps).
//   - A map from step ID to per-step errors (failed or skipped steps).
//   - A top-level error if the DAG is invalid or the context is cancelled.
//
// Concurrency model:
//
//	A coordinator goroutine owns all mutable state (inDegree, results,
//	stepErrors). Step goroutines send stepCompletion values on the completions
//	channel. The coordinator receives completions, decrements in-degree of
//	dependents, and launches newly-ready steps. No shared mutable state
//	exists between goroutines -- all coordination happens through the channel.
func (s *Scheduler) Run(ctx context.Context, dag *DAG) (map[string][]*artifact.Artifact, map[string]error, error) {
	if err := dag.Validate(); err != nil {
		return nil, nil, err
	}

	// Build dependency structures -- owned exclusively by the coordinator.
	steps := make(map[string]Step, len(dag.Steps))
	inDegree := make(map[string]int, len(dag.Steps))
	dependents := make(map[string][]string, len(dag.Steps))

	for _, step := range dag.Steps {
		steps[step.ID] = step
		if _, ok := inDegree[step.ID]; !ok {
			inDegree[step.ID] = 0
		}
		for _, dep := range step.DependsOn {
			inDegree[step.ID]++
			dependents[dep] = append(dependents[dep], step.ID)
		}
	}

	// Results and error tracking -- owned exclusively by the coordinator.
	results := make(map[string][]*artifact.Artifact, len(dag.Steps))
	stepErrors := make(map[string]error, len(dag.Steps))

	// Count total steps (needed for completion channel buffer calculation).
	totalSteps := len(dag.Steps)
	processed := 0

	// Channel for step goroutines to report completion to the coordinator.
	// Buffer must be at least 1 to avoid deadlock; use totalSteps when unlimited.
	completionBuf := s.maxConcurrent
	if completionBuf <= 0 {
		completionBuf = totalSteps
	}
	if completionBuf < 1 {
		completionBuf = 1
	}
	completions := make(chan stepCompletion, completionBuf)

	// Semaphore channel to bound concurrent step execution.
	// When maxConcurrent == 0, sem is nil (unlimited — no blocking).
	var sem chan struct{}
	if s.maxConcurrent > 0 {
		sem = make(chan struct{}, s.maxConcurrent)
	}

	// hasFailed returns true if any dependency of stepID has failed or been skipped.
	hasFailed := func(stepID string) bool {
		for _, dep := range steps[stepID].DependsOn {
			if _, ok := stepErrors[dep]; ok {
				return true
			}
		}
		return false
	}

	// launchStep starts a goroutine to execute a single step.
	// Inputs are passed as a parameter to avoid a data race: the coordinator
	// collects input artifacts from the results map (which it owns exclusively)
	// and passes them directly to the goroutine at launch time.
	launchStep := func(step Step, inputs []*artifact.Artifact) {
		go func() {
			if sem != nil {
				sem <- struct{}{} // acquire concurrency slot
				defer func() { <-sem }()
			}

			// Check cancellation before executing.
			if ctx.Err() != nil {
				completions <- stepCompletion{stepID: step.ID, err: ctx.Err()}
				return
			}

			// Execute the step using the inputs passed from the coordinator.
			arts, err := s.executor.Execute(ctx, step, inputs)
			completions <- stepCompletion{stepID: step.ID, artifacts: arts, err: err}
		}()
	}

	// Launch root steps (in-degree 0).
	// Root steps have no dependencies, so inputs are nil.
	inflight := 0
	for _, step := range dag.Steps {
		if inDegree[step.ID] == 0 {
			launchStep(step, nil)
			inflight++
		}
	}

	// Coordinator loop: receive completions, update state, launch newly-ready steps.
	for processed < totalSteps {
		if inflight == 0 {
			// No steps are in-flight and we haven't processed everything.
			// This should not happen with a valid DAG, but guard against it.
			break
		}

		select {
		case <-ctx.Done():
			return results, stepErrors, ctx.Err()

		case comp := <-completions:
			inflight--
			processed++

			// Record result.
			if comp.err != nil {
				stepErrors[comp.stepID] = comp.err
				results[comp.stepID] = nil
			} else {
				results[comp.stepID] = comp.artifacts
			}

			// Process dependents: decrement in-degree and launch or skip.
			for _, depID := range dependents[comp.stepID] {
				inDegree[depID]--
				if inDegree[depID] == 0 {
					if hasFailed(depID) {
						// A dependency failed -- skip this step.
						results[depID] = nil
						stepErrors[depID] = fmt.Errorf("err:dag skipped due to failed dependency")
						processed++

						// Propagate skip to transitive dependents.
						s.propagateSkip(depID, dependents, inDegree, results, stepErrors, &processed)
					} else {
						// Collect input artifacts for the dependent step.
						var inputs []*artifact.Artifact
						for _, dep := range steps[depID].DependsOn {
							if arts := results[dep]; arts != nil {
								inputs = append(inputs, arts...)
							}
						}
						launchStep(steps[depID], inputs)
						inflight++
					}
				}
			}
		}
	}

	return results, stepErrors, nil
}

// propagateSkip recursively marks all transitive dependents of a skipped step
// as skipped. This handles the case where a skip creates a cascade (e.g.,
// s1 fails -> s2 skipped -> s3 skipped -> s4 skipped).
func (s *Scheduler) propagateSkip(
	skipID string,
	dependents map[string][]string,
	inDegree map[string]int,
	results map[string][]*artifact.Artifact,
	stepErrors map[string]error,
	processed *int,
) {
	for _, depID := range dependents[skipID] {
		inDegree[depID]--
		if inDegree[depID] == 0 {
			// This dependent is now ready but has a failed ancestor -- skip it.
			results[depID] = nil
			stepErrors[depID] = fmt.Errorf("err:dag skipped due to failed dependency")
			*processed++

			// Continue propagation.
			s.propagateSkip(depID, dependents, inDegree, results, stepErrors, processed)
		}
	}
}
