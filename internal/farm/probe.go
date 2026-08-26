package farm

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

func RunJob(ctx context.Context, inventory Inventory, job Job) (RunReport, error) {
	plan, err := BuildPlan(inventory, job)
	if err != nil {
		return RunReport{}, err
	}
	if job.Kind != "probe" {
		return RunReport{}, fmt.Errorf("%w: install and register farm-agent on each target before running code", ErrAgentRequired)
	}

	report := RunReport{
		ID:        fmt.Sprintf("run-%s", time.Now().UTC().Format("20060102T150405.000000000Z")),
		JobName:   job.Name,
		Kind:      job.Kind,
		StartedAt: time.Now().UTC(),
		Status:    "passed",
		Results:   make([]ProbeResult, len(plan.Shards)),
	}
	devices := make(map[string]Device, len(inventory.Devices))
	for _, device := range inventory.Devices {
		devices[device.ID] = device
	}

	parallel := plan.MaxParallel
	semaphore := make(chan struct{}, parallel)
	probeCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var wg sync.WaitGroup
	for index, shard := range plan.Shards {
		index, shard := index, shard
		wg.Add(1)
		go func() {
			defer wg.Done()
			semaphore <- struct{}{}
			defer func() { <-semaphore }()
			result := probeDevice(probeCtx, devices[shard.DeviceID])
			report.Results[index] = result
			if result.Status != "passed" && job.Strategy.FailFast {
				cancel()
			}
		}()
	}
	wg.Wait()
	report.FinishedAt = time.Now().UTC()
	for _, result := range report.Results {
		if result.Status != "passed" {
			report.Status = "failed"
			break
		}
	}
	return report, nil
}

func probeDevice(parent context.Context, device Device) ProbeResult {
	started := time.Now()
	result := ProbeResult{DeviceID: device.ID, DeviceName: device.Name, Transport: device.Transport, Status: "failed"}
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()

	var output string
	var err error
	switch device.Transport {
	case "local":
		output, err = runCommand(ctx, "sh", "-c", "printf 'hostname='; hostname; printf '\nos='; sw_vers -productVersion 2>/dev/null || uname -sr; printf '\narch='; uname -m")
	case "ssh":
		output, err = probeSSH(ctx, device)
	case "https":
		output, err = probeHTTPS(ctx, device)
	default:
		err = fmt.Errorf("unsupported transport %q", device.Transport)
	}
	result.DurationMS = time.Since(started).Milliseconds()
	result.Output = strings.TrimSpace(output)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	result.Status = "passed"
	return result
}

func probeSSH(ctx context.Context, device Device) (string, error) {
	if device.Address == "" || device.SSHUser == "" {
		return "", errors.New("ssh transport requires address and sshUser")
	}
	remoteCommand := "printf 'hostname='; hostname; printf '\nos='; if command -v sw_vers >/dev/null 2>&1; then sw_vers -productVersion; elif test -r /etc/os-release; then . /etc/os-release; printf '%s' \"$PRETTY_NAME\"; else uname -sr; fi; printf '\narch='; uname -m"
	target := device.SSHUser + "@" + device.Address
	return runCommand(ctx, "ssh",
		"-F", "/dev/null",
		"-o", "BatchMode=yes",
		"-o", "ConnectTimeout=5",
		"-o", "LogLevel=ERROR",
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
		target,
		remoteCommand,
	)
}

func probeHTTPS(ctx context.Context, device Device) (string, error) {
	if device.HealthURL == "" {
		return "", errors.New("https transport requires healthUrl")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if device.AllowSelfSigned {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} // Home-lab endpoint with a self-signed certificate.
	}
	client := &http.Client{Transport: transport}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, device.HealthURL, nil)
	if err != nil {
		return "", err
	}
	response, err := client.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode >= 500 {
		return "", fmt.Errorf("health endpoint returned %s", response.Status)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 64<<10))
	if err != nil {
		return "", err
	}
	title := extractTitle(string(body))
	if title == "" {
		title = response.Status
	}
	return fmt.Sprintf("status=%d\ntitle=%s", response.StatusCode, title), nil
}

func extractTitle(body string) string {
	lower := strings.ToLower(body)
	start := strings.Index(lower, "<title>")
	end := strings.Index(lower, "</title>")
	if start < 0 || end <= start {
		return ""
	}
	return strings.TrimSpace(body[start+len("<title>") : end])
}

func runCommand(ctx context.Context, name string, args ...string) (string, error) {
	command := exec.CommandContext(ctx, name, args...)
	combined, err := command.CombinedOutput()
	if ctx.Err() != nil {
		return string(combined), ctx.Err()
	}
	if err != nil {
		return string(combined), fmt.Errorf("%s: %w", strings.TrimSpace(string(combined)), err)
	}
	return string(combined), nil
}

func SaveRunReport(directory string, report RunReport) (string, error) {
	if err := os.MkdirAll(directory, 0o750); err != nil {
		return "", err
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return "", err
	}
	temporary, err := os.CreateTemp(directory, ".run-*.tmp")
	if err != nil {
		return "", err
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	if _, err := temporary.Write(append(data, '\n')); err != nil {
		temporary.Close()
		return "", err
	}
	if err := temporary.Close(); err != nil {
		return "", err
	}
	finalPath := filepath.Join(directory, report.ID+".json")
	if err := os.Rename(temporaryName, finalPath); err != nil {
		return "", err
	}
	return finalPath, nil
}
