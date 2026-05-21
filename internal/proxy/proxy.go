// Package proxy implements a rate-limiting reverse proxy for the Z.AI API.
// All Claude CLI instances route API requests through this local proxy, which
// serializes requests using per-model semaphores so only N concurrent requests
// per model hit Z.AI at a time, preventing 429 rate-limit errors.
package proxy

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"regexp"
	"sync"
	"sync/atomic"
	"time"

	"github.com/veschin/GoLeM/internal/retry"
)

// RetryConfig controls automatic retry behavior for transient upstream errors.
// When MaxRetries is 0 (zero value), retry is disabled and the proxy behaves
// exactly as before -- no buffering, no retry.
type RetryConfig struct {
	MaxRetries  int           // maximum retry attempts (0 = disabled, default 3)
	BaseDelay   time.Duration // initial backoff delay (default 1s)
	MaxDelay    time.Duration // maximum backoff delay (default 30s)
	MaxBodySize int64         // max request body to buffer for retry, in bytes (default 10MB)
}

// Config holds configuration for the proxy server.
type Config struct {
	TargetURL   string        // e.g. "https://api.z.ai/api/anthropic"
	Concurrency int           // global max concurrent requests used when Models is empty (default 1)
	IdleTimeout time.Duration // auto-shutdown after inactivity (0 = never)
	Port        int           // 0 = OS assigns a free port
	LogFile     string        // path for log output (empty = stderr)
	Retry       RetryConfig   // retry configuration (zero value = disabled)
	Models      map[string]int // per-model concurrency limits; when non-empty, Concurrency is ignored
}

// maxBodySize is the default maximum request body size to buffer for retry.
const maxBodySize = 10 * 1024 * 1024 // 10 MB

// modelStat tracks per-model request counts and error rates.
type modelStat struct {
	total  atomic.Int64
	active atomic.Int64
	errors atomic.Int64
}

// ErrModelNotFound is returned when the "model" field is absent from a request body.
var ErrModelNotFound = errors.New("model field not found in request body")

// Proxy is a rate-limiting reverse proxy server.
type Proxy struct {
	cfg      Config
	registry *ModelRegistry // per-model semaphores (set in New)

	listener net.Listener
	idle     *time.Timer
	mu       sync.Mutex

	active         int64 // atomic active request count
	totalRequests  int64 // atomic counter for total requests served
	retryAttempts  int64 // atomic counter for total retry attempts
	retrySuccesses int64 // atomic counter for requests that succeeded after retry
	startTime      time.Time
	logger         *log.Logger

	modelStats sync.Map // map[string]*modelStat
}

