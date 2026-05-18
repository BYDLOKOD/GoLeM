package proxy

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// proxy.go tests
// ---------------------------------------------------------------------------

func TestNewProxyDefaultsConcurrency(t *testing.T) {
	tests := []struct {
		name        string
		concurrency int
		want        int
	}{
		{"zero defaults to 1", 0, 1},
		{"negative defaults to 1", -5, 1},
		{"positive kept as-is", 3, 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := New(Config{
				TargetURL:   "http://localhost:9999",
				Concurrency: tt.concurrency,
			})
			// In backward-compat mode (no Models map), cfg.Concurrency is
			// normalized to at least 1. The per-model registry is nil.
			if p.cfg.Concurrency != tt.want {
				t.Errorf("cfg.Concurrency = %d, want %d", p.cfg.Concurrency, tt.want)
			}
			if p.registry != nil {
				t.Error("registry should be nil in backward-compat mode (no Models map)")
			}
		})
	}
}

// startTestProxy starts a proxy pointed at the given backend URL on a random
// port and returns the proxy, its base URL, and a cleanup function.
func startTestProxy(t *testing.T, backendURL string, concurrency int) (*Proxy, string, func()) {
	t.Helper()
	p := New(Config{
		TargetURL:   backendURL,
		Concurrency: concurrency,
		Port:        0, // random port
	})

	started := make(chan struct{})
	var startErr error

	go func() {
		defer close(started)
		_, startErr = p.Start()
	}()

	// Wait until the proxy is listening.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if port := p.Port(); port > 0 {
			baseURL := fmt.Sprintf("http://localhost:%d", port)
			return p, baseURL, func() { p.Stop() }
		}
		time.Sleep(10 * time.Millisecond)
	}

	// If we get here, Start failed or listener never bound.
	select {
	case <-started:
		if startErr != nil {
			t.Fatalf("proxy Start failed: %v", startErr)
		}
	default:
	}
	t.Fatal("proxy did not start listening within 3s")
	return nil, "", nil // unreachable
}

func TestProxyStartAndStop(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	p, baseURL, cleanup := startTestProxy(t, backend.URL, 1)
	defer cleanup()

	// Verify /health returns 200.
	resp, err := http.Get(baseURL + "/health")
	if err != nil {
		t.Fatalf("GET /health: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("/health status = %d, want 200", resp.StatusCode)
	}

	port := p.Port()

	// Stop the proxy.
	p.Stop()

	// Give the listener a moment to close.
	time.Sleep(50 * time.Millisecond)

	// Verify the port is released — connection should fail.
	client := &http.Client{Timeout: 500 * time.Millisecond}
	_, err = client.Get(fmt.Sprintf("http://localhost:%d/health", port))
	if err == nil {
		t.Error("expected connection error after Stop, but request succeeded")
	}
}

func TestProxyHealthEndpointReturnsJSON(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	_, baseURL, cleanup := startTestProxy(t, backend.URL, 2)
	defer cleanup()

	resp, err := http.Get(baseURL + "/health")
	if err != nil {
		t.Fatalf("GET /health: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}

	var body map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode JSON: %v", err)
	}

	// Check required fields.
	if status, ok := body["status"].(string); !ok || status != "ok" {
		t.Errorf("status = %v, want \"ok\"", body["status"])
	}
	for _, key := range []string{"port", "active", "queued"} {
		if _, ok := body[key]; !ok {
			t.Errorf("missing key %q in health response", key)
		}
	}
	// Port should match actual port (non-zero).
	if portVal, ok := body["port"].(float64); !ok || portVal <= 0 {
		t.Errorf("port = %v, want >0 number", body["port"])
	}
}

func TestProxyConcurrencySemaphore(t *testing.T) {
	// Track how many requests are concurrently active in the backend.
	var active int64
	var maxActive int64

	gate := make(chan struct{}) // keeps backend requests blocked until we release

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cur := atomic.AddInt64(&active, 1)
		defer atomic.AddInt64(&active, -1)

		// Track the maximum concurrency observed.
		for {
			old := atomic.LoadInt64(&maxActive)
			if cur <= old || atomic.CompareAndSwapInt64(&maxActive, old, cur) {
				break
			}
		}

		<-gate // wait until test releases
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	_, baseURL, cleanup := startTestProxy(t, backend.URL, 1) // concurrency = 1
	defer cleanup()

	// Send 2 concurrent requests through the proxy.
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := http.Get(baseURL + "/test")
			if err != nil {
				return // proxy may have stopped
			}
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
		}()
	}

	// Wait a bit for both requests to arrive at the proxy.
	time.Sleep(200 * time.Millisecond)

	// With concurrency=1, only one should be active in the backend.
	cur := atomic.LoadInt64(&active)
	if cur != 1 {
		t.Errorf("active backend requests = %d, want 1 (concurrency=1)", cur)
	}

	// Also verify via /health that active=1.
	resp, err := http.Get(baseURL + "/health")
	if err == nil {
		var body map[string]interface{}
		_ = json.NewDecoder(resp.Body).Decode(&body)
		_ = resp.Body.Close()
		if a, ok := body["active"].(float64); ok && a != 1 {
			t.Errorf("/health active = %v, want 1", a)
		}
	}

	// Release both requests.
	close(gate)
	wg.Wait()

	// Max active should have been exactly 1.
	if m := atomic.LoadInt64(&maxActive); m != 1 {
		t.Errorf("max concurrent backend requests = %d, want 1", m)
	}
}

