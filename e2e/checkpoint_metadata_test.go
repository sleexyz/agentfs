package e2e

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// checkpointJSON represents the JSON output from checkpoint commands
type checkpointJSON struct {
	Version       string `json:"version"`
	Message       string `json:"message,omitempty"`
	CreatedAt     string `json:"created_at"`
	DurationMs    int64  `json:"duration_ms,omitempty"`
	ParentVersion *int   `json:"parent_version"`
}

// TestHelper provides utilities for e2e tests
type TestHelper struct {
	t          *testing.T
	projectDir string
	agentfsBin string
	tempDir    string
	storeDir   string
	mountDir   string
}

// NewTestHelper creates a new test helper
func NewTestHelper(t *testing.T) *TestHelper {
	t.Helper()

	// Get project directory (where go.mod is)
	projectDir, err := findProjectDir()
	if err != nil {
		t.Fatalf("failed to find project directory: %v", err)
	}

	// Build agentfs if needed
	agentfsBin := filepath.Join(projectDir, "agentfs")
	if _, err := os.Stat(agentfsBin); os.IsNotExist(err) {
		cmd := exec.Command("go", "build", "-o", agentfsBin, "./cmd/agentfs")
		cmd.Dir = projectDir
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("failed to build agentfs: %v\n%s", err, output)
		}
	}

	// Create temp directory for test
	tempDir, err := os.MkdirTemp("", "agentfs-e2e-*")
	if err != nil {
		t.Fatalf("failed to create temp directory: %v", err)
	}

	return &TestHelper{
		t:          t,
		projectDir: projectDir,
		agentfsBin: agentfsBin,
		tempDir:    tempDir,
	}
}

// findProjectDir finds the project directory by looking for go.mod
func findProjectDir() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}

	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", os.ErrNotExist
		}
		dir = parent
	}
}

// Cleanup removes temporary files and unmounts stores
func (h *TestHelper) Cleanup() {
	h.t.Helper()

	// Unmount if mounted
	if h.mountDir != "" {
		exec.Command("hdiutil", "detach", h.mountDir).Run()
	}

	// Remove temp directory
	if h.tempDir != "" {
		os.RemoveAll(h.tempDir)
	}
}

// CreateStore creates a new store for testing
func (h *TestHelper) CreateStore(name string) {
	h.t.Helper()

	h.mountDir = filepath.Join(h.tempDir, name)
	h.storeDir = h.mountDir + ".fs"

	// Create directory to manage
	if err := os.MkdirAll(h.mountDir, 0755); err != nil {
		h.t.Fatalf("failed to create mount directory: %v", err)
	}

	// Create a test file
	testFile := filepath.Join(h.mountDir, "test.txt")
	if err := os.WriteFile(testFile, []byte("test content"), 0644); err != nil {
		h.t.Fatalf("failed to create test file: %v", err)
	}

	// Run manage command
	output, err := h.RunAgentFS("manage", h.mountDir)
	if err != nil {
		h.t.Fatalf("failed to manage directory: %v\n%s", err, output)
	}
}

// RunAgentFS runs an agentfs command and returns the output
func (h *TestHelper) RunAgentFS(args ...string) (string, error) {
	h.t.Helper()

	cmd := exec.Command(h.agentfsBin, args...)
	cmd.Dir = h.tempDir
	output, err := cmd.CombinedOutput()
	return string(output), err
}

// RunAgentFSInStore runs an agentfs command from within the mounted store
func (h *TestHelper) RunAgentFSInStore(args ...string) (string, error) {
	h.t.Helper()

	cmd := exec.Command(h.agentfsBin, args...)
	cmd.Dir = h.mountDir
	output, err := cmd.CombinedOutput()
	return string(output), err
}

// CreateCheckpoint creates a checkpoint and returns its JSON output
func (h *TestHelper) CreateCheckpoint(message string) (*checkpointJSON, error) {
	h.t.Helper()

	args := []string{"checkpoint", "create", "--json"}
	if message != "" {
		args = append(args, message)
	}

	output, err := h.RunAgentFSInStore(args...)
	if err != nil {
		return nil, err
	}

	var cp checkpointJSON
	if err := json.Unmarshal([]byte(output), &cp); err != nil {
		return nil, err
	}

	return &cp, nil
}

