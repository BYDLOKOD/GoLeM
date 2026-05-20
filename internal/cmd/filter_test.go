package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// =============================================================================
// Dataset helpers
// =============================================================================

// jobSpec describes one job in the standard 8-job test dataset.
type jobSpec struct {
	jobID     string
	status    string
	project   string // basename of project directory (e.g. "my-app")
	createdAt string
}

// standardDataset mirrors the BDD background table.
var standardDataset = []jobSpec{
	{"job-20260227-153000-aa11bb22", "running", "my-app", "2026-02-27T15:30:00+03:00"},
	{"job-20260227-151500-cc33dd44", "running", "api-server", "2026-02-27T15:15:00+03:00"},
	{"job-20260227-144500-ee55ff66", "done", "my-app", "2026-02-27T14:45:00+03:00"},
	{"job-20260227-120000-a1b2c3d4", "done", "my-app", "2026-02-27T12:00:00+03:00"},
	{"job-20260227-110000-e5f6a7b8", "failed", "api-server", "2026-02-27T11:00:00+03:00"},
	{"job-20260227-100000-c9d0e1f2", "queued", "my-app", "2026-02-27T10:00:00+03:00"},
	{"job-20260227-090000-a3b4c5d6", "timeout", "api-server", "2026-02-27T09:00:00+03:00"},
	{"job-20260227-080000-e7f8a9b0", "killed", "api-server", "2026-02-27T08:00:00+03:00"},
}

