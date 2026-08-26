package farm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"time"
)

type Controller struct {
	InventoryPath        string
	RunsDirectory        string
	IntelligencePath     string
	ScreenshotsDirectory string
}

func (controller Controller) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", controller.handleHealth)
	mux.HandleFunc("GET /v1/devices", controller.handleDevices)
	mux.HandleFunc("POST /v1/plan", controller.handlePlan)
	mux.HandleFunc("POST /v1/runs", controller.handleRun)
	mux.HandleFunc("GET /v1/reviews/pending", controller.handlePendingReviews)
	mux.HandleFunc("GET /v1/reviews", controller.handleReviewHistory)
	mux.HandleFunc("POST /v1/reviews", controller.handleSubmitReview)
	return requestLogging(mux)
}

func (controller Controller) handlePendingReviews(response http.ResponseWriter, _ *http.Request) {
	pending, err := LoadPendingScreenshotReviews(controller.screenshotsDirectory())
	if err != nil {
		writeError(response, http.StatusInternalServerError, err)
		return
	}
	writeJSON(response, http.StatusOK, pending)
}

func (controller Controller) handleReviewHistory(response http.ResponseWriter, _ *http.Request) {
	reviews, err := LoadScreenshotReviews(controller.screenshotsDirectory())
	if err != nil {
		writeError(response, http.StatusInternalServerError, err)
		return
	}
	writeJSON(response, http.StatusOK, reviews)
}

func (controller Controller) handleSubmitReview(response http.ResponseWriter, request *http.Request) {
	request.Body = http.MaxBytesReader(response, request.Body, maximumReviewLineBytes)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	var submission ScreenshotReviewSubmission
	if err := decoder.Decode(&submission); err != nil {
		writeError(response, http.StatusBadRequest, fmt.Errorf("decode screenshot review: %w", err))
		return
	}
	if err := requireJSONEnd(decoder); err != nil {
		writeError(response, http.StatusBadRequest, err)
		return
	}
	review, created, err := RecordScreenshotReview(controller.screenshotsDirectory(), submission)
	if err != nil {
		writeError(response, http.StatusUnprocessableEntity, err)
		return
	}
	if _, err := ReconcileScreenshotReviewIntelligence(controller.intelligencePath(), controller.screenshotsDirectory(), review); err != nil {
		writeError(response, http.StatusInternalServerError, err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	writeJSON(response, status, review)
}

func (controller Controller) screenshotsDirectory() string {
	if controller.ScreenshotsDirectory != "" {
		return controller.ScreenshotsDirectory
	}
	return filepath.Join(controller.RunsDirectory, "screenshots")
}

func (controller Controller) intelligencePath() string {
	if controller.IntelligencePath != "" {
		return controller.IntelligencePath
	}
	return filepath.Join(controller.RunsDirectory, "intelligence.json")
}

func requireJSONEnd(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); errors.Is(err, io.EOF) {
		return nil
	} else if err != nil {
		return fmt.Errorf("decode trailing JSON: %w", err)
	}
	return errors.New("request body must contain exactly one JSON value")
}

func (controller Controller) ListenAndServe(ctx context.Context, address string) error {
	if err := requireLoopback(address); err != nil {
		return err
	}
	server := &http.Server{
		Addr:              address,
		Handler:           controller.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()
	err := server.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func requireLoopback(address string) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("invalid listen address: %w", err)
	}
	if strings.EqualFold(host, "localhost") {
		return nil
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return errors.New("controller may only bind to loopback until API authentication is configured")
	}
	return nil
}

func (controller Controller) handleHealth(response http.ResponseWriter, _ *http.Request) {
	writeJSON(response, http.StatusOK, map[string]any{"status": "ok", "version": Version})
}

func (controller Controller) handleDevices(response http.ResponseWriter, _ *http.Request) {
	inventory, err := LoadInventory(controller.InventoryPath)
	if err != nil {
		writeError(response, http.StatusInternalServerError, err)
		return
	}
	writeJSON(response, http.StatusOK, inventory)
}

func (controller Controller) handlePlan(response http.ResponseWriter, request *http.Request) {
	job, inventory, ok := controller.decodeRequest(response, request)
	if !ok {
		return
	}
	plan, err := BuildPlan(inventory, job)
	if err != nil {
		writeError(response, http.StatusUnprocessableEntity, err)
		return
	}
	writeJSON(response, http.StatusOK, plan)
}

func (controller Controller) handleRun(response http.ResponseWriter, request *http.Request) {
	job, inventory, ok := controller.decodeRequest(response, request)
	if !ok {
		return
	}
	report, err := RunJob(request.Context(), inventory, job)
	if err != nil {
		status := http.StatusUnprocessableEntity
		if errors.Is(err, ErrAgentRequired) {
			status = http.StatusNotImplemented
		}
		writeError(response, status, err)
		return
	}
	if _, err := SaveRunReport(controller.RunsDirectory, report); err != nil {
		writeError(response, http.StatusInternalServerError, err)
		return
	}
	writeJSON(response, http.StatusCreated, report)
}

func (controller Controller) decodeRequest(response http.ResponseWriter, request *http.Request) (Job, Inventory, bool) {
	request.Body = http.MaxBytesReader(response, request.Body, 1<<20)
	body, err := io.ReadAll(request.Body)
	if err != nil {
		writeError(response, http.StatusBadRequest, errors.New("request body is invalid or exceeds 1 MiB"))
		return Job{}, Inventory{}, false
	}
	job, err := DecodeJob(body)
	if err != nil {
		writeError(response, http.StatusBadRequest, err)
		return Job{}, Inventory{}, false
	}
	inventory, err := LoadInventory(controller.InventoryPath)
	if err != nil {
		writeError(response, http.StatusInternalServerError, err)
		return Job{}, Inventory{}, false
	}
	return job, inventory, true
}

func writeJSON(response http.ResponseWriter, status int, value any) {
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(value)
}

func writeError(response http.ResponseWriter, status int, err error) {
	writeJSON(response, status, map[string]string{"error": err.Error()})
}

func requestLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(response, request)
	})
}
