package farm

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	screenshotTimeout       = 15 * time.Second
	frozenFrameThreshold    = 3
	unchangedPixelThreshold = 0.1
)

type screenshotState struct {
	Version int                              `json:"version"`
	Devices map[string]screenshotDeviceState `json:"devices"`
}

type screenshotDeviceState struct {
	Path            string `json:"path"`
	UnchangedFrames int    `json:"unchangedFrames"`
}

// CaptureScreenshots records one non-interactive screenshot for every device
// observed by a run. Unsupported or unavailable desktop sessions are retained
// in the manifest as capture failures instead of aborting the testing loop.
func CaptureScreenshots(ctx context.Context, root string, inventory Inventory, report RunReport) (ScreenshotBatch, error) {
	batch := ScreenshotBatch{RunID: report.ID, CapturedAt: time.Now().UTC()}
	deviceByID := make(map[string]Device, len(inventory.Devices))
	for _, device := range inventory.Devices {
		deviceByID[device.ID] = device
	}
	statePath := filepath.Join(root, "state.json")
	state, err := loadScreenshotState(statePath)
	if err != nil {
		return batch, err
	}
	runDirectory := filepath.Join(root, report.ID)
	if err := os.MkdirAll(runDirectory, 0o750); err != nil {
		return batch, err
	}

	for _, result := range report.Results {
		device, exists := deviceByID[result.DeviceID]
		artifact := ScreenshotArtifact{
			RunID: report.ID, DeviceID: result.DeviceID, DeviceName: result.DeviceName,
			CapturedAt: time.Now().UTC(), Status: "failed", SemanticReviewStatus: "capture_failed",
		}
		if !exists {
			artifact.Error = "device no longer exists in inventory"
			batch.Artifacts = append(batch.Artifacts, artifact)
			continue
		}
		if device.OS != "macos" && device.OS != "linux" {
			artifact.Status = "unsupported"
			artifact.SemanticReviewStatus = "not_applicable"
			artifact.Error = fmt.Sprintf("screenshots are not supported for %s devices", device.OS)
			batch.Artifacts = append(batch.Artifacts, artifact)
			continue
		}
		captureCtx, cancel := context.WithTimeout(ctx, screenshotTimeout)
		data, captureErr := captureScreenshot(captureCtx, device)
		cancel()
		if captureErr != nil {
			artifact.Error = captureErr.Error()
			batch.Artifacts = append(batch.Artifacts, artifact)
			continue
		}
		path := filepath.Join(runDirectory, safeFileName(device.ID)+".png")
		if err := os.WriteFile(path, data, 0o640); err != nil {
			artifact.Error = err.Error()
			batch.Artifacts = append(batch.Artifacts, artifact)
			continue
		}
		artifact.Status = "captured"
		artifact.Path = path
		artifact.SemanticReviewStatus = "pending"
		previous := state.Devices[device.ID]
		analysisSucceeded := true
		if err := analyzeScreenshot(path, previous.Path, &artifact); err != nil {
			analysisSucceeded = false
			artifact.Status = "analysis_failed"
			artifact.Error = err.Error()
			artifact.SemanticReviewStatus = "pending"
		}
		if analysisSucceeded && artifact.PreviousPath != "" && artifact.ChangedPixelsPercent <= unchangedPixelThreshold {
			artifact.UnchangedFrames = previous.UnchangedFrames + 1
		}
		artifact.PossiblyFrozen = artifact.UnchangedFrames >= frozenFrameThreshold
		if analysisSucceeded {
			state.Devices[device.ID] = screenshotDeviceState{Path: path, UnchangedFrames: artifact.UnchangedFrames}
		}
		batch.Artifacts = append(batch.Artifacts, artifact)
	}
	sort.Slice(batch.Artifacts, func(left, right int) bool {
		return batch.Artifacts[left].DeviceID < batch.Artifacts[right].DeviceID
	})
	if err := saveJSONAtomic(filepath.Join(runDirectory, "manifest.json"), batch); err != nil {
		return batch, err
	}
	if err := saveScreenshotState(statePath, state); err != nil {
		return batch, err
	}
	if err := appendScreenshotQueue(filepath.Join(root, "review-queue.jsonl"), batch.Artifacts); err != nil {
		return batch, err
	}
	return batch, nil
}

