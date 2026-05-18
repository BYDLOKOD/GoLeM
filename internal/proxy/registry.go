// Package proxy – model registry for per-model concurrency control.
package proxy

import "sort"

// ModelRegistry maps model names to individual semaphore channels.
// An optional defaultCap provides a fallback semaphore for models that are
// not explicitly registered.  When defaultCap is 0 there is no fallback and
// Get returns (nil, false) for unknown models.
type ModelRegistry struct {
	semaphores map[string]chan struct{}
	defaultSem chan struct{} // nil when defaultCap == 0
	defaultCap int
}

// NewModelRegistry creates an empty registry.
// defaultCap is the capacity of the fallback semaphore used for models that
// are not explicitly registered.  Pass 0 to disable the fallback.
func NewModelRegistry(defaultCap int) *ModelRegistry {
	r := &ModelRegistry{
		semaphores: make(map[string]chan struct{}),
		defaultCap: defaultCap,
	}
	if defaultCap > 0 {
		r.defaultSem = make(chan struct{}, defaultCap)
	}
	return r
}

// NewRegistryFromMap builds a registry from a model→concurrency map.
// defaultCap is used for models absent from the map (0 = no fallback).
func NewRegistryFromMap(models map[string]int, defaultCap int) *ModelRegistry {
	r := NewModelRegistry(defaultCap)
	for name, concurrency := range models {
		r.Register(name, concurrency)
	}
	return r
}

// Register adds or replaces a named model with the given concurrency limit.
func (r *ModelRegistry) Register(name string, concurrency int) {
	r.semaphores[name] = make(chan struct{}, concurrency)
}

// Get returns the semaphore channel for the given model name.
// If the model is not registered and a default semaphore is configured,
// the default semaphore is returned with ok=true.
// If neither a named nor a default semaphore exists, (nil, false) is returned.
func (r *ModelRegistry) Get(name string) (chan struct{}, bool) {
	if sem, ok := r.semaphores[name]; ok {
		return sem, true
	}
	if r.defaultSem != nil {
		return r.defaultSem, true
	}
	return nil, false
}

// Concurrency returns the capacity of the semaphore for the given model.
// Unknown models return defaultCap (which may be 0).
func (r *ModelRegistry) Concurrency(name string) int {
	if sem, ok := r.semaphores[name]; ok {
		return cap(sem)
	}
	return r.defaultCap
}

// Models returns the sorted list of explicitly registered model names.
// The default semaphore is not represented in this list.
func (r *ModelRegistry) Models() []string {
	names := make([]string, 0, len(r.semaphores))
	for name := range r.semaphores {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
