package farm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestScreenshotReviewLifecycleIsPendingAndIdempotent(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	artifacts := []ScreenshotArtifact{
		{RunID: "run-1", DeviceID: "alpha", DeviceName: "Alpha", CapturedAt: now, Status: "captured", Path: filepath.Join(root, "run-1", "alpha.png"), SemanticReviewStatus: "pending"},
		{RunID: "run-1", DeviceID: "beta", DeviceName: "Beta", CapturedAt: now.Add(time.Second), Status: "captured", Path: filepath.Join(root, "run-1", "beta.png"), SemanticReviewStatus: "pending"},
	}
	if err := appendScreenshotQueue(filepath.Join(root, "review-queue.jsonl"), artifacts); err != nil {
		t.Fatal(err)
	}

	pending, err := LoadPendingScreenshotReviews(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 2 || pending[0].DeviceID != "alpha" || pending[1].DeviceID != "beta" {
		t.Fatalf("unexpected pending reviews: %#v", pending)
	}

	submission := ScreenshotReviewSubmission{
		RunID: "run-1", DeviceID: "alpha", Verdict: ScreenshotVerdictHealthy, Summary: "application is visible and responsive",
	}
	review, created, err := RecordScreenshotReview(root, submission)
	if err != nil {
		t.Fatal(err)
	}
	if !created || review.ID == "" || review.Path != artifacts[0].Path || review.Reviewer != "operator" || review.ReviewedAt.IsZero() {
		t.Fatalf("unexpected review: %#v", review)
	}

	repeated, created, err := RecordScreenshotReview(root, submission)
	if err != nil {
		t.Fatal(err)
	}
	if created || repeated.ID != review.ID {
		t.Fatalf("exact retry should be idempotent: %#v, created=%t", repeated, created)
	}
	pending, err = LoadPendingScreenshotReviews(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].DeviceID != "beta" {
		t.Fatalf("reviewed screenshot remained pending: %#v", pending)
	}
	history, err := LoadScreenshotReviews(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 1 || history[0].ID != review.ID {
		t.Fatalf("unexpected review history: %#v", history)
	}
	data, err := os.ReadFile(filepath.Join(root, "reviews.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if lines := strings.Count(strings.TrimSpace(string(data)), "\n") + 1; lines != 1 {
		t.Fatalf("idempotent retry appended %d ledger lines", lines)
	}

	submission.Verdict = ScreenshotVerdictFailed
	if _, _, err := RecordScreenshotReview(root, submission); err == nil {
		t.Fatal("changing an existing review should be rejected")
	}
	if _, _, err := RecordScreenshotReview(root, ScreenshotReviewSubmission{
		RunID: "run-1", DeviceID: "beta", Verdict: "excellent", Summary: "invalid verdict",
	}); err == nil {
		t.Fatal("unsupported verdict should be rejected")
	}
}

func TestReconcileScreenshotReviewIntelligenceTracksIssuesAndRecovery(t *testing.T) {
	directory := t.TempDir()
	root := filepath.Join(directory, "screenshots")
	if err := os.MkdirAll(root, 0o750); err != nil {
		t.Fatal(err)
	}
	intelligencePath := filepath.Join(directory, "intelligence.json")
	now := time.Date(2026, 8, 26, 13, 0, 0, 0, time.UTC)
	report := RunReport{
		ID: "run-1", StartedAt: now, FinishedAt: now, Status: "passed",
		Results: []ProbeResult{{DeviceID: "ui", DeviceName: "UI", Status: "passed"}},
	}
	if _, err := UpdateIntelligence(intelligencePath, report); err != nil {
		t.Fatal(err)
	}
	firstArtifact := ScreenshotArtifact{
		RunID: "run-1", DeviceID: "ui", DeviceName: "UI", CapturedAt: now, Status: "captured",
		Path: filepath.Join(root, "run-1", "ui.png"), SemanticReviewStatus: "pending",
	}
	if err := appendScreenshotQueue(filepath.Join(root, "review-queue.jsonl"), []ScreenshotArtifact{firstArtifact}); err != nil {
		t.Fatal(err)
	}
	if _, err := UpdateScreenshotIntelligence(intelligencePath, ScreenshotBatch{RunID: "run-1", CapturedAt: now, Artifacts: []ScreenshotArtifact{firstArtifact}}); err != nil {
		t.Fatal(err)
	}
	degraded, _, err := RecordScreenshotReview(root, ScreenshotReviewSubmission{
		RunID: "run-1", DeviceID: "ui", Verdict: ScreenshotVerdictDegraded, Summary: "error dialog obscures the application", Reviewer: "vision-agent",
	})
	if err != nil {
		t.Fatal(err)
	}
	intelligence, err := ReconcileScreenshotReviewIntelligence(intelligencePath, root, degraded)
	if err != nil {
		t.Fatal(err)
	}
	device := intelligence.Devices[0]
	if device.SemanticReviewsPending != 0 || device.SemanticReviewsCompleted != 1 || device.SemanticReviewIssues != 1 || device.LatestSemanticVerdict != ScreenshotVerdictDegraded {
		t.Fatalf("unexpected degraded intelligence: %#v", device)
	}
	findingsAfterDegraded := len(intelligence.RecentFindings)
	intelligence, err = ReconcileScreenshotReviewIntelligence(intelligencePath, root, degraded)
	if err != nil {
		t.Fatal(err)
	}
	if len(intelligence.RecentFindings) != findingsAfterDegraded {
		t.Fatal("reconciling the same review duplicated its finding")
	}

	secondArtifact := ScreenshotArtifact{
		RunID: "run-2", DeviceID: "ui", DeviceName: "UI", CapturedAt: now.Add(time.Minute), Status: "captured",
		Path: filepath.Join(root, "run-2", "ui.png"), SemanticReviewStatus: "pending",
	}
	if err := appendScreenshotQueue(filepath.Join(root, "review-queue.jsonl"), []ScreenshotArtifact{secondArtifact}); err != nil {
		t.Fatal(err)
	}
	healthy, _, err := RecordScreenshotReview(root, ScreenshotReviewSubmission{
		RunID: "run-2", DeviceID: "ui", Verdict: ScreenshotVerdictHealthy, Summary: "application recovered and is interactive", Reviewer: "vision-agent",
	})
	if err != nil {
		t.Fatal(err)
	}
	intelligence, err = ReconcileScreenshotReviewIntelligence(intelligencePath, root, healthy)
	if err != nil {
		t.Fatal(err)
	}
	device = intelligence.Devices[0]
	if device.SemanticReviewsPending != 0 || device.SemanticReviewsCompleted != 2 || device.SemanticReviewIssues != 1 || device.LatestSemanticVerdict != ScreenshotVerdictHealthy {
		t.Fatalf("unexpected recovered intelligence: %#v", device)
	}
	latest := intelligence.RecentFindings[len(intelligence.RecentFindings)-1]
	if latest.Kind != "semantic_review_recovered" || latest.RunID != "run-2" {
		t.Fatalf("expected semantic recovery finding, got %#v", latest)
	}
}
