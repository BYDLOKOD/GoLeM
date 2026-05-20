// Package proxy manages the lifecycle of the GoLeM rate-limiting proxy daemon.
// Multiple glm instances share one proxy process via PID/port files in configDir.
package proxy

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"time"
)

const lockFile = "proxy.lock"

const (
	pidFile  = "proxy.pid"
	portFile = "proxy.port"
	logFile  = "proxy.log"
)

// healthResponse is the expected JSON body from GET /health.
type healthResponse struct {
	Port int `json:"port"`
}

// EnsureRunning returns the proxy port, starting the daemon if it is not already running.
// glmBinary is the path to the glm executable, configDir is the directory used for
// PID/port/log files, targetURL is the upstream URL, and idleTimeout controls how long
// the proxy waits before shutting itself down when idle. Concurrency is controlled
// exclusively via per-model limits in the [models] TOML section; no global limit is applied.
//
// A file-based flock around the check-and-spawn sequence prevents concurrent callers
// from spawning duplicate proxy instances (TOCTOU race).
func EnsureRunning(glmBinary, configDir, targetURL string, idleTimeout time.Duration) (int, error) {
	// Acquire exclusive flock to serialize the IsRunning check + spawn.
	lockPath := filepath.Join(configDir, lockFile)
	lk, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return 0, fmt.Errorf("proxy: open lock file: %w", err)
	}
	defer func() { _ = lk.Close() }()

	if err := syscall.Flock(int(lk.Fd()), syscall.LOCK_EX); err != nil {
		return 0, fmt.Errorf("proxy: flock: %w", err)
	}
	defer func() { _ = syscall.Flock(int(lk.Fd()), syscall.LOCK_UN) }()

	// Re-check under lock — another caller may have started the proxy already.
	if port, alive := IsRunning(configDir); alive {
		return port, nil
	}

	// Open (or create) the log file for proxy stdout/stderr.
	logPath := filepath.Join(configDir, logFile)
	lf, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return 0, fmt.Errorf("proxy: open log file: %w", err)
	}
	defer func() { _ = lf.Close() }()

	cmd := exec.Command(
		glmBinary,
		"_proxy",
		"--port", "0",
		"--idle-timeout", strconv.Itoa(int(idleTimeout.Seconds())),
		"--target", targetURL,
		"--config-dir", configDir,
	)
	cmd.Stdout = lf
	cmd.Stderr = lf
	// Detach the proxy into its own process group so it survives the parent.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := cmd.Start(); err != nil {
		return 0, fmt.Errorf("proxy: start daemon: %w", err)
	}

	// Poll /health until the proxy is ready, up to 5 seconds.
	port, err := waitHealthy(configDir, 5*time.Second, 100*time.Millisecond)
	if err != nil {
		return 0, fmt.Errorf("proxy: daemon did not become healthy: %w", err)
	}
	return port, nil
}

// IsRunning checks whether the proxy daemon is alive by inspecting the PID and
// port files and probing the /health endpoint.  It returns (port, true) when the
// proxy is reachable, and (0, false) otherwise (stale files are cleaned up).
func IsRunning(configDir string) (port int, alive bool) {
	pid, port, err := readPIDPort(configDir)
	if err != nil {
		return 0, false
	}

	// Check whether the process is alive via signal 0.
	proc, err := os.FindProcess(pid)
	if err != nil {
		cleanPIDPort(configDir)
		return 0, false
	}
	if err := proc.Signal(syscall.Signal(0)); err != nil {
		cleanPIDPort(configDir)
		return 0, false
	}

	// Verify the proxy answers HTTP health checks.
	if ok := checkHealth(port, 1*time.Second); !ok {
		cleanPIDPort(configDir)
		return 0, false
	}
	return port, true
}

// WritePIDFile writes proxy.pid and proxy.port atomically to configDir.
// It is called by the proxy process itself immediately after it starts listening.
func WritePIDFile(configDir string, pid, port int) error {
	if err := writeAtomic(filepath.Join(configDir, pidFile), strconv.Itoa(pid)); err != nil {
		return fmt.Errorf("proxy: write pid file: %w", err)
	}
	if err := writeAtomic(filepath.Join(configDir, portFile), strconv.Itoa(port)); err != nil {
		return fmt.Errorf("proxy: write port file: %w", err)
	}
	return nil
}

// CleanPIDFile removes proxy.pid and proxy.port from configDir.
// It is called by the proxy process at shutdown.
func CleanPIDFile(configDir string) error {
	var firstErr error
	for _, name := range []string{pidFile, portFile} {
		path := filepath.Join(configDir, name)
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			if firstErr == nil {
				firstErr = fmt.Errorf("proxy: remove %s: %w", name, err)
			}
		}
	}
	return firstErr
}

