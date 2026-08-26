package farm

import (
	"errors"
	"testing"
)

func testInventory() Inventory {
	return Inventory{Version: 1, Devices: []Device{
		{ID: "cuda", Name: "CUDA", OS: "linux", Arch: "amd64", Transport: "ssh", Labels: []string{"test-worker", "cuda", "gpu"}},
		{ID: "mac", Name: "Mac", OS: "macos", Arch: "arm64", Transport: "local", Labels: []string{"test-worker", "macos"}},
		{ID: "pve", Name: "Proxmox", OS: "proxmox", Arch: "amd64", Transport: "https", Labels: []string{"infrastructure"}},
	}}
}

func TestBuildPlanSelectsByLabelAndOS(t *testing.T) {
	job := Job{
		APIVersion: "farm/v1",
		Name:       "cuda-smoke",
		Kind:       "probe",
		Selector: Selector{
			AllLabels:        []string{"test-worker", "GPU"},
			OperatingSystems: []string{"LINUX"},
		},
		Strategy: Strategy{MaxParallel: 2},
	}
	plan, err := BuildPlan(testInventory(), job)
	if err != nil {
		t.Fatal(err)
	}
	if plan.SelectedCount != 1 || plan.Shards[0].DeviceID != "cuda" {
		t.Fatalf("unexpected plan: %#v", plan)
	}
}

func TestTestJobIsBlockedWithoutAgent(t *testing.T) {
	job := Job{
		APIVersion: "farm/v1",
		Name:       "tests",
		Kind:       "test",
		Selector:   Selector{AllLabels: []string{"test-worker"}},
		Execution:  Execution{Isolation: "required", Workspace: "ephemeral"},
		Steps:      []Step{{Name: "test", Command: []string{"go", "test", "./..."}, TimeoutSeconds: 60}},
	}
	plan, err := BuildPlan(testInventory(), job)
	if err != nil {
		t.Fatal(err)
	}
	if plan.RunnableCount != 0 {
		t.Fatalf("unregistered agents must not be runnable: %#v", plan)
	}
	_, err = RunJob(t.Context(), testInventory(), job)
	if !errors.Is(err, ErrAgentRequired) {
		t.Fatalf("expected ErrAgentRequired, got %v", err)
	}
}

func TestTestJobRequiresIsolation(t *testing.T) {
	job := Job{APIVersion: "farm/v1", Name: "unsafe", Kind: "test", Steps: []Step{{Name: "test", Command: []string{"true"}, TimeoutSeconds: 10}}}
	if err := ValidateJob(job); err == nil {
		t.Fatal("expected an isolation validation error")
	}
}