// New creates a new Proxy from cfg.
// When cfg.Models is non-empty, per-model semaphores are created.
// When cfg.Models is empty, a single global semaphore of size Concurrency is used
// (backward-compat mode: no model extraction, any request passes through).
func New(cfg Config) *Proxy {
	var registry *ModelRegistry
	if len(cfg.Models) > 0 {
		// Per-model mode: build registry, no global default (unknown -> 400).
		registry = NewRegistryFromMap(cfg.Models, 0)
	} else {
		// Global semaphore backward-compat mode.
		concurrency := cfg.Concurrency
		if concurrency <= 0 {
			concurrency = 1
		}
		cfg.Concurrency = concurrency
		// nil registry signals global-semaphore mode in proxyHandler.
		registry = nil
	}

	var logOut io.Writer = os.Stderr
	if cfg.LogFile != "" {
		f, err := os.OpenFile(cfg.LogFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err == nil {
			logOut = f
		}
	}

	return &Proxy{
		cfg:       cfg,
		registry:  registry,
		startTime: time.Now(),
		logger:    log.New(logOut, "[proxy] ", log.LstdFlags),
	}
}

// Start binds a TCP listener, registers routes, and begins serving requests.
func (p *Proxy) Start() (net.Addr, error) {
	ln, err := net.Listen("tcp", fmt.Sprintf("localhost:%d", p.cfg.Port))
	if err != nil {
		return nil, fmt.Errorf("proxy: listen: %w", err)
	}

	p.mu.Lock()
	p.listener = ln
	p.mu.Unlock()

	rp, err := p.buildReverseProxy()
	if err != nil {
		_ = ln.Close()
		return nil, fmt.Errorf("proxy: build reverse proxy: %w", err)
	}

	if p.cfg.IdleTimeout > 0 {
		p.mu.Lock()
		p.idle = time.AfterFunc(p.cfg.IdleTimeout, func() {
			p.logger.Printf("idle timeout reached, shutting down")
			p.Stop()
		})
		p.mu.Unlock()
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", p.healthHandler)
	mux.HandleFunc("/", p.proxyHandler(rp))

	mode := "per-model"
	if p.registry == nil {
		mode = fmt.Sprintf("global concurrency=%d", p.cfg.Concurrency)
	}
	p.logger.Printf("listening on %s (mode=%s target=%s)", ln.Addr(), mode, p.cfg.TargetURL)

	if err := http.Serve(ln, mux); err != nil {
		if isClosedErr(err) {
			return ln.Addr(), nil
		}
		return ln.Addr(), fmt.Errorf("proxy: serve: %w", err)
	}
	return ln.Addr(), nil
}

// Port returns the actual TCP port the proxy is listening on.
func (p *Proxy) Port() int {
	p.mu.Lock()
	ln := p.listener
	p.mu.Unlock()
	if ln == nil {
		return 0
	}
	addr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		return 0
	}
	return addr.Port
}

// Stop closes the listener, causing Start to return.
func (p *Proxy) Stop() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.idle != nil {
		p.idle.Stop()
	}
	if p.listener != nil {
		_ = p.listener.Close()
	}
}