// GetCheckpointInfo gets info for a specific checkpoint
func (h *TestHelper) GetCheckpointInfo(version string) (*checkpointJSON, error) {
	h.t.Helper()

	output, err := h.RunAgentFSInStore("checkpoint", "info", "--json", version)
	if err != nil {
		return nil, err
	}

	var cp checkpointJSON
	if err := json.Unmarshal([]byte(output), &cp); err != nil {
		return nil, err
	}

	return &cp, nil
}

// ListCheckpoints returns all checkpoints as JSON
func (h *TestHelper) ListCheckpoints() ([]checkpointJSON, error) {
	h.t.Helper()

	output, err := h.RunAgentFSInStore("checkpoint", "list", "--json")
	if err != nil {
		return nil, err
	}

	var checkpoints []checkpointJSON
	if err := json.Unmarshal([]byte(output), &checkpoints); err != nil {
		return nil, err
	}

	return checkpoints, nil
}

// RestoreCheckpoint restores to a specific version
func (h *TestHelper) RestoreCheckpoint(version string) error {
	h.t.Helper()

	// Need to respond 'y' to the confirmation prompt
	// Run from tempDir (not mountDir) to avoid "Resource busy" on unmount
	cmd := exec.Command(h.agentfsBin, "restore", "--store", h.storeDir, version)
	cmd.Dir = h.tempDir
	cmd.Stdin = strings.NewReader("y\n")
	output, err := cmd.CombinedOutput()
	if err != nil {
		h.t.Logf("restore output: %s", output)
		return err
	}
	return nil
}

// TestCheckpointDurationMs tests that duration_ms is recorded and persisted
func TestCheckpointDurationMs(t *testing.T) {
	h := NewTestHelper(t)
	defer h.Cleanup()

	h.CreateStore("test-duration")

	// Create a checkpoint
	cp, err := h.CreateCheckpoint("test duration")
	if err != nil {
		t.Fatalf("failed to create checkpoint: %v", err)
	}

	// Verify duration_ms is positive
	if cp.DurationMs <= 0 {
		t.Errorf("expected duration_ms > 0, got %d", cp.DurationMs)
	}

	// Verify duration_ms persists in checkpoint info
	info, err := h.GetCheckpointInfo(cp.Version)
	if err != nil {
		t.Fatalf("failed to get checkpoint info: %v", err)
	}

	if info.DurationMs != cp.DurationMs {
		t.Errorf("duration_ms not persisted: create=%d, info=%d", cp.DurationMs, info.DurationMs)
	}

	// Verify it appears in list
	checkpoints, err := h.ListCheckpoints()
	if err != nil {
		t.Fatalf("failed to list checkpoints: %v", err)
	}

	if len(checkpoints) == 0 {
		t.Fatal("no checkpoints found")
	}

	if checkpoints[0].DurationMs != cp.DurationMs {
		t.Errorf("duration_ms not in list: create=%d, list=%d", cp.DurationMs, checkpoints[0].DurationMs)
	}
}

// TestCheckpointParentVersion_FirstCheckpoint tests that the first checkpoint has null parent_version
func TestCheckpointParentVersion_FirstCheckpoint(t *testing.T) {
	h := NewTestHelper(t)
	defer h.Cleanup()

	h.CreateStore("test-first-parent")

	// Create first checkpoint
	cp, err := h.CreateCheckpoint("first checkpoint")
	if err != nil {
		t.Fatalf("failed to create checkpoint: %v", err)
	}

	// First checkpoint should have null parent_version
	if cp.ParentVersion != nil {
		t.Errorf("expected first checkpoint parent_version to be null, got %d", *cp.ParentVersion)
	}

	// Verify in info
	info, err := h.GetCheckpointInfo(cp.Version)
	if err != nil {
		t.Fatalf("failed to get checkpoint info: %v", err)
	}

	if info.ParentVersion != nil {
		t.Errorf("expected first checkpoint parent_version in info to be null, got %d", *info.ParentVersion)
	}
}