func captureScreenshot(ctx context.Context, device Device) ([]byte, error) {
	if device.Transport == "local" {
		return captureLocalScreenshot(ctx, device.OS)
	}
	if device.Transport != "ssh" {
		return nil, fmt.Errorf("transport %q cannot capture a desktop", device.Transport)
	}
	if device.Address == "" || device.SSHUser == "" {
		return nil, errors.New("ssh screenshot capture requires address and sshUser")
	}
	remoteCommand := linuxScreenshotCommand
	if device.OS == "macos" {
		remoteCommand = macOSScreenshotCommand
	}
	target := device.SSHUser + "@" + device.Address
	command := exec.CommandContext(ctx, "ssh",
		"-F", "/dev/null",
		"-o", "BatchMode=yes",
		"-o", "ConnectTimeout=5",
		"-o", "LogLevel=ERROR",
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
		target, remoteCommand,
	)
	encoded, err := command.CombinedOutput()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		return nil, fmt.Errorf("remote screenshot: %s: %w", strings.TrimSpace(string(encoded)), err)
	}
	data, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(encoded)))
	if err != nil {
		return nil, fmt.Errorf("decode remote screenshot: %w", err)
	}
	return data, nil
}

func captureLocalScreenshot(ctx context.Context, operatingSystem string) ([]byte, error) {
	directory, err := os.MkdirTemp("", "farm-screenshot-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(directory)
	path := filepath.Join(directory, "capture.png")
	var command *exec.Cmd
	if operatingSystem == "macos" {
		command = exec.CommandContext(ctx, "/usr/sbin/screencapture", "-x", "-t", "png", path)
	} else {
		command = exec.CommandContext(ctx, "sh", "-c", linuxScreenshotCommand, "farm-screenshot", path)
		command.Env = os.Environ()
	}
	output, err := command.CombinedOutput()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		return nil, fmt.Errorf("local screenshot: %s: %w", strings.TrimSpace(string(output)), err)
	}
	return os.ReadFile(path)
}

const macOSScreenshotCommand = `tmp=$(mktemp /tmp/farm-screenshot.XXXXXX.png) || exit 1
trap 'rm -f "$tmp"' EXIT
/usr/sbin/screencapture -x -t png "$tmp" || exit 1
base64 < "$tmp"`

// The optional first argument lets local capture provide an explicit output
// path. Remote capture omits it and receives base64 on stdout.
const linuxScreenshotCommand = `if [ -n "$1" ]; then tmp=$1; emit=0; else tmp=$(mktemp /tmp/farm-screenshot.XXXXXX.png) || exit 1; emit=1; fi
trap 'if [ "$emit" = 1 ]; then rm -f "$tmp"; fi' EXIT
if command -v grim >/dev/null 2>&1; then grim "$tmp"
elif command -v gnome-screenshot >/dev/null 2>&1; then gnome-screenshot -f "$tmp"
elif command -v scrot >/dev/null 2>&1; then scrot "$tmp"
elif command -v import >/dev/null 2>&1; then import -window root "$tmp"
else echo 'no supported screenshot utility (grim, gnome-screenshot, scrot, or import)' >&2; exit 127
fi || exit 1
if [ "$emit" = 1 ]; then base64 < "$tmp"; fi`

