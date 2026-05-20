package proxy

import (
	"testing"
)

// ---------------------------------------------------------------------------
// ModelRegistry tests
// ---------------------------------------------------------------------------

func TestModelRegistryRegisterAndGet(t *testing.T) {
	reg := NewModelRegistry(0)
	reg.Register("glm-5", 4)

	sem, ok := reg.Get("glm-5")
	if !ok {
		t.Fatal("Get returned ok=false for registered model")
	}
	if cap(sem) != 4 {
		t.Errorf("semaphore capacity = %d, want 4", cap(sem))
	}
}

func TestModelRegistryGetUnknownWithDefault(t *testing.T) {
	reg := NewModelRegistry(3) // defaultCap = 3
	reg.Register("glm-5", 2)

	// Unknown model should return default semaphore.
	sem, ok := reg.Get("glm-unknown")
	if !ok {
		t.Fatal("Get returned ok=false for unknown model with non-zero defaultCap")
	}
	if cap(sem) != 3 {
		t.Errorf("default semaphore capacity = %d, want 3", cap(sem))
	}
}

func TestModelRegistryGetUnknownWithoutDefault(t *testing.T) {
	reg := NewModelRegistry(0) // defaultCap = 0 → no default
	reg.Register("glm-5", 2)

	_, ok := reg.Get("glm-unknown")
	if ok {
		t.Error("Get returned ok=true for unknown model with defaultCap=0, want false")
	}
}

func TestModelRegistryConcurrency(t *testing.T) {
	reg := NewModelRegistry(0)
	reg.Register("glm-5", 10)
	reg.Register("glm-4", 2)

	if got := reg.Concurrency("glm-5"); got != 10 {
		t.Errorf("Concurrency(glm-5) = %d, want 10", got)
	}
	if got := reg.Concurrency("glm-4"); got != 2 {
		t.Errorf("Concurrency(glm-4) = %d, want 2", got)
	}
}

func TestModelRegistryConcurrencyUnknown(t *testing.T) {
	reg := NewModelRegistry(5)
	if got := reg.Concurrency("unknown"); got != 5 {
		t.Errorf("Concurrency(unknown) with defaultCap=5 = %d, want 5", got)
	}

	reg2 := NewModelRegistry(0)
	if got := reg2.Concurrency("unknown"); got != 0 {
		t.Errorf("Concurrency(unknown) with defaultCap=0 = %d, want 0", got)
	}
}

func TestModelRegistryModels(t *testing.T) {
	reg := NewModelRegistry(0)
	reg.Register("glm-5", 2)
	reg.Register("glm-4", 1)
	reg.Register("glm-5.1", 3)

	models := reg.Models()
	if len(models) != 3 {
		t.Errorf("Models() len = %d, want 3", len(models))
	}

	// Verify all registered model names are present.
	set := make(map[string]bool)
	for _, m := range models {
		set[m] = true
	}
	for _, name := range []string{"glm-5", "glm-4", "glm-5.1"} {
		if !set[name] {
			t.Errorf("Models() missing %q", name)
		}
	}
}

func TestModelRegistryDefaultCapIsolation(t *testing.T) {
	// The default semaphore must be shared for the same unknown model
	// but each call with defaultCap > 0 returns the same channel.
	reg := NewModelRegistry(2)

	sem1, ok1 := reg.Get("x")
	sem2, ok2 := reg.Get("x")
	if !ok1 || !ok2 {
		t.Fatal("Get returned ok=false for unknown model with defaultCap=2")
	}
	// Must be the same channel so callers share the semaphore slot pool.
	if sem1 != sem2 {
		t.Error("Get returned different channels for same unknown model")
	}
}

func TestModelRegistryDefaultDoesNotBlockRegistered(t *testing.T) {
	// Acquiring the default semaphore must not affect capacity of a named model.
	reg := NewModelRegistry(1)
	reg.Register("glm-5", 5)

	// Acquire the default semaphore (for unknown model).
	defaultSem, _ := reg.Get("unknown")
	defaultSem <- struct{}{}

	// Named model semaphore must still be fully open.
	namedSem, ok := reg.Get("glm-5")
	if !ok {
		t.Fatal("Get returned ok=false for registered model")
	}
	if len(namedSem) != 0 {
		t.Errorf("named model semaphore has %d items, want 0 (not affected by default)", len(namedSem))
	}
	<-defaultSem // cleanup
}

func TestNewRegistryFromMap(t *testing.T) {
	models := map[string]int{
		"glm-5.1": 10,
		"glm-5":   2,
		"glm-4.7": 2,
	}
	reg := NewRegistryFromMap(models, 0)

	for name, wantCap := range models {
		sem, ok := reg.Get(name)
		if !ok {
			t.Errorf("Get(%q) returned ok=false", name)
			continue
		}
		if cap(sem) != wantCap {
			t.Errorf("Get(%q) cap = %d, want %d", name, cap(sem), wantCap)
		}
	}
}

func TestNewRegistryFromMapEmpty(t *testing.T) {
	// Empty map with non-zero default: unknown models should use default.
	reg := NewRegistryFromMap(nil, 7)

	sem, ok := reg.Get("any-model")
	if !ok {
		t.Fatal("Get returned ok=false for non-zero defaultCap with empty map")
	}
	if cap(sem) != 7 {
		t.Errorf("default semaphore cap = %d, want 7", cap(sem))
	}
}