// Stop gracefully stops the proxy daemon identified by the PID file in configDir.
// It sends SIGTERM and waits up to 3 seconds before sending SIGKILL, then cleans
// up the PID and port files.
func Stop(configDir string) error {
	pidData, err := os.ReadFile(filepath.Join(configDir, pidFile))
	if err != nil {
		if os.IsNotExist(err) {
			return nil // Already stopped.
		}
		return fmt.Errorf("proxy: read pid file: %w", err)
	}
	pid, err := strconv.Atoi(string(pidData))
	if err != nil {
		return fmt.Errorf("proxy: parse pid: %w", err)
	}

	proc, err := os.FindProcess(pid)
	if err != nil {
		cleanPIDPort(configDir)
		return nil
	}

	// Attempt graceful shutdown with SIGTERM.
	if err := proc.Signal(syscall.SIGTERM); err != nil {
		// Process is already gone.
		cleanPIDPort(configDir)
		return nil
	}

	// Poll for up to 3 seconds.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if err := proc.Signal(syscall.Signal(0)); err != nil {
			// Process exited.
			cleanPIDPort(configDir)
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}

	// Still alive — force kill.
	if err := proc.Signal(syscall.SIGKILL); err != nil && err.Error() != "os: process already finished" {
		return fmt.Errorf("proxy: SIGKILL: %w", err)
	}
	cleanPIDPort(configDir)
	return nil
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

// readPIDPort reads the PID and port from their respective files in configDir.
func readPIDPort(configDir string) (pid, port int, err error) {
	pidData, err := os.ReadFile(filepath.Join(configDir, pidFile))
	if err != nil {
		return 0, 0, err
	}
	pid, err = strconv.Atoi(string(pidData))
	if err != nil {
		return 0, 0, fmt.Errorf("proxy: parse pid: %w", err)
	}

	portData, err := os.ReadFile(filepath.Join(configDir, portFile))
	if err != nil {
		return 0, 0, err
	}
	port, err = strconv.Atoi(string(portData))
	if err != nil {
		return 0, 0, fmt.Errorf("proxy: parse port: %w", err)
	}
	return pid, port, nil
}

// cleanPIDPort silently removes both files, ignoring errors.
func cleanPIDPort(configDir string) {
	_ = os.Remove(filepath.Join(configDir, pidFile))
	_ = os.Remove(filepath.Join(configDir, portFile))
}

// checkHealth probes GET http://localhost:{port}/health with the given timeout.
// Returns true only when the server responds with HTTP 200.
func checkHealth(port int, timeout time.Duration) bool {
	client := &http.Client{Timeout: timeout}
	url := fmt.Sprintf("http://localhost:%d/health", port)
	resp, err := client.Get(url)
	if err != nil {
		return false
	}
	defer func() { _ = resp.Body.Close() }()
	return resp.StatusCode == http.StatusOK
}

// waitHealthy polls the proxy until it exposes a healthy /health endpoint that
// returns a port in its JSON body, or until the overall timeout is exceeded.
// The port is read from the port file (written by the proxy at startup) and then
// confirmed via an HTTP health check.
func waitHealthy(configDir string, total, interval time.Duration) (int, error) {
	deadline := time.Now().Add(total)
	for time.Now().Before(deadline) {
		// Read port file; the proxy writes it after binding.
		portData, err := os.ReadFile(filepath.Join(configDir, portFile))
		if err == nil {
			port, err := strconv.Atoi(string(portData))
			if err == nil && port > 0 {
				// Confirm via HTTP.
				client := &http.Client{Timeout: 1 * time.Second}
				url := fmt.Sprintf("http://localhost:%d/health", port)
				resp, err := client.Get(url)
				if err == nil {
					defer func() { _ = resp.Body.Close() }()
					if resp.StatusCode == http.StatusOK {
						// Try to extract port from response body; fall back to file value.
						var hr healthResponse
						if jsonErr := json.NewDecoder(resp.Body).Decode(&hr); jsonErr == nil && hr.Port > 0 {
							return hr.Port, nil
						}
						return port, nil
					}
				}
			}
		}
		time.Sleep(interval)
	}
	return 0, fmt.Errorf("timed out after %s", total)
}

// writeAtomic writes data to path by first writing to a temporary file in the
// same directory and then renaming, ensuring an atomic update.
func writeAtomic(path, data string) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".proxy-tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()

	if _, err := tmp.WriteString(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return nil
}