func TestJoinPaths(t *testing.T) {
	tests := []struct {
		base, suffix, want string
	}{
		{"", "/foo", "/foo"},
		{"/api", "/v1", "/api/v1"},
		{"/api/", "/v1", "/api/v1"},
		{"/api/", "v1", "/api/v1"},
		{"/api", "", "/api"},
		{"", "", ""},
		{"/a/b/", "/c/d", "/a/b/c/d"},
	}
	for _, tt := range tests {
		name := fmt.Sprintf("joinPaths(%q,%q)", tt.base, tt.suffix)
		t.Run(name, func(t *testing.T) {
			if got := joinPaths(tt.base, tt.suffix); got != tt.want {
				t.Errorf("joinPaths(%q, %q) = %q, want %q", tt.base, tt.suffix, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// lifecycle.go tests
// ---------------------------------------------------------------------------

func TestWriteAndReadPIDPort(t *testing.T) {
	dir := t.TempDir()

	wantPID := 12345
	wantPort := 54321

	if err := WritePIDFile(dir, wantPID, wantPort); err != nil {
		t.Fatalf("WritePIDFile: %v", err)
	}

	gotPID, gotPort, err := readPIDPort(dir)
	if err != nil {
		t.Fatalf("readPIDPort: %v", err)
	}
	if gotPID != wantPID {
		t.Errorf("pid = %d, want %d", gotPID, wantPID)
	}
	if gotPort != wantPort {
		t.Errorf("port = %d, want %d", gotPort, wantPort)
	}
}

func TestCleanPIDFile(t *testing.T) {
	dir := t.TempDir()

	// Write PID and port files.
	if err := WritePIDFile(dir, 1, 2); err != nil {
		t.Fatalf("WritePIDFile: %v", err)
	}

	// Verify files exist.
	for _, name := range []string{pidFile, portFile} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatalf("expected %s to exist: %v", name, err)
		}
	}

	// Clean up.
	if err := CleanPIDFile(dir); err != nil {
		t.Fatalf("CleanPIDFile: %v", err)
	}

	// Verify files are removed.
	for _, name := range []string{pidFile, portFile} {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Errorf("expected %s to be removed, got err: %v", name, err)
		}
	}
}

func TestCleanPIDFileIdempotent(t *testing.T) {
	dir := t.TempDir()
	// Calling CleanPIDFile on a directory with no PID files should not error.
	if err := CleanPIDFile(dir); err != nil {
		t.Fatalf("CleanPIDFile on empty dir: %v", err)
	}
}

func TestIsRunningReturnsFalseWhenNoFiles(t *testing.T) {
	dir := t.TempDir()
	port, alive := IsRunning(dir)
	if alive {
		t.Error("IsRunning returned true for empty directory")
	}
	if port != 0 {
		t.Errorf("port = %d, want 0", port)
	}
}

func TestWriteAtomic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")

	content := "hello, world"
	if err := writeAtomic(path, content); err != nil {
		t.Fatalf("writeAtomic: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != content {
		t.Errorf("content = %q, want %q", string(got), content)
	}

	// Overwrite with new content — should replace atomically.
	content2 := "updated content"
	if err := writeAtomic(path, content2); err != nil {
		t.Fatalf("writeAtomic (overwrite): %v", err)
	}
	got, err = os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile after overwrite: %v", err)
	}
	if string(got) != content2 {
		t.Errorf("content after overwrite = %q, want %q", string(got), content2)
	}

	// No temp files should be left behind.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if e.Name() != "test.txt" {
			t.Errorf("unexpected leftover file: %s", e.Name())
		}
	}
}