// resetIdle resets the idle timer on every proxied request.
func (p *Proxy) resetIdle() {
	if p.cfg.IdleTimeout <= 0 {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.idle != nil {
		p.idle.Reset(p.cfg.IdleTimeout)
	}
}

// getOrCreateModelStat returns the modelStat for the given model, creating it
// if it does not exist yet.
func (p *Proxy) getOrCreateModelStat(model string) *modelStat {
	v, _ := p.modelStats.LoadOrStore(model, &modelStat{})
	return v.(*modelStat)
}

// healthHandler returns a JSON health document with per-model stats when
// operating in per-model mode.
func (p *Proxy) healthHandler(w http.ResponseWriter, r *http.Request) {
	active := atomic.LoadInt64(&p.active)
	total := atomic.LoadInt64(&p.totalRequests)
	retryAttempts := atomic.LoadInt64(&p.retryAttempts)
	retrySuccesses := atomic.LoadInt64(&p.retrySuccesses)
	uptime := int64(time.Since(p.startTime).Seconds())

	body := map[string]interface{}{
		"status":          "ok",
		"active":          active,
		"port":            p.Port(),
		"total_requests":  total,
		"uptime_sec":      uptime,
		"retry_attempts":  retryAttempts,
		"retry_successes": retrySuccesses,
	}

	if p.registry != nil {
		// Per-model mode: include per-model stats and queued count.
		modelsInfo := make(map[string]interface{})
		for _, name := range p.registry.Models() {
			sem, _ := p.registry.Get(name)
			stat := p.getOrCreateModelStat(name)
			modelsInfo[name] = map[string]interface{}{
				"concurrency": cap(sem),
				"active":      stat.active.Load(),
				"total":       stat.total.Load(),
				"errors":      stat.errors.Load(),
			}
		}
		// Also include stats for models seen in traffic but not in registry
		// (they used the default semaphore).
		p.modelStats.Range(func(key, value any) bool {
			name := key.(string)
			if _, ok := modelsInfo[name]; !ok {
				stat := value.(*modelStat)
				modelsInfo[name] = map[string]interface{}{
					"concurrency": p.registry.Concurrency(name),
					"active":      stat.active.Load(),
					"total":       stat.total.Load(),
					"errors":      stat.errors.Load(),
				}
			}
			return true
		})
		body["models"] = modelsInfo
	} else {
		// Global semaphore mode: per-sem queue depth is not tracked, so always
		// report 0 to preserve the field shape for backward-compatible clients.
		body["queued"] = int64(0)
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(body)
}

// proxyHandler wraps a reverse proxy inside a semaphore gate with optional retry.
// When registry is nil (backward-compat mode), it uses a global semaphore built
// from cfg.Concurrency without model extraction.
func (p *Proxy) proxyHandler(rp *httputil.ReverseProxy) http.HandlerFunc {
	// Global semaphore (only used when registry == nil).
	var globalSem chan struct{}
	if p.registry == nil {
		globalSem = make(chan struct{}, p.cfg.Concurrency)
	}

	return func(w http.ResponseWriter, r *http.Request) {
		p.resetIdle()

		var (
			sem       chan struct{}
			model     string
			bodyBytes []byte
		)

		if p.registry != nil {
			// Per-model mode: extract model from body to select the right semaphore.
			var err error
			bodyBytes, model, err = extractModelFromBody(r)
			if err != nil || model == "" {
				p.logger.Printf("[REJECT] %s %s | reason=missing_model", r.Method, r.URL.Path)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]string{
					"error": fmt.Sprintf("failed to extract model: %v", ErrModelNotFound),
				})
				return
			}

			var ok bool
			sem, ok = p.registry.Get(model)
			if !ok {
				p.logger.Printf("[REJECT] %s %s | model=%s reason=unknown_model", r.Method, r.URL.Path, model)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]string{
					"error": fmt.Sprintf("unknown model: %s", model),
				})
				return
			}

			// Restore body so the upstream gets the original payload.
			r.Body = io.NopCloser(bytes.NewReader(bodyBytes))
			// Set X-Model header for upstream and logging.
			r.Header.Set("X-Model", model)
		} else {
			// Backward-compat global semaphore mode.
			sem = globalSem
		}

		queueStart := time.Now()
		sem <- struct{}{}
		waitDur := time.Since(queueStart)

		atomic.AddInt64(&p.active, 1)
		atomic.AddInt64(&p.totalRequests, 1)

		var stat *modelStat
		if model != "" {
			stat = p.getOrCreateModelStat(model)
			stat.total.Add(1)
			stat.active.Add(1)
		}

		start := time.Now()
		loggedStatus := http.StatusOK

		defer func() {
			atomic.AddInt64(&p.active, -1)
			<-sem
			if stat != nil {
				stat.active.Add(-1)
				if loggedStatus >= 400 {
					stat.errors.Add(1)
				}
			}
			p.logger.Printf("%s %s model=%s wait=%s duration=%s status=%d",
				r.Method, r.URL.Path, model,
				waitDur.Round(time.Millisecond),
				time.Since(start).Round(time.Millisecond),
				loggedStatus)
		}()

		// If retry is disabled, use the direct path.
		if p.cfg.Retry.MaxRetries == 0 {
			lrw := &loggingResponseWriter{ResponseWriter: w, statusCode: http.StatusOK}
			rp.ServeHTTP(lrw, r)
			loggedStatus = lrw.statusCode
			return
		}

		// Retry path: buffer the body for replay.
		maxSize := p.cfg.Retry.MaxBodySize
		if maxSize <= 0 {
			maxSize = maxBodySize
		}

		if len(bodyBytes) == 0 {
			// Body not yet read (global semaphore mode or non-JSON request).
			var readErr error
			bodyBytes, readErr = io.ReadAll(io.LimitReader(r.Body, maxSize+1))
			_ = r.Body.Close()
			if readErr != nil {
				p.logger.Printf("retry: read body: %v", readErr)
				http.Error(w, "failed to read request body", http.StatusInternalServerError)
				loggedStatus = http.StatusInternalServerError
				return
			}
		}

		if int64(len(bodyBytes)) > maxSize {
			p.logger.Printf("retry: body too large: %d bytes (max %d)", len(bodyBytes), maxSize)
			http.Error(w, "request body too large for retry", http.StatusRequestEntityTooLarge)
			loggedStatus = http.StatusRequestEntityTooLarge
			return
		}

		maxRetries := p.cfg.Retry.MaxRetries
		baseDelay := p.cfg.Retry.BaseDelay
		if baseDelay <= 0 {
			baseDelay = retry.DefaultBaseDelay
		}
		maxDelay := p.cfg.Retry.MaxDelay
		if maxDelay <= 0 {
			maxDelay = retry.DefaultMaxDelay
		}

		var (
			lastBRW      *bufferedResponseWriter
			attemptCount int
		)

		errRetryable := errors.New("retryable upstream error")

		op := func() error {
			r.Body = io.NopCloser(bytes.NewReader(bodyBytes))
			r.GetBody = func() (io.ReadCloser, error) {
				return io.NopCloser(bytes.NewReader(bodyBytes)), nil
			}

			brw := newBufferedResponseWriter()
			rp.ServeHTTP(brw, r)
			lastBRW = brw
			attemptCount++

			if isRetryable(brw.statusCode) {
				atomic.AddInt64(&p.retryAttempts, 1)
				p.logger.Printf("retry: attempt %d/%d failed with status %d",
					attemptCount, maxRetries+1, brw.statusCode)
				return errRetryable
			}
			return nil
		}

		retryErr := retry.Do(r.Context(), op,
			retry.WithMaxAttempts(maxRetries+1),
			retry.WithBaseDelay(baseDelay),
			retry.WithMaxDelay(maxDelay),
			retry.WithRetryIf(func(err error) bool {
				return errors.Is(err, errRetryable)
			}),
		)

		if lastBRW != nil {
			if flushErr := lastBRW.flushTo(w); flushErr != nil {
				p.logger.Printf("retry: flush response: %v", flushErr)
			}
			loggedStatus = lastBRW.statusCode

			if retryErr == nil && attemptCount > 1 {
				atomic.AddInt64(&p.retrySuccesses, 1)
			}
		}

		if retryErr != nil && !errors.Is(retryErr, errRetryable) {
			p.logger.Printf("retry: aborted: %v", retryErr)
		}
	}
}

