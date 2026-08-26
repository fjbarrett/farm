package farm

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const maxRecentFindings = 100

// UpdateIntelligence merges a raw report into a cumulative intelligence file.
// The update is atomic, so readers never observe a partially written document.
func UpdateIntelligence(path string, report RunReport) (Intelligence, error) {
	intelligence, err := LoadIntelligence(path)
	if err != nil {
		return Intelligence{}, err
	}
	mergeReport(&intelligence, report)
	if err := saveIntelligence(path, intelligence); err != nil {
		return Intelligence{}, err
	}
	return intelligence, nil
}

// UpdateScreenshotIntelligence connects visual artifacts to the same durable
// device history as probe results.
func UpdateScreenshotIntelligence(path string, batch ScreenshotBatch) (Intelligence, error) {
	intelligence, err := LoadIntelligence(path)
	if err != nil {
		return Intelligence{}, err
	}
	devices := make(map[string]*DeviceIntelligence, len(intelligence.Devices))
	for index := range intelligence.Devices {
		devices[intelligence.Devices[index].DeviceID] = &intelligence.Devices[index]
	}
	for _, artifact := range batch.Artifacts {
		device := devices[artifact.DeviceID]
		if device == nil {
			continue
		}
		previousStatus := device.LatestScreenshotStatus
		device.LatestScreenshotStatus = artifact.Status
		switch artifact.Status {
		case "captured", "analysis_failed":
			device.ScreenshotCaptures++
			device.LastScreenshotPath = artifact.Path
			device.LastScreenshotAt = artifact.CapturedAt
			if artifact.SemanticReviewStatus == "pending" {
				device.SemanticReviewsPending++
			}
			if artifact.LooksBlank {
				device.BlankFrames++
				addScreenshotFinding(&intelligence, batch, artifact, "blank_frame", "screenshot appears blank or nearly uniform")
			}
			if artifact.PossiblyFrozen {
				device.PossiblyFrozenFrames++
				if artifact.UnchangedFrames == frozenFrameThreshold {
					addScreenshotFinding(&intelligence, batch, artifact, "possibly_frozen", fmt.Sprintf("screen was unchanged for %d consecutive captures", artifact.UnchangedFrames))
				}
			}
			if previousStatus != "" && previousStatus != "captured" && previousStatus != "analysis_failed" {
				addScreenshotFinding(&intelligence, batch, artifact, "screenshot_capture_recovered", fmt.Sprintf("screenshot capture recovered after %s", previousStatus))
			}
		case "failed":
			device.ScreenshotFailures++
			if previousStatus != "failed" {
				addScreenshotFinding(&intelligence, batch, artifact, "screenshot_capture_failed", artifact.Error)
			}
		}
	}
	intelligence.GeneratedAt = batch.CapturedAt
	if err := saveIntelligence(path, intelligence); err != nil {
		return Intelligence{}, err
	}
	return intelligence, nil
}

func addScreenshotFinding(intelligence *Intelligence, batch ScreenshotBatch, artifact ScreenshotArtifact, kind, message string) {
	addFinding(intelligence, RunReport{ID: batch.RunID, FinishedAt: artifact.CapturedAt}, artifact.DeviceID, kind, message)
}

func LoadIntelligence(path string) (Intelligence, error) {
	intelligence := Intelligence{Version: 1}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return intelligence, nil
	}
	if err != nil {
		return Intelligence{}, fmt.Errorf("load intelligence: %w", err)
	}
	if err := json.Unmarshal(data, &intelligence); err != nil {
		return Intelligence{}, fmt.Errorf("decode intelligence: %w", err)
	}
	if intelligence.Version != 1 {
		return Intelligence{}, fmt.Errorf("unsupported intelligence version %d", intelligence.Version)
	}
	return intelligence, nil
}