func TestWriteAtomicBadDir(t *testing.T) {
	// Writing to a non-existent directory should fail.
	err := writeAtomic("/nonexistent-dir-12345/file.txt", "data")
	if err == nil {
		t.Error("expected error writing to non-existent directory")
	}
}

func TestReadPIDPortParseErrors(t *testing.T) {
	dir := t.TempDir()

	// Write non-numeric PID.
	if err := os.WriteFile(filepath.Join(dir, pidFile), []byte("notanumber"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, portFile), []byte("8080"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, _, err := readPIDPort(dir)
	if err == nil {
		t.Error("expected error for non-numeric PID")
	}

	// Write valid PID but non-numeric port.
	if err := os.WriteFile(filepath.Join(dir, pidFile), []byte(strconv.Itoa(os.Getpid())), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, portFile), []byte("notaport"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, _, err = readPIDPort(dir)
	if err == nil {
		t.Error("expected error for non-numeric port")
	}
}

// ---------------------------------------------------------------------------
// Proxy Retry tests
// ---------------------------------------------------------------------------

// startTestProxyWithRetry starts a proxy with retry configuration.
func startTestProxyWithRetry(t *testing.T, backendURL string, concurrency int, retryCfg RetryConfig) (*Proxy, string, func()) {
	t.Helper()
	p := New(Config{
		TargetURL:   backendURL,
		Concurrency: concurrency,
		Port:        0,
		Retry:       retryCfg,
	})

	started := make(chan struct{})
	var startErr error

	go func() {
		defer close(started)
		_, startErr = p.Start()
	}()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if port := p.Port(); port > 0 {
			baseURL := fmt.Sprintf("http://localhost:%d", port)
			return p, baseURL, func() { p.Stop() }
		}
		time.Sleep(10 * time.Millisecond)
	}

	select {
	case <-started:
		if startErr != nil {
			t.Fatalf("proxy Start failed: %v", startErr)
		}
	default:
	}
	t.Fatal("proxy did not start listening within 3s")
	return nil, "", nil
}

func TestProxyRetryOn502(t *testing.T) {
	var callCount int64
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt64(&callCount, 1)
		if n < 3 {
			w.WriteHeader(http.StatusBadGateway) // 502
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("success"))
	}))
	defer backend.Close()

	_, baseURL, cleanup := startTestProxyWithRetry(t, backend.URL, 1, RetryConfig{
		MaxRetries: 3,
		BaseDelay:  10 * time.Millisecond,
		MaxDelay:   50 * time.Millisecond,
	})
	defer cleanup()

	resp, err := http.Get(baseURL + "/test")
	if err != nil {
		t.Fatalf("GET /test: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200 (after retry)", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	if string(body) != "success" {
		t.Errorf("body = %q, want \"success\"", string(body))
	}

	if got := atomic.LoadInt64(&callCount); got != 3 {
		t.Errorf("backend callCount = %d, want 3 (2 failures + 1 success)", got)
	}
}

func TestProxyRetryOn429(t *testing.T) {
	var callCount int64
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt64(&callCount, 1)
		if n < 2 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests) // 429
			_, _ = w.Write([]byte("rate limited"))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer backend.Close()

	_, baseURL, cleanup := startTestProxyWithRetry(t, backend.URL, 1, RetryConfig{
		MaxRetries: 3,
		BaseDelay:  10 * time.Millisecond,
		MaxDelay:   50 * time.Millisecond,
	})
	defer cleanup()

	resp, err := http.Get(baseURL + "/test")
	if err != nil {
		t.Fatalf("GET /test: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200 (after 429 retry)", resp.StatusCode)
	}
}

func TestProxyNoRetryOn400(t *testing.T) {
	var callCount int64
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&callCount, 1)
		w.WriteHeader(http.StatusBadRequest) // 400 -- client error
		_, _ = w.Write([]byte("bad request"))
	}))
	defer backend.Close()

	_, baseURL, cleanup := startTestProxyWithRetry(t, backend.URL, 1, RetryConfig{
		MaxRetries: 3,
		BaseDelay:  10 * time.Millisecond,
		MaxDelay:   50 * time.Millisecond,
	})
	defer cleanup()

	resp, err := http.Get(baseURL + "/test")
	if err != nil {
		t.Fatalf("GET /test: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 (no retry on client error)", resp.StatusCode)
	}

	if got := atomic.LoadInt64(&callCount); got != 1 {
		t.Errorf("backend callCount = %d, want 1 (no retry)", got)
	}
}