// extractModelFromBody reads the body, extracts the "model" field, and restores
// the body on the request.  Returns the raw body bytes and the model name.
func extractModelFromBody(r *http.Request) ([]byte, string, error) {
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, "", err
	}
	_ = r.Body.Close()
	r.Body = io.NopCloser(bytes.NewReader(bodyBytes))

	if len(bodyBytes) == 0 {
		return bodyBytes, "", ErrModelNotFound
	}

	var parsed struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(bodyBytes, &parsed); err != nil {
		return bodyBytes, "", err
	}
	return bodyBytes, parsed.Model, nil
}

// bufferedResponseWriter captures the HTTP response in memory.
type bufferedResponseWriter struct {
	header      http.Header
	body        bytes.Buffer
	statusCode  int
	wroteHeader bool
}

func newBufferedResponseWriter() *bufferedResponseWriter {
	return &bufferedResponseWriter{
		header: make(http.Header),
	}
}

func (b *bufferedResponseWriter) Header() http.Header {
	return b.header
}

func (b *bufferedResponseWriter) WriteHeader(code int) {
	if b.wroteHeader {
		return
	}
	b.statusCode = code
	b.wroteHeader = true
}

func (b *bufferedResponseWriter) Write(p []byte) (int, error) {
	if !b.wroteHeader {
		b.WriteHeader(http.StatusOK)
	}
	return b.body.Write(p)
}

func (b *bufferedResponseWriter) flushTo(w http.ResponseWriter) error {
	for k, vals := range b.header {
		for _, v := range vals {
			w.Header().Add(k, v)
		}
	}
	if b.wroteHeader {
		w.WriteHeader(b.statusCode)
	} else {
		w.WriteHeader(http.StatusOK)
	}
	_, err := b.body.WriteTo(w)
	return err
}

// isRetryable reports whether the given HTTP status code should trigger a retry.
func isRetryable(statusCode int) bool {
	return statusCode == http.StatusTooManyRequests ||
		(statusCode >= 500 && statusCode < 600)
}