func mergeReport(intelligence *Intelligence, report RunReport) {
	intelligence.Version = 1
	intelligence.GeneratedAt = report.FinishedAt
	intelligence.LastRunAt = report.FinishedAt
	intelligence.LastRunID = report.ID
	intelligence.TotalRuns++
	if intelligence.FirstRunAt.IsZero() {
		intelligence.FirstRunAt = report.StartedAt
	}
	if report.Status == "passed" {
		intelligence.PassedRuns++
	} else {
		intelligence.FailedRuns++
	}

	devices := make(map[string]*DeviceIntelligence, len(intelligence.Devices))
	for index := range intelligence.Devices {
		devices[intelligence.Devices[index].DeviceID] = &intelligence.Devices[index]
	}
	for _, result := range report.Results {
		device := devices[result.DeviceID]
		if device == nil {
			intelligence.Devices = append(intelligence.Devices, DeviceIntelligence{
				DeviceID:    result.DeviceID,
				DeviceName:  result.DeviceName,
				Transport:   result.Transport,
				FirstSeenAt: report.FinishedAt,
				Attributes:  make(map[string]string),
			})
			device = &intelligence.Devices[len(intelligence.Devices)-1]
			devices[result.DeviceID] = device
			addFinding(intelligence, report, result.DeviceID, "device_discovered", "first observation recorded")
		}

		previousStatus := device.LatestStatus
		if previousStatus != "" && previousStatus != result.Status {
			kind := "status_changed"
			if result.Status == "passed" {
				kind = "recovered"
			}
			addFinding(intelligence, report, result.DeviceID, kind, fmt.Sprintf("status changed from %s to %s", previousStatus, result.Status))
			device.StatusTransitions++
		}

		attributes := parseAttributes(result.Output)
		for key, value := range attributes {
			if previous, exists := device.Attributes[key]; exists && previous != value {
				addFinding(intelligence, report, result.DeviceID, "attribute_changed", fmt.Sprintf("%s changed from %q to %q", key, previous, value))
			}
			device.Attributes[key] = value
		}

		device.DeviceName = result.DeviceName
		device.Transport = result.Transport
		device.LastSeenAt = report.FinishedAt
		device.Observations++
		intelligence.TotalObservations++
		device.LatestStatus = result.Status
		if result.Status == "passed" {
			device.Passed++
			device.ConsecutiveFailures = 0
			device.LastError = ""
		} else {
			device.Failed++
			device.ConsecutiveFailures++
			device.LastError = result.Error
		}
		device.Availability = float64(device.Passed) / float64(device.Observations)
		device.LatestDurationMS = result.DurationMS
		device.TotalDurationMS += result.DurationMS
		device.AverageDurationMS = float64(device.TotalDurationMS) / float64(device.Observations)
		if device.Observations == 1 || result.DurationMS < device.MinDurationMS {
			device.MinDurationMS = result.DurationMS
		}
		if result.DurationMS > device.MaxDurationMS {
			device.MaxDurationMS = result.DurationMS
		}
	}
	sort.Slice(intelligence.Devices, func(left, right int) bool {
		return intelligence.Devices[left].DeviceID < intelligence.Devices[right].DeviceID
	})
}

func parseAttributes(output string) map[string]string {
	attributes := make(map[string]string)
	for _, line := range strings.Split(output, "\n") {
		key, value, found := strings.Cut(strings.TrimSpace(line), "=")
		if !found || strings.TrimSpace(key) == "" {
			continue
		}
		attributes[strings.TrimSpace(key)] = strings.TrimSpace(value)
	}
	return attributes
}

func addFinding(intelligence *Intelligence, report RunReport, deviceID, kind, message string) {
	intelligence.RecentFindings = append(intelligence.RecentFindings, Finding{
		RunID: report.ID, ObservedAt: report.FinishedAt, DeviceID: deviceID, Kind: kind, Message: message,
	})
	if excess := len(intelligence.RecentFindings) - maxRecentFindings; excess > 0 {
		intelligence.RecentFindings = append([]Finding(nil), intelligence.RecentFindings[excess:]...)
	}
}

func saveIntelligence(path string, intelligence Intelligence) error {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o750); err != nil {
		return err
	}
	data, err := json.MarshalIndent(intelligence, "", "  ")
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(directory, ".intelligence-*.tmp")
	if err != nil {
		return err
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	if _, err := temporary.Write(append(data, '\n')); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryName, path); err != nil {
		return err
	}
	return nil
}
