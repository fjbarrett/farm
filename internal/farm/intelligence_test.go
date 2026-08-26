package farm

import (
	"math"
	"path/filepath"
	"testing"
	"time"
)

func TestUpdateIntelligenceBuildsTrendsAndFindings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "intelligence.json")
	started := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	first := RunReport{
		ID: "run-1", StartedAt: started, FinishedAt: started.Add(time.Second), Status: "passed",
		Results: []ProbeResult{{DeviceID: "worker", DeviceName: "Worker", Transport: "ssh", Status: "passed", DurationMS: 20, Output: "hostname=one\nos=1"}},
	}
	intelligence, err := UpdateIntelligence(path, first)
	if err != nil {
		t.Fatal(err)
	}
	if intelligence.TotalRuns != 1 || intelligence.TotalObservations != 1 || len(intelligence.RecentFindings) != 1 {
		t.Fatalf("unexpected first intelligence: %#v", intelligence)
	}

	second := RunReport{
		ID: "run-2", StartedAt: started.Add(time.Minute), FinishedAt: started.Add(time.Minute + time.Second), Status: "failed",
		Results: []ProbeResult{{DeviceID: "worker", DeviceName: "Worker", Transport: "ssh", Status: "failed", DurationMS: 40, Output: "hostname=one\nos=2", Error: "offline"}},
	}
	intelligence, err = UpdateIntelligence(path, second)
	if err != nil {
		t.Fatal(err)
	}
	device := intelligence.Devices[0]
	if intelligence.TotalRuns != 2 || intelligence.PassedRuns != 1 || intelligence.FailedRuns != 1 {
		t.Fatalf("unexpected run totals: %#v", intelligence)
	}
	if device.Observations != 2 || device.ConsecutiveFailures != 1 || device.StatusTransitions != 1 {
		t.Fatalf("unexpected device totals: %#v", device)
	}
	if math.Abs(device.Availability-0.5) > 0.0001 || math.Abs(device.AverageDurationMS-30) > 0.0001 {
		t.Fatalf("unexpected aggregates: %#v", device)
	}
	if device.Attributes["os"] != "2" || device.LastError != "offline" {
		t.Fatalf("latest intelligence was not retained: %#v", device)
	}
	if len(intelligence.RecentFindings) != 3 {
		t.Fatalf("expected discovery, status, and attribute findings: %#v", intelligence.RecentFindings)
	}
}

func TestParseAttributesIgnoresNonDataLines(t *testing.T) {
	attributes := parseAttributes("hostname=worker\nnoise\n =empty-key\ntitle=a=b")
	if len(attributes) != 2 || attributes["hostname"] != "worker" || attributes["title"] != "a=b" {
		t.Fatalf("unexpected attributes: %#v", attributes)
	}
}

func TestUpdateScreenshotIntelligenceAddsVisualFindings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "intelligence.json")
	now := time.Date(2026, 8, 25, 13, 0, 0, 0, time.UTC)
	report := RunReport{
		ID: "visual-run", StartedAt: now, FinishedAt: now, Status: "passed",
		Results: []ProbeResult{{DeviceID: "ui", DeviceName: "UI", Status: "passed"}},
	}
	if _, err := UpdateIntelligence(path, report); err != nil {
		t.Fatal(err)
	}
	batch := ScreenshotBatch{RunID: report.ID, CapturedAt: now.Add(time.Second), Artifacts: []ScreenshotArtifact{{
		RunID: report.ID, DeviceID: "ui", CapturedAt: now.Add(time.Second), Status: "captured", Path: "shot.png",
		LooksBlank: true, PossiblyFrozen: true, UnchangedFrames: frozenFrameThreshold, SemanticReviewStatus: "pending",
	}}}
	intelligence, err := UpdateScreenshotIntelligence(path, batch)
	if err != nil {
		t.Fatal(err)
	}
	device := intelligence.Devices[0]
	if device.ScreenshotCaptures != 1 || device.BlankFrames != 1 || device.PossiblyFrozenFrames != 1 || device.SemanticReviewsPending != 1 {
		t.Fatalf("unexpected screenshot intelligence: %#v", device)
	}
	if len(intelligence.RecentFindings) != 3 {
		t.Fatalf("expected discovery, blank, and frozen findings: %#v", intelligence.RecentFindings)
	}
}
