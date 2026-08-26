package farm

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRunLoopProducesRawReportsAndIntelligence(t *testing.T) {
	directory := t.TempDir()
	inventoryPath := filepath.Join(directory, "inventory.json")
	inventory := Inventory{Version: 1, Devices: []Device{{
		ID: "local", Name: "Local", OS: "test", Arch: "test", Transport: "local", Labels: []string{"loop"},
	}}}
	data, err := json.Marshal(inventory)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(inventoryPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	runs := filepath.Join(directory, "runs")
	intelligencePath := filepath.Join(runs, "intelligence.json")
	completed := 0
	err = RunLoop(context.Background(), LoopConfig{
		InventoryPath: inventoryPath, RunsDirectory: runs, IntelligencePath: intelligencePath,
		Job:      Job{APIVersion: "farm/v1", Name: "loop", Kind: "probe", Selector: Selector{AllLabels: []string{"loop"}}, Strategy: Strategy{MaxParallel: 1}},
		Interval: time.Millisecond, MaxIterations: 2,
		OnIteration: func(_ int, _ RunReport, _ Intelligence, _ ScreenshotBatch, _ string) { completed++ },
	})
	if err != nil {
		t.Fatal(err)
	}
	if completed != 2 {
		t.Fatalf("expected 2 completed iterations, got %d", completed)
	}
	intelligence, err := LoadIntelligence(intelligencePath)
	if err != nil {
		t.Fatal(err)
	}
	if intelligence.TotalRuns != 2 || intelligence.TotalObservations != 2 {
		t.Fatalf("unexpected intelligence: %#v", intelligence)
	}
	reports, err := filepath.Glob(filepath.Join(runs, "run-*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(reports) != 2 {
		t.Fatalf("expected 2 raw reports, got %d", len(reports))
	}
}
