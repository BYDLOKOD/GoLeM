// Package dag defines the DAG (Directed Acyclic Graph) pipeline model for GoLeM.
// It provides type definitions, validation with cycle detection (Kahn's algorithm),
// and a concurrent scheduler for executing pipeline steps.
package dag

import (
	"fmt"

	"github.com/veschin/GoLeM/internal/validation"
)

// Step represents a single unit of work in a DAG pipeline.
type Step struct {
	// ID is the unique identifier for this step (required).
	ID string `json:"id" toml:"id"`
	// Prompt is the instruction to execute (required for non-gate steps).
	Prompt string `json:"prompt" toml:"prompt"`
	// DependsOn lists step IDs that must complete before this step starts.
	DependsOn []string `json:"depends_on,omitempty" toml:"depends_on,omitempty"`
	// Condition is a reserved field for conditional execution (not evaluated yet).
	Condition string `json:"condition,omitempty" toml:"condition,omitempty"`
	// Model overrides the default model for this step (empty = use default).
	Model string `json:"model,omitempty" toml:"model,omitempty"`
	// Timeout overrides the default timeout in seconds (0 = use default).
	Timeout int `json:"timeout,omitempty" toml:"timeout,omitempty"`
	// Validate is an optional output validation rule. For gate steps, this is required.
	Validate *validation.ValidationRule `json:"validate,omitempty" toml:"validate,omitempty"`
	// Type distinguishes step variants. "gate" steps validate upstream output instead of running a prompt.
	Type string `json:"type,omitempty" toml:"type,omitempty"`
	// Retry configures automatic retries on failure.
	Retry *RetryConfig `json:"retry,omitempty" toml:"retry,omitempty"`
}

// RetryConfig controls automatic retry behaviour for a step.
type RetryConfig struct {
	MaxAttempts int    `json:"max_attempts" toml:"max_attempts"`
	Feedback    string `json:"feedback" toml:"feedback"`
}

// DAG represents a pipeline of steps with dependency ordering.
type DAG struct {
	Steps []Step `json:"steps" toml:"steps"`

	// topoOrder caches the topological order computed by Validate.
	// Nil means Validate has not been called (or the DAG is invalid).
	topoOrder []Step
}

// Validate checks the DAG for structural errors and caches the
// topological order for use by TopologicalSort:
//   - At least one step exists.
//   - All step IDs are non-empty and unique.
//   - All prompts are non-empty.
//   - All DependsOn references point to existing step IDs.
//   - No cycles exist (detected via Kahn's algorithm).
func (d *DAG) Validate() error {
	if len(d.Steps) == 0 {
		return fmt.Errorf("err:dag empty pipeline")
	}

	// Return cached result if already validated successfully.
	if d.topoOrder != nil {
		return nil
	}

	// Build lookup maps and check basic validity.
	ids := make(map[string]bool, len(d.Steps))
	for i, s := range d.Steps {
		if s.ID == "" {
			return fmt.Errorf("err:dag step %d has empty ID", i)
		}
		if s.Prompt == "" && s.Type != "gate" {
			return fmt.Errorf("err:dag step %q has empty prompt", s.ID)
		}
		if ids[s.ID] {
			return fmt.Errorf("err:dag duplicate step ID %q", s.ID)
		}
		ids[s.ID] = true
	}

	// Gate-specific validation.
	for _, s := range d.Steps {
		if s.Type != "gate" {
			continue
		}
		if len(s.DependsOn) == 0 {
			return fmt.Errorf("err:dag gate step %q must have at least one dependency", s.ID)
		}
		if s.Validate == nil {
			return fmt.Errorf("err:dag gate step %q has no validate rule", s.ID)
		}
	}

	// Check DependsOn references.
	for _, s := range d.Steps {
		for _, dep := range s.DependsOn {
			if !ids[dep] {
				return fmt.Errorf("err:dag step %q depends on unknown step %q", s.ID, dep)
			}
		}
	}

	// Kahn's algorithm for cycle detection and topological sort.
	// inDegree[stepID] = number of unresolved dependencies.
	inDegree := make(map[string]int, len(d.Steps))
	// dependents[stepID] = list of steps that depend on stepID.
	dependents := make(map[string][]string, len(d.Steps))
	// stepMap for looking up Step structs by ID during sort.
	stepMap := make(map[string]Step, len(d.Steps))

	for _, s := range d.Steps {
		stepMap[s.ID] = s
		if _, ok := inDegree[s.ID]; !ok {
			inDegree[s.ID] = 0
		}
		for _, dep := range s.DependsOn {
			inDegree[s.ID]++
			dependents[dep] = append(dependents[dep], s.ID)
		}
	}

	// Enqueue all steps with in-degree 0.
	queue := make([]string, 0, len(d.Steps))
	for id, deg := range inDegree {
		if deg == 0 {
			queue = append(queue, id)
		}
	}

	// Process queue -- build topological order while detecting cycles.
	var sorted []Step
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		sorted = append(sorted, stepMap[current])

		for _, dep := range dependents[current] {
			inDegree[dep]--
			if inDegree[dep] == 0 {
				queue = append(queue, dep)
			}
		}
	}

	if len(sorted) != len(d.Steps) {
		d.topoOrder = nil // ensure no stale cache
		return fmt.Errorf("err:dag cycle detected")
	}

	// Cache the topological order for TopologicalSort().
	d.topoOrder = sorted

	return nil
}

// TopologicalSort returns the steps in topological order.
// Returns the cached order from a prior Validate call, or calls Validate
// internally if the cache is empty. Returns an error if the DAG contains
// cycles or is empty.
func (d *DAG) TopologicalSort() ([]Step, error) {
	// Return cached order from a prior Validate call.
	if d.topoOrder != nil {
		return d.topoOrder, nil
	}
	// Validate computes and caches topoOrder.
	if err := d.Validate(); err != nil {
		return nil, err
	}
	return d.topoOrder, nil
}