// buildDataset creates the standard 8-job directory structure under root.
// The project directory name encodes just the basename (e.g. "my-app-<crc>").
// For testing we use the basename directly as the project directory name since
// FilterJobs matches by prefix.
func buildDataset(t *testing.T, root string) []JobEntry {
	t.Helper()
	var entries []JobEntry
	for _, s := range standardDataset {
		// Use project basename as directory name for simplicity.
		projDir := s.project + "-1234567890"
		dir := filepath.Join(root, projDir, s.jobID)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("buildDataset mkdir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dir, "status"), []byte(s.status), 0o644); err != nil {
			t.Fatalf("buildDataset write status: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dir, "started_at.txt"), []byte(s.createdAt), 0o644); err != nil {
			t.Fatalf("buildDataset write started_at: %v", err)
		}

		ts, _ := time.Parse(time.RFC3339, s.createdAt)
		entries = append(entries, JobEntry{
			JobID:     s.jobID,
			Status:    s.status,
			StartedAt: &ts,
			Dir:       dir,
		})
	}
	return entries
}

// =============================================================================
// AC1: Filter by single status
// =============================================================================

// Scenario: Filter jobs by a single status
func TestFilterJobsByASingleStatus(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	jobs := buildDataset(t, root)

	opts, err := ParseStatusFilter("running")
	if err != nil {
		t.Fatalf("ParseStatusFilter: %v", err)
	}
	filter := &FilterOptions{Statuses: opts}
	result := FilterJobs(jobs, filter)

	if len(result) != 2 {
		t.Fatalf("expected 2 jobs, got %d", len(result))
	}
	for _, j := range result {
		if j.Status != "running" {
			t.Errorf("expected status running, got %q", j.Status)
		}
	}
	ids := jobIDs(result)
	assertContains(t, ids, "job-20260227-153000-aa11bb22")
	assertContains(t, ids, "job-20260227-151500-cc33dd44")
}

// Scenario: Filter jobs by done status
func TestFilterJobsByDoneStatus(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	jobs := buildDataset(t, root)

	opts, err := ParseStatusFilter("done")
	if err != nil {
		t.Fatalf("ParseStatusFilter: %v", err)
	}
	result := FilterJobs(jobs, &FilterOptions{Statuses: opts})

	if len(result) != 2 {
		t.Fatalf("expected 2 jobs, got %d", len(result))
	}
	for _, j := range result {
		if j.Status != "done" {
			t.Errorf("expected status done, got %q", j.Status)
		}
	}
}

// Scenario: Filter jobs by queued status
func TestFilterJobsByQueuedStatus(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	jobs := buildDataset(t, root)

	opts, err := ParseStatusFilter("queued")
	if err != nil {
		t.Fatalf("ParseStatusFilter: %v", err)
	}
	result := FilterJobs(jobs, &FilterOptions{Statuses: opts})

	if len(result) != 1 {
		t.Fatalf("expected 1 job, got %d", len(result))
	}
	assertContains(t, jobIDs(result), "job-20260227-100000-c9d0e1f2")
}

// =============================================================================
// AC1: Filter by multiple comma-separated statuses
// =============================================================================

// Scenario: Filter jobs by multiple statuses
func TestFilterJobsByMultipleStatuses(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	jobs := buildDataset(t, root)

	opts, err := ParseStatusFilter("done,failed")
	if err != nil {
		t.Fatalf("ParseStatusFilter: %v", err)
	}
	result := FilterJobs(jobs, &FilterOptions{Statuses: opts})

	if len(result) != 3 {
		t.Fatalf("expected 3 jobs, got %d", len(result))
	}
	for _, j := range result {
		if j.Status != "done" && j.Status != "failed" {
			t.Errorf("expected status done or failed, got %q", j.Status)
		}
	}
	ids := jobIDs(result)
	assertContains(t, ids, "job-20260227-144500-ee55ff66")
	assertContains(t, ids, "job-20260227-120000-a1b2c3d4")
	assertContains(t, ids, "job-20260227-110000-e5f6a7b8")
}

// Scenario: Filter jobs by all terminal statuses
func TestFilterJobsByAllTerminalStatuses(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	jobs := buildDataset(t, root)

	opts, err := ParseStatusFilter("done,failed,timeout,killed")
	if err != nil {
		t.Fatalf("ParseStatusFilter: %v", err)
	}
	result := FilterJobs(jobs, &FilterOptions{Statuses: opts})

	if len(result) != 5 {
		t.Fatalf("expected 5 jobs, got %d", len(result))
	}
}

// =============================================================================
// AC2: Filter by project ID with prefix match
// =============================================================================

// Scenario: Filter jobs by project ID prefix
func TestFilterJobsByProjectIDPrefix(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	jobs := buildDataset(t, root)

	result := FilterJobs(jobs, &FilterOptions{ProjectPrefix: "my-app"})

	if len(result) != 4 {
		t.Fatalf("expected 4 jobs for my-app, got %d", len(result))
	}
	ids := jobIDs(result)
	assertContains(t, ids, "job-20260227-153000-aa11bb22")
	assertContains(t, ids, "job-20260227-144500-ee55ff66")
	assertContains(t, ids, "job-20260227-120000-a1b2c3d4")
	assertContains(t, ids, "job-20260227-100000-c9d0e1f2")
}

// Scenario: Filter jobs by project ID prefix for api-server
func TestFilterJobsByProjectIDPrefixForApiServer(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	jobs := buildDataset(t, root)

	result := FilterJobs(jobs, &FilterOptions{ProjectPrefix: "api-server"})

	if len(result) != 4 {
		t.Fatalf("expected 4 jobs for api-server, got %d", len(result))
	}
}

// Scenario: Filter by partial project prefix
func TestFilterByPartialProjectPrefix(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	jobs := buildDataset(t, root)

	// "my" is a prefix of "my-app-1234567890".
	result := FilterJobs(jobs, &FilterOptions{ProjectPrefix: "my"})

	if len(result) != 4 {
		t.Fatalf("expected 4 jobs for prefix 'my', got %d", len(result))
	}
	for _, j := range result {
		if !strings.HasPrefix(filepath.Base(filepath.Dir(j.Dir)), "my") {
			t.Errorf("job %q does not belong to a my-app project", j.JobID)
		}
	}
}

// Scenario: Filter by project with no matches
func TestFilterByProjectWithNoMatches(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	jobs := buildDataset(t, root)

	result := FilterJobs(jobs, &FilterOptions{ProjectPrefix: "nonexistent-project"})

	if len(result) != 0 {
		t.Errorf("expected 0 jobs, got %d", len(result))
	}
}

// =============================================================================
// AC3: Filter by time using Go duration format
// =============================================================================

// fixedNow returns a fixed time for deterministic tests.
func fixedNow(s string) func() time.Time {
	t, _ := time.Parse(time.RFC3339, s)
	return func() time.Time { return t }
}

// Scenario: Filter jobs since a duration using hours
func TestFilterJobsSinceADurationUsingHours(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	jobs := buildDataset(t, root)
	now := fixedNow("2026-02-27T16:00:00+03:00")

	since, err := ParseSinceFilter("2h", now)
	if err != nil {
		t.Fatalf("ParseSinceFilter: %v", err)
	}
	result := FilterJobs(jobs, &FilterOptions{Since: since})

	if len(result) != 3 {
		t.Fatalf("expected 3 jobs since 2h, got %d", len(result))
	}
	ids := jobIDs(result)
	assertContains(t, ids, "job-20260227-153000-aa11bb22")
	assertContains(t, ids, "job-20260227-151500-cc33dd44")
	assertContains(t, ids, "job-20260227-144500-ee55ff66")
}

// Scenario: Filter jobs since a duration using minutes
func TestFilterJobsSinceADurationUsingMinutes(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	jobs := buildDataset(t, root)
	now := fixedNow("2026-02-27T16:00:00+03:00")

	since, err := ParseSinceFilter("30m", now)
	if err != nil {
		t.Fatalf("ParseSinceFilter: %v", err)
	}
	result := FilterJobs(jobs, &FilterOptions{Since: since})

	if len(result) != 1 {
		t.Fatalf("expected 1 job since 30m, got %d", len(result))
	}
	assertContains(t, jobIDs(result), "job-20260227-153000-aa11bb22")
}

// Scenario: Filter jobs since a duration using days
func TestFilterJobsSinceADurationUsingDays(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	jobs := buildDataset(t, root)
	now := fixedNow("2026-02-27T16:00:00+03:00")

	since, err := ParseSinceFilter("7d", now)
	if err != nil {
		t.Fatalf("ParseSinceFilter: %v", err)
	}
	result := FilterJobs(jobs, &FilterOptions{Since: since})

	if len(result) != 8 {
		t.Fatalf("expected 8 jobs since 7d, got %d", len(result))
	}
}

// Scenario: Filter jobs since an ISO date
func TestFilterJobsSinceAnISODate(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	jobs := buildDataset(t, root)
	now := func() time.Time { return time.Now() }

	since, err := ParseSinceFilter("2026-02-27", now)
	if err != nil {
		t.Fatalf("ParseSinceFilter: %v", err)
	}
	result := FilterJobs(jobs, &FilterOptions{Since: since})

	// All 8 jobs were created on 2026-02-27, so all should match.
	if len(result) != 8 {
		t.Fatalf("expected 8 jobs since 2026-02-27, got %d", len(result))
	}
}

// Scenario: Filter with since value in the future returns empty list
func TestFilterWithSinceValueInFutureReturnsEmptyList(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	jobs := buildDataset(t, root)
	now := fixedNow("2026-02-27T16:00:00+03:00")

	since, err := ParseSinceFilter("2026-03-01", now)
	if err != nil {
		t.Fatalf("ParseSinceFilter: %v", err)
	}
	result := FilterJobs(jobs, &FilterOptions{Since: since})

	if len(result) != 0 {
		t.Errorf("expected 0 jobs for future date, got %d", len(result))
	}
}

// =============================================================================
// AC4: Filters combine with AND logic
// =============================================================================

// Scenario: Combine status and project filters with AND logic
func TestCombineStatusAndProjectFiltersWithAndLogic(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	jobs := buildDataset(t, root)

	statuses, err := ParseStatusFilter("running")
	if err != nil {
		t.Fatalf("ParseStatusFilter: %v", err)
	}
	result := FilterJobs(jobs, &FilterOptions{
		Statuses:      statuses,
		ProjectPrefix: "my-app",
	})

	if len(result) != 1 {
		t.Fatalf("expected 1 job, got %d", len(result))
	}
	assertContains(t, jobIDs(result), "job-20260227-153000-aa11bb22")
}

// Scenario: Combine status and project filters that match no jobs
func TestCombineStatusAndProjectFiltersThatMatchNoJobs(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	jobs := buildDataset(t, root)

	statuses, err := ParseStatusFilter("queued")
	if err != nil {
		t.Fatalf("ParseStatusFilter: %v", err)
	}
	result := FilterJobs(jobs, &FilterOptions{
		Statuses:      statuses,
		ProjectPrefix: "api-server",
	})

	if len(result) != 0 {
		t.Errorf("expected 0 jobs, got %d", len(result))
	}
}

// Scenario: Combine all three filters
func TestCombineAllThreeFilters(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	jobs := buildDataset(t, root)
	now := fixedNow("2026-02-27T16:00:00+03:00")

	statuses, err := ParseStatusFilter("running")
	if err != nil {
		t.Fatalf("ParseStatusFilter: %v", err)
	}
	since, err := ParseSinceFilter("1h", now)
	if err != nil {
		t.Fatalf("ParseSinceFilter: %v", err)
	}
	result := FilterJobs(jobs, &FilterOptions{
		Statuses:      statuses,
		ProjectPrefix: "my-app",
		Since:         since,
	})

	if len(result) != 1 {
		t.Fatalf("expected 1 job, got %d", len(result))
	}
	assertContains(t, jobIDs(result), "job-20260227-153000-aa11bb22")
}

// Scenario: Combine status and since filters
func TestCombineStatusAndSinceFilters(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	jobs := buildDataset(t, root)
	now := fixedNow("2026-02-27T16:00:00+03:00")

	statuses, err := ParseStatusFilter("done")
	if err != nil {
		t.Fatalf("ParseStatusFilter: %v", err)
	}
	since, err := ParseSinceFilter("4h", now)
	if err != nil {
		t.Fatalf("ParseSinceFilter: %v", err)
	}
	result := FilterJobs(jobs, &FilterOptions{
		Statuses: statuses,
		Since:    since,
	})

	if len(result) != 1 {
		t.Fatalf("expected 1 job (done within 4h), got %d", len(result))
	}
	assertContains(t, jobIDs(result), "job-20260227-144500-ee55ff66")
}

// =============================================================================
// AC5: Works with JSON output mode
// =============================================================================

// Scenario: Filter with JSON output mode
func TestFilterWithJsonOutputMode(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	buildDataset(t, root)

	statuses, err := ParseStatusFilter("running")
	if err != nil {
		t.Fatalf("ParseStatusFilter: %v", err)
	}

	var buf bytes.Buffer
	if err := ListJSON(root, &FilterOptions{Statuses: statuses}, &buf); err != nil {
		t.Fatalf("ListJSON: %v", err)
	}

	out := strings.TrimSpace(buf.String())
	if !isValidJSON(out) {
		t.Fatalf("expected valid JSON, got: %q", out)
	}

	var arr []map[string]any
	if err := json.Unmarshal([]byte(out), &arr); err != nil {
		t.Fatalf("JSON unmarshal: %v", err)
	}
	if len(arr) != 2 {
		t.Fatalf("expected 2 elements, got %d", len(arr))
	}
	for _, item := range arr {
		if item["status"] != "running" {
			t.Errorf("expected status running, got %q", item["status"])
		}
	}
}

// Scenario: Filter returning empty set in JSON mode
func TestFilterReturningEmptySetInJsonMode(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	buildDataset(t, root)

	statuses, err := ParseStatusFilter("queued")
	if err != nil {
		t.Fatalf("ParseStatusFilter: %v", err)
	}

	var buf bytes.Buffer
	if err := ListJSON(root, &FilterOptions{
		Statuses:      statuses,
		ProjectPrefix: "api-server",
	}, &buf); err != nil {
		t.Fatalf("ListJSON: %v", err)
	}

	out := strings.TrimSpace(buf.String())
	if out != "[]" {
		t.Errorf("expected [], got: %q", out)
	}
}

// Scenario: Combined filters with JSON output
func TestCombinedFiltersWithJsonOutput(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	buildDataset(t, root)

	statuses, err := ParseStatusFilter("done,failed")
	if err != nil {
		t.Fatalf("ParseStatusFilter: %v", err)
	}

	var buf bytes.Buffer
	if err := ListJSON(root, &FilterOptions{
		Statuses:      statuses,
		ProjectPrefix: "my-app",
	}, &buf); err != nil {
		t.Fatalf("ListJSON: %v", err)
	}

	out := strings.TrimSpace(buf.String())
	if !isValidJSON(out) {
		t.Fatalf("expected valid JSON, got: %q", out)
	}
	var arr []map[string]any
	if err := json.Unmarshal([]byte(out), &arr); err != nil {
		t.Fatalf("JSON unmarshal: %v", err)
	}
	if len(arr) != 2 {
		t.Fatalf("expected 2 elements, got %d", len(arr))
	}
}

// =============================================================================
// AC6: Invalid filter values return err:user
// =============================================================================

// Scenario: Invalid status value returns error
func TestInvalidStatusValueReturnsError(t *testing.T) {
	t.Parallel()
	_, err := ParseStatusFilter("bogus")
	if err == nil {
		t.Fatal("expected error for invalid status, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "err:user") {
		t.Errorf("error must contain err:user, got: %q", msg)
	}
	if !strings.Contains(msg, "Unknown status: bogus") {
		t.Errorf("error must mention Unknown status: bogus, got: %q", msg)
	}
	// Must list valid statuses.
	for _, valid := range []string{"queued", "running", "done", "failed", "timeout", "killed", "permission_error"} {
		if !strings.Contains(msg, valid) {
			t.Errorf("error message missing valid status %q: %q", valid, msg)
		}
	}
}

// Scenario: Invalid status among valid statuses returns error
func TestInvalidStatusAmongValidStatusesReturnsError(t *testing.T) {
	t.Parallel()
	_, err := ParseStatusFilter("running,bogus")
	if err == nil {
		t.Fatal("expected error for partially invalid status list, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "err:user") {
		t.Errorf("error must contain err:user, got: %q", msg)
	}
	if !strings.Contains(msg, "Unknown status: bogus") {
		t.Errorf("error must mention Unknown status: bogus, got: %q", msg)
	}
}

// Scenario: Invalid since duration format returns error
func TestInvalidSinceDurationFormatReturnsError(t *testing.T) {
	t.Parallel()
	_, err := ParseSinceFilter("not-a-duration", func() time.Time { return time.Now() })
	if err == nil {
		t.Fatal("expected error for invalid duration, got nil")
	}
	if !strings.Contains(err.Error(), "err:user") {
		t.Errorf("error must contain err:user, got: %q", err.Error())
	}
}

// =============================================================================
// ParseDuration unit tests
// =============================================================================

func TestParseDurationHours(t *testing.T) {
	t.Parallel()
	d, err := ParseDuration("2h")
	if err != nil {
		t.Fatalf("ParseDuration(2h): %v", err)
	}
	if d != 2*time.Hour {
		t.Errorf("ParseDuration(2h): got %v, want %v", d, 2*time.Hour)
	}
}

func TestParseDurationMinutes(t *testing.T) {
	t.Parallel()
	d, err := ParseDuration("30m")
	if err != nil {
		t.Fatalf("ParseDuration(30m): %v", err)
	}
	if d != 30*time.Minute {
		t.Errorf("ParseDuration(30m): got %v, want %v", d, 30*time.Minute)
	}
}

func TestParseDurationDays(t *testing.T) {
	t.Parallel()
	d, err := ParseDuration("7d")
	if err != nil {
		t.Fatalf("ParseDuration(7d): %v", err)
	}
	want := 7 * 24 * time.Hour
	if d != want {
		t.Errorf("ParseDuration(7d): got %v, want %v", d, want)
	}
}

func TestParseDurationInvalid(t *testing.T) {
	t.Parallel()
	_, err := ParseDuration("not-a-duration")
	if err == nil {
		t.Error("expected error for invalid duration, got nil")
	}
}

// =============================================================================
// Helpers
// =============================================================================

func jobIDs(jobs []JobEntry) []string {
	ids := make([]string, len(jobs))
	for i, j := range jobs {
		ids[i] = j.JobID
	}
	return ids
}

func assertContains(t *testing.T, ids []string, want string) {
	t.Helper()
	for _, id := range ids {
		if id == want {
			return
		}
	}
	t.Errorf("expected job %q in result set %v", want, ids)
}