// buildReverseProxy constructs and configures an httputil.ReverseProxy.
func (p *Proxy) buildReverseProxy() (*httputil.ReverseProxy, error) {
	target, err := url.Parse(p.cfg.TargetURL)
	if err != nil {
		return nil, fmt.Errorf("parse target URL: %w", err)
	}

	rp := httputil.NewSingleHostReverseProxy(target)
	rp.FlushInterval = -1

	rp.Transport = &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 5 * time.Minute,
		ExpectContinueTimeout: 1 * time.Second,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
	}

	targetPath := target.Path
	rp.Director = func(req *http.Request) {
		req.URL.Scheme = target.Scheme
		req.URL.Host = target.Host
		req.Host = target.Host
		req.URL.Path = joinPaths(targetPath, req.URL.Path)
		req.Header.Del("X-Forwarded-For")
	}

	rp.ModifyResponse = func(resp *http.Response) error {
		if resp.StatusCode >= 400 {
			// Cap upstream error body to 64KiB; a 5xx page from a CDN can be megabytes.
			bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
			_ = resp.Body.Close()
			resp.Body = io.NopCloser(bytes.NewReader(bodyBytes))
			p.logger.Printf("[UPSTREAM] %d | model=%s body=%s",
				resp.StatusCode, resp.Request.Header.Get("X-Model"), redactBody(bodyBytes, 512))
		}
		return nil
	}

	rp.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		p.logger.Printf("[ERROR] upstream failed | method=%s url=%s model=%s err=%v",
			r.Method, r.URL.String(), r.Header.Get("X-Model"), err)
		if urlErr, ok := err.(*url.Error); ok && urlErr.Timeout() {
			w.WriteHeader(http.StatusGatewayTimeout)
			return
		}
		w.WriteHeader(http.StatusBadGateway)
	}

	return rp, nil
}

// redactBodyKeyRE matches sensitive JSON fields whose values must be replaced
// before the upstream error body is logged. Z.AI's 4xx responses occasionally
// echo back the inbound key or token, so anything that looks like a credential
// is masked.
var redactBodyKeyRE = regexp.MustCompile(`(?i)"(api_key|authorization|x-api-key|token|access_token|bearer)"\s*:\s*"[^"]*"`)

// redactBody truncates b to max bytes (appending a marker) and replaces
// credential-like JSON fields with [REDACTED].
func redactBody(b []byte, max int) string {
	if len(b) > max {
		b = append([]byte(nil), b[:max]...)
		b = append(b, []byte("...[truncated]")...)
	}
	s := string(b)
	return redactBodyKeyRE.ReplaceAllString(s, `"$1":"[REDACTED]"`)
}

// joinPaths concatenates base and suffix, ensuring exactly one slash between them.
func joinPaths(base, suffix string) string {
	if base == "" {
		return suffix
	}
	if suffix == "" {
		return base
	}
	for len(base) > 0 && base[len(base)-1] == '/' {
		base = base[:len(base)-1]
	}
	if len(suffix) == 0 || suffix[0] != '/' {
		return base + "/" + suffix
	}
	return base + suffix
}

// loggingResponseWriter captures the HTTP status code and bytes written.
type loggingResponseWriter struct {
	http.ResponseWriter
	statusCode   int
	bytesWritten int64
}

func (l *loggingResponseWriter) WriteHeader(code int) {
	l.statusCode = code
	l.ResponseWriter.WriteHeader(code)
}

func (l *loggingResponseWriter) Write(b []byte) (int, error) {
	n, err := l.ResponseWriter.Write(b)
	l.bytesWritten += int64(n)
	return n, err
}

// isClosedErr reports whether err is the expected "use of closed network connection" error.
func isClosedErr(err error) bool {
	if err == nil {
		return false
	}
	return err.Error() == "http: Server closed" || isNetClosedErr(err)
}

func isNetClosedErr(err error) bool {
	if err == nil {
		return false
	}
	const closed = "use of closed network connection"
	type unwrapper interface{ Unwrap() error }
	for e := err; e != nil; {
		if e.Error() == closed {
			return true
		}
		u, ok := e.(unwrapper)
		if !ok {
			break
		}
		e = u.Unwrap()
	}
	return false
}