func TestProxyRetryExhaustedReturnsLastStatus(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable) // 503
		_, _ = w.Write([]byte("unavailable"))
	}))
	defer backend.Close()

	_, baseURL, cleanup := startTestProxyWithRetry(t, backend.URL, 1, RetryConfig{
		MaxRetries: 2,
		BaseDelay:  10 * time.Millisecond,
		MaxDelay:   50 * time.Millisecond,
	})
	defer cleanup()

	resp, err := http.Get(baseURL + "/test")
	if err != nil {
		t.Fatalf("GET /test: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503 (all retries exhausted)", resp.StatusCode)
	}
}

func TestProxyRetryDisabledWhenMaxRetriesZero(t *testing.T) {
	var callCount int64
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&callCount, 1)
		w.WriteHeader(http.StatusBadGateway) // 502
	}))
	defer backend.Close()

	_, baseURL, cleanup := startTestProxyWithRetry(t, backend.URL, 1, RetryConfig{
		MaxRetries: 0, // disabled
	})
	defer cleanup()

	resp, err := http.Get(baseURL + "/test")
	if err != nil {
		t.Fatalf("GET /test: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("status = %d, want 502 (retry disabled, pass-through)", resp.StatusCode)
	}

	if got := atomic.LoadInt64(&callCount); got != 1 {
		t.Errorf("backend callCount = %d, want 1 (retry disabled)", got)
	}
}

func TestProxyRetryPreservesHeadersOnSuccess(t *testing.T) {
	var callCount int64
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt64(&callCount, 1)
		if n == 1 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		w.Header().Set("X-Custom", "value")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer backend.Close()

	_, baseURL, cleanup := startTestProxyWithRetry(t, backend.URL, 1, RetryConfig{
		MaxRetries: 2,
		BaseDelay:  10 * time.Millisecond,
		MaxDelay:   50 * time.Millisecond,
	})
	defer cleanup()

	resp, err := http.Get(baseURL + "/test")
	if err != nil {
		t.Fatalf("GET /test: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if v := resp.Header.Get("X-Custom"); v != "value" {
		t.Errorf("X-Custom = %q, want \"value\"", v)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want \"application/json\"", ct)
	}
}