// TestCheckpointParentVersion_Sequential tests that sequential checkpoints have correct parent_version
func TestCheckpointParentVersion_Sequential(t *testing.T) {
	h := NewTestHelper(t)
	defer h.Cleanup()

	h.CreateStore("test-sequential")

	// Create first checkpoint (v1)
	cp1, err := h.CreateCheckpoint("v1")
	if err != nil {
		t.Fatalf("failed to create v1: %v", err)
	}
	if cp1.Version != "v1" {
		t.Fatalf("expected version v1, got %s", cp1.Version)
	}
	if cp1.ParentVersion != nil {
		t.Errorf("expected v1 parent_version to be null, got %d", *cp1.ParentVersion)
	}

	// Make a change (touch a file)
	testFile := filepath.Join(h.mountDir, "change.txt")
	if err := os.WriteFile(testFile, []byte("changed"), 0644); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}

	// Create second checkpoint (v2)
	cp2, err := h.CreateCheckpoint("v2")
	if err != nil {
		t.Fatalf("failed to create v2: %v", err)
	}
	if cp2.Version != "v2" {
		t.Fatalf("expected version v2, got %s", cp2.Version)
	}

	// v2 should have parent_version = 1
	if cp2.ParentVersion == nil {
		t.Error("expected v2 parent_version to be 1, got null")
	} else if *cp2.ParentVersion != 1 {
		t.Errorf("expected v2 parent_version to be 1, got %d", *cp2.ParentVersion)
	}
}

// TestCheckpointParentVersion_AfterRestore tests parent_version after restore (fork point)
func TestCheckpointParentVersion_AfterRestore(t *testing.T) {
	h := NewTestHelper(t)
	defer h.Cleanup()

	h.CreateStore("test-restore-fork")

	// Create v1
	cp1, err := h.CreateCheckpoint("v1")
	if err != nil {
		t.Fatalf("failed to create v1: %v", err)
	}
	if cp1.Version != "v1" {
		t.Fatalf("expected version v1, got %s", cp1.Version)
	}

	// Make a change
	testFile := filepath.Join(h.mountDir, "change1.txt")
	if err := os.WriteFile(testFile, []byte("change1"), 0644); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}

	// Create v2
	cp2, err := h.CreateCheckpoint("v2")
	if err != nil {
		t.Fatalf("failed to create v2: %v", err)
	}
	if cp2.Version != "v2" {
		t.Fatalf("expected version v2, got %s", cp2.Version)
	}

	// Restore to v1 (this creates a pre-restore checkpoint v3)
	if err := h.RestoreCheckpoint("v1"); err != nil {
		t.Fatalf("failed to restore to v1: %v", err)
	}

	// The restore creates a "pre-restore" checkpoint (v3) with parent_version = 1 (the target)
	// Let's verify v3 exists and has parent_version = 1
	checkpoints, err := h.ListCheckpoints()
	if err != nil {
		t.Fatalf("failed to list checkpoints: %v", err)
	}

	// Find v3 (pre-restore checkpoint)
	var v3 *checkpointJSON
	for i := range checkpoints {
		if checkpoints[i].Version == "v3" {
			v3 = &checkpoints[i]
			break
		}
	}

	if v3 == nil {
		t.Fatal("v3 (pre-restore checkpoint) not found")
	}

	// v3 should have parent_version = 1 (the fork point, not v2)
	if v3.ParentVersion == nil {
		t.Error("expected v3 parent_version to be 1, got null")
	} else if *v3.ParentVersion != 1 {
		t.Errorf("expected v3 parent_version to be 1 (fork point), got %d", *v3.ParentVersion)
	}

	// Now create v4 after the restore - it should have parent_version = 3
	testFile2 := filepath.Join(h.mountDir, "change2.txt")
	if err := os.WriteFile(testFile2, []byte("change2"), 0644); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}

	cp4, err := h.CreateCheckpoint("v4")
	if err != nil {
		t.Fatalf("failed to create v4: %v", err)
	}
	if cp4.Version != "v4" {
		t.Fatalf("expected version v4, got %s", cp4.Version)
	}

	// v4 should have parent_version = 3 (the latest checkpoint)
	if cp4.ParentVersion == nil {
		t.Error("expected v4 parent_version to be 3, got null")
	} else if *cp4.ParentVersion != 3 {
		t.Errorf("expected v4 parent_version to be 3, got %d", *cp4.ParentVersion)
	}
}
