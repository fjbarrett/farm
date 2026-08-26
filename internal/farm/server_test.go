package farm

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRequireLoopback(t *testing.T) {
	for _, address := range []string{"127.0.0.1:7331", "[::1]:7331", "localhost:7331"} {
		if err := requireLoopback(address); err != nil {
			t.Fatalf("expected %s to be allowed: %v", address, err)
		}
	}
	if err := requireLoopback("0.0.0.0:7331"); err == nil {
		t.Fatal("public binding must be rejected until authentication exists")
	}
}

func TestControllerScreenshotReviewAPI(t *testing.T) {
	directory := t.TempDir()
	screenshots := filepath.Join(directory, "screenshots")
	if err := os.MkdirAll(screenshots, 0o750); err != nil {
		t.Fatal(err)
	}
	intelligencePath := filepath.Join(directory, "intelligence.json")
	now := time.Date(2026, 8, 26, 14, 0, 0, 0, time.UTC)
	report := RunReport{
		ID: "run-api", StartedAt: now, FinishedAt: now, Status: "passed",
		Results: []ProbeResult{{DeviceID: "browser", DeviceName: "Browser", Status: "passed"}},
	}
	if _, err := UpdateIntelligence(intelligencePath, report); err != nil {
		t.Fatal(err)
	}
	artifact := ScreenshotArtifact{
		RunID: "run-api", DeviceID: "browser", DeviceName: "Browser", CapturedAt: now, Status: "captured",
		Path: filepath.Join(screenshots, "run-api", "browser.png"), SemanticReviewStatus: "pending",
	}
	if err := appendScreenshotQueue(filepath.Join(screenshots, "review-queue.jsonl"), []ScreenshotArtifact{artifact}); err != nil {
		t.Fatal(err)
	}
	controller := Controller{ScreenshotsDirectory: screenshots, IntelligencePath: intelligencePath}

	pendingResponse := httptest.NewRecorder()
	controller.Handler().ServeHTTP(pendingResponse, httptest.NewRequest(http.MethodGet, "/v1/reviews/pending", nil))
	if pendingResponse.Code != http.StatusOK {
		t.Fatalf("pending reviews returned %d: %s", pendingResponse.Code, pendingResponse.Body.String())
	}
	var pending []ScreenshotArtifact
	if err := json.Unmarshal(pendingResponse.Body.Bytes(), &pending); err != nil || len(pending) != 1 {
		t.Fatalf("unexpected pending response: %s, error=%v", pendingResponse.Body.String(), err)
	}

	payload := []byte(`{"runId":"run-api","deviceId":"browser","verdict":"failed","summary":"page shows a fatal error","reviewer":"test"}`)
	submitResponse := httptest.NewRecorder()
	controller.Handler().ServeHTTP(submitResponse, httptest.NewRequest(http.MethodPost, "/v1/reviews", bytes.NewReader(payload)))
	if submitResponse.Code != http.StatusCreated {
		t.Fatalf("submit review returned %d: %s", submitResponse.Code, submitResponse.Body.String())
	}

	retryResponse := httptest.NewRecorder()
	controller.Handler().ServeHTTP(retryResponse, httptest.NewRequest(http.MethodPost, "/v1/reviews", bytes.NewReader(payload)))
	if retryResponse.Code != http.StatusOK {
		t.Fatalf("idempotent retry returned %d: %s", retryResponse.Code, retryResponse.Body.String())
	}

	pendingResponse = httptest.NewRecorder()
	controller.Handler().ServeHTTP(pendingResponse, httptest.NewRequest(http.MethodGet, "/v1/reviews/pending", nil))
	if pendingResponse.Code != http.StatusOK || pendingResponse.Body.String() != "[]\n" {
		t.Fatalf("reviewed screenshot remained pending: %d %s", pendingResponse.Code, pendingResponse.Body.String())
	}
	historyResponse := httptest.NewRecorder()
	controller.Handler().ServeHTTP(historyResponse, httptest.NewRequest(http.MethodGet, "/v1/reviews", nil))
	if historyResponse.Code != http.StatusOK {
		t.Fatalf("review history returned %d: %s", historyResponse.Code, historyResponse.Body.String())
	}
	var history []ScreenshotReview
	if err := json.Unmarshal(historyResponse.Body.Bytes(), &history); err != nil || len(history) != 1 {
		t.Fatalf("unexpected history response: %s, error=%v", historyResponse.Body.String(), err)
	}

	invalidResponse := httptest.NewRecorder()
	invalid := bytes.NewBufferString(`{"runId":"run-api","deviceId":"browser","verdict":"failed","summary":"x","unexpected":true}`)
	controller.Handler().ServeHTTP(invalidResponse, httptest.NewRequest(http.MethodPost, "/v1/reviews", invalid))
	if invalidResponse.Code != http.StatusBadRequest {
		t.Fatalf("unknown JSON field returned %d: %s", invalidResponse.Code, invalidResponse.Body.String())
	}
}