func TestProxyRetryWithPostBody(t *testing.T) {
	var receivedBody string
	var callCount int64
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt64(&callCount, 1)
		body, _ := io.ReadAll(r.Body)
		receivedBody = string(body)
		if n < 2 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer backend.Close()

	_, baseURL, cleanup := startTestProxyWithRetry(t, backend.URL, 1, RetryConfig{
		MaxRetries: 3,
		BaseDelay:  10 * time.Millisecond,
		MaxDelay:   50 * time.Millisecond,
	})
	defer cleanup()

	resp, err := http.Post(baseURL+"/test", "application/json",
		strings.NewReader(`{"prompt":"hello"}`))
	if err != nil {
		t.Fatalf("POST /test: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}

	if receivedBody != `{"prompt":"hello"}` {
		t.Errorf("received body = %q, want original request body on retry", receivedBody)
	}
}

func TestProxyRetryHealthEndpointReportsStats(t *testing.T) {
	var callCount int64
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt64(&callCount, 1)
		if n < 3 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer backend.Close()

	_, baseURL, cleanup := startTestProxyWithRetry(t, backend.URL, 1, RetryConfig{
		MaxRetries: 3,
		BaseDelay:  10 * time.Millisecond,
		MaxDelay:   50 * time.Millisecond,
	})
	defer cleanup()

	// Make a request that triggers retries.
	resp, err := http.Get(baseURL + "/test")
	if err != nil {
		t.Fatalf("GET /test: %v", err)
	}
	_ = resp.Body.Close()

	// Check health endpoint for retry stats.
	resp, err = http.Get(baseURL + "/health")
	if err != nil {
		t.Fatalf("GET /health: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	var health map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&health); err != nil {
		t.Fatalf("decode health JSON: %v", err)
	}

	retryAttempts, ok := health["retry_attempts"].(float64)
	if !ok || retryAttempts < 1 {
		t.Errorf("retry_attempts = %v, want >= 1", health["retry_attempts"])
	}

	retrySuccesses, ok := health["retry_successes"].(float64)
	if !ok || retrySuccesses < 1 {
		t.Errorf("retry_successes = %v, want >= 1", health["retry_successes"])
	}
}

func TestProxyRetryBodyTooLargeReturns413(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	_, baseURL, cleanup := startTestProxyWithRetry(t, backend.URL, 1, RetryConfig{
		MaxRetries:  3,
		BaseDelay:   10 * time.Millisecond,
		MaxDelay:    50 * time.Millisecond,
		MaxBodySize: 10, // 10 bytes limit
	})
	defer cleanup()

	// Send a body larger than MaxBodySize.
	bigBody := strings.NewReader(strings.Repeat("x", 20))
	resp, err := http.Post(baseURL+"/test", "text/plain", bigBody)
	if err != nil {
		t.Fatalf("POST /test: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want 413 (body too large)", resp.StatusCode)
	}
}

// ---------------------------------------------------------------------------
// Per-model concurrency and model extraction tests
// ---------------------------------------------------------------------------

// startTestProxyWithModels starts a proxy with per-model registry configuration.
func startTestProxyWithModels(t *testing.T, backendURL string, models map[string]int, defaultCap int) (*Proxy, string, func()) {
	t.Helper()
	p := New(Config{
		TargetURL: backendURL,
		Port:      0,
		Models:    models,
	})

	go func() {
		p.Start() //nolint:errcheck
	}()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if port := p.Port(); port > 0 {
			return p, fmt.Sprintf("http://localhost:%d", port), func() { p.Stop() }
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("proxy did not start listening within 3s")
	return nil, "", nil
}

func TestPerModelConcurrencyIsolation(t *testing.T) {
	// Model A at concurrency=1 must not block model B.
	var activeA, activeB int64
	gateA := make(chan struct{}) // keeps model A requests blocked

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		model := r.Header.Get("X-Model")
		if model == "model-a" {
			atomic.AddInt64(&activeA, 1)
			<-gateA
			atomic.AddInt64(&activeA, -1)
		} else {
			atomic.AddInt64(&activeB, 1)
			atomic.AddInt64(&activeB, -1)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	_, baseURL, cleanup := startTestProxyWithModels(t, backend.URL, map[string]int{
		"model-a": 1,
		"model-b": 2,
	}, 0)
	defer cleanup()

	// Send a model-a request that will block at backend.
	var wgA sync.WaitGroup
	wgA.Add(1)
	go func() {
		defer wgA.Done()
		http.Post(baseURL+"/v1/chat/completions", "application/json", //nolint:errcheck
			strings.NewReader(`{"model":"model-a"}`))
	}()

	// Let model-a reach the backend and park there.
	time.Sleep(150 * time.Millisecond)

	// model-b requests must complete without waiting.
	done := make(chan struct{})
	go func() {
		defer close(done)
		resp, err := http.Post(baseURL+"/v1/chat/completions", "application/json",
			strings.NewReader(`{"model":"model-b"}`))
		if err == nil {
			_ = resp.Body.Close()
		}
	}()

	select {
	case <-done:
		// model-b completed independently — good.
	case <-time.After(2 * time.Second):
		t.Error("model-b request was blocked by model-a semaphore")
	}

	close(gateA)
	wgA.Wait()
}

func TestModelExtractionFromBody(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Echo the X-Model header set by the proxy.
		w.Header().Set("X-Echo-Model", r.Header.Get("X-Model"))
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	_, baseURL, cleanup := startTestProxyWithModels(t, backend.URL, map[string]int{
		"glm-5": 2,
	}, 0)
	defer cleanup()

	resp, err := http.Post(baseURL+"/v1/chat/completions", "application/json",
		strings.NewReader(`{"model":"glm-5","messages":[]}`))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("X-Echo-Model"); got != "glm-5" {
		t.Errorf("X-Model header forwarded as %q, want glm-5", got)
	}
}

func TestUnknownModelWithNoDefault(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	_, baseURL, cleanup := startTestProxyWithModels(t, backend.URL, map[string]int{
		"glm-5": 2,
	}, 0) // no default cap
	defer cleanup()

	resp, err := http.Post(baseURL+"/v1/chat/completions", "application/json",
		strings.NewReader(`{"model":"unknown-model"}`))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for unknown model", resp.StatusCode)
	}
}

func TestUnknownModelWithDefault(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	// defaultCap is set via global Concurrency field when Models is nil.
	p := New(Config{
		TargetURL:   backend.URL,
		Port:        0,
		Concurrency: 2, // global fallback when no Models map
	})
	go func() {
		p.Start() //nolint:errcheck
	}()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if port := p.Port(); port > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	defer p.Stop()

	baseURL := fmt.Sprintf("http://localhost:%d", p.Port())

	// When no per-model map is configured, any model should be accepted (global
	// semaphore backward-compat mode).
	resp, err := http.Post(baseURL+"/v1/chat/completions", "application/json",
		strings.NewReader(`{"model":"any-model"}`))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200 (global semaphore mode)", resp.StatusCode)
	}
}

func TestMissingModelFieldReturns400(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	_, baseURL, cleanup := startTestProxyWithModels(t, backend.URL, map[string]int{
		"glm-5": 2,
	}, 0)
	defer cleanup()

	// Request body has no "model" field.
	resp, err := http.Post(baseURL+"/v1/chat/completions", "application/json",
		strings.NewReader(`{"messages":[]}`))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for missing model field", resp.StatusCode)
	}
}

func TestLoggingResponseWriterCapturesStatus(t *testing.T) {
	lrw := &loggingResponseWriter{
		ResponseWriter: httptest.NewRecorder(),
		statusCode:     http.StatusOK,
	}

	lrw.WriteHeader(http.StatusNotFound)
	if lrw.statusCode != http.StatusNotFound {
		t.Errorf("statusCode = %d, want 404", lrw.statusCode)
	}
}

func TestLoggingResponseWriterCapturesBytesWritten(t *testing.T) {
	rec := httptest.NewRecorder()
	lrw := &loggingResponseWriter{
		ResponseWriter: rec,
		statusCode:     http.StatusOK,
	}

	payload := []byte("hello world")
	n, err := lrw.Write(payload)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if n != len(payload) {
		t.Errorf("Write returned %d, want %d", n, len(payload))
	}
	if lrw.bytesWritten != int64(len(payload)) {
		t.Errorf("bytesWritten = %d, want %d", lrw.bytesWritten, len(payload))
	}
}

func TestHealthEndpointWithPerModelStats(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer backend.Close()

	_, baseURL, cleanup := startTestProxyWithModels(t, backend.URL, map[string]int{
		"glm-5": 3,
		"glm-4": 1,
	}, 0)
	defer cleanup()

	// Make a request to generate model stats.
	http.Post(baseURL+"/v1/chat/completions", "application/json", //nolint:errcheck
		strings.NewReader(`{"model":"glm-5"}`))

	resp, err := http.Get(baseURL + "/health")
	if err != nil {
		t.Fatalf("GET /health: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	var body map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode health JSON: %v", err)
	}

	if body["status"] != "ok" {
		t.Errorf("status = %v, want ok", body["status"])
	}

	models, ok := body["models"].(map[string]interface{})
	if !ok {
		t.Fatalf("health response missing 'models' map; got: %v", body)
	}

	// glm-5 should appear in models stats.
	glm5, ok := models["glm-5"].(map[string]interface{})
	if !ok {
		t.Fatalf("models[glm-5] missing; got keys: %v", modelKeys(models))
	}

	if concurrency, ok := glm5["concurrency"].(float64); !ok || concurrency != 3 {
		t.Errorf("models[glm-5].concurrency = %v, want 3", glm5["concurrency"])
	}
	if total, ok := glm5["total"].(float64); !ok || total < 1 {
		t.Errorf("models[glm-5].total = %v, want >= 1", glm5["total"])
	}
}

func modelKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

func TestBackwardCompatNoModelsMap(t *testing.T) {
	// When Config.Models is nil/empty, the proxy should use a global semaphore
	// and not require model extraction — backward-compat mode.
	var callCount int64
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&callCount, 1)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer backend.Close()

	p := New(Config{
		TargetURL:   backend.URL,
		Concurrency: 2,
		Port:        0,
	})
	go func() {
		p.Start() //nolint:errcheck
	}()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if port := p.Port(); port > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	defer p.Stop()

	baseURL := fmt.Sprintf("http://localhost:%d", p.Port())

	// Plain GET without JSON body must work.
	resp, err := http.Get(baseURL + "/some/path")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200 (backward compat: no model extraction)", resp.StatusCode)
	}
	if got := atomic.LoadInt64(&callCount); got != 1 {
		t.Errorf("backend call count = %d, want 1", got)
	}
}