func analyzeScreenshot(path, previousPath string, artifact *ScreenshotArtifact) error {
	current, err := decodeImage(path)
	if err != nil {
		return err
	}
	bounds := current.Bounds()
	artifact.Width = bounds.Dx()
	artifact.Height = bounds.Dy()
	mean, deviation := luminanceStats(current)
	artifact.MeanLuminance = mean
	artifact.LuminanceDeviation = deviation
	artifact.LooksBlank = mean < 2 || deviation < 1
	if previousPath == "" {
		return nil
	}
	previous, err := decodeImage(previousPath)
	if err != nil {
		return nil
	}
	artifact.PreviousPath = previousPath
	artifact.ChangedPixelsPercent = changedPixelsPercent(current, previous)
	return nil
}

func decodeImage(path string) (image.Image, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	decoded, _, err := image.Decode(file)
	return decoded, err
}

func luminanceStats(source image.Image) (float64, float64) {
	bounds := source.Bounds()
	step := sampleStep(bounds.Dx() * bounds.Dy())
	var count int
	var sum, sumSquares float64
	for y := bounds.Min.Y; y < bounds.Max.Y; y += step {
		for x := bounds.Min.X; x < bounds.Max.X; x += step {
			value := luminance(source.At(x, y))
			count++
			sum += value
			sumSquares += value * value
		}
	}
	if count == 0 {
		return 0, 0
	}
	mean := sum / float64(count)
	variance := math.Max(0, sumSquares/float64(count)-mean*mean)
	return mean, math.Sqrt(variance)
}

func changedPixelsPercent(current, previous image.Image) float64 {
	currentBounds, previousBounds := current.Bounds(), previous.Bounds()
	if currentBounds.Dx() != previousBounds.Dx() || currentBounds.Dy() != previousBounds.Dy() {
		return 100
	}
	step := sampleStep(currentBounds.Dx() * currentBounds.Dy())
	var sampled, changed int
	for y := currentBounds.Min.Y; y < currentBounds.Max.Y; y += step {
		for x := currentBounds.Min.X; x < currentBounds.Max.X; x += step {
			sampled++
			if math.Abs(luminance(current.At(x, y))-luminance(previous.At(x, y))) > 8 {
				changed++
			}
		}
	}
	if sampled == 0 {
		return 0
	}
	return float64(changed) * 100 / float64(sampled)
}

func luminance(colorValue interface{ RGBA() (r, g, b, a uint32) }) float64 {
	red, green, blue, _ := colorValue.RGBA()
	return 0.2126*float64(red>>8) + 0.7152*float64(green>>8) + 0.0722*float64(blue>>8)
}

func sampleStep(pixels int) int {
	const maximumSamples = 100000
	if pixels <= maximumSamples {
		return 1
	}
	return int(math.Ceil(math.Sqrt(float64(pixels) / maximumSamples)))
}

func safeFileName(value string) string {
	return strings.Map(func(character rune) rune {
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || character == '-' || character == '_' {
			return character
		}
		return '_'
	}, value)
}

func loadScreenshotState(path string) (screenshotState, error) {
	state := screenshotState{Version: 1, Devices: make(map[string]screenshotDeviceState)}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return state, nil
	}
	if err != nil {
		return state, err
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return state, err
	}
	if state.Version != 1 {
		return state, fmt.Errorf("unsupported screenshot state version %d", state.Version)
	}
	if state.Devices == nil {
		state.Devices = make(map[string]screenshotDeviceState)
	}
	return state, nil
}

func saveScreenshotState(path string, state screenshotState) error {
	return saveJSONAtomic(path, state)
}

func saveJSONAtomic(path string, value any) error {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o750); err != nil {
		return err
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(directory, ".farm-json-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if _, err := temporary.Write(append(data, '\n')); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}

func appendScreenshotQueue(path string, artifacts []ScreenshotArtifact) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o640)
	if err != nil {
		return err
	}
	defer file.Close()
	writer := bufio.NewWriter(file)
	for _, artifact := range artifacts {
		if artifact.Status != "captured" && artifact.Status != "analysis_failed" {
			continue
		}
		data, err := json.Marshal(artifact)
		if err != nil {
			return err
		}
		if _, err := writer.Write(append(data, '\n')); err != nil {
			return err
		}
	}
	return writer.Flush()
}
