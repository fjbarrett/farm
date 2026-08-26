package farm

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

var ErrAgentRequired = errors.New("sandboxed worker agent required")

func ValidateJob(job Job) error {
	if job.APIVersion != "farm/v1" {
		return fmt.Errorf("unsupported apiVersion %q; expected farm/v1", job.APIVersion)
	}
	if strings.TrimSpace(job.Name) == "" {
		return errors.New("job name is required")
	}
	if job.Kind != "probe" && job.Kind != "test" {
		return fmt.Errorf("unsupported job kind %q; expected probe or test", job.Kind)
	}
	if job.Kind == "test" {
		if job.Execution.Isolation != "required" {
			return errors.New("test jobs must set execution.isolation to required")
		}
		if job.Execution.Workspace != "ephemeral" {
			return errors.New("test jobs must use an ephemeral workspace")
		}
		if len(job.Steps) == 0 {
			return errors.New("test jobs require at least one step")
		}
		for _, step := range job.Steps {
			if step.Name == "" || len(step.Command) == 0 {
				return errors.New("each test step requires a name and command")
			}
			if step.TimeoutSeconds < 1 || step.TimeoutSeconds > 3600 {
				return fmt.Errorf("step %q timeout must be between 1 and 3600 seconds", step.Name)
			}
		}
	}
	return nil
}

func BuildPlan(inventory Inventory, job Job) (Plan, error) {
	if err := ValidateJob(job); err != nil {
		return Plan{}, err
	}
	parallel := job.Strategy.MaxParallel
	if parallel <= 0 {
		parallel = 1
	}
	if parallel > 32 {
		return Plan{}, errors.New("strategy.maxParallel cannot exceed 32")
	}

	plan := Plan{JobName: job.Name, Kind: job.Kind, MaxParallel: parallel}
	for _, device := range inventory.Devices {
		if !matches(device, job.Selector) {
			continue
		}
		shard := Shard{
			Index:    len(plan.Shards) + 1,
			DeviceID: device.ID,
			Name:     device.Name,
			OS:       device.OS,
			Arch:     device.Arch,
			Labels:   device.Labels,
			Runnable: true,
		}
		if job.Kind == "test" && device.AgentURL == "" {
			shard.Runnable = false
			shard.Reason = "sandboxed worker agent is not installed"
		}
		if job.Kind == "probe" && device.Transport == "" {
			shard.Runnable = false
			shard.Reason = "no health-check transport configured"
		}
		plan.Shards = append(plan.Shards, shard)
		if shard.Runnable {
			plan.RunnableCount++
		}
	}
	plan.SelectedCount = len(plan.Shards)
	if plan.SelectedCount == 0 {
		return plan, errors.New("selector matched no devices")
	}
	return plan, nil
}

func matches(device Device, selector Selector) bool {
	if len(selector.DeviceIDs) > 0 && !containsFold(selector.DeviceIDs, device.ID) {
		return false
	}
	if len(selector.OperatingSystems) > 0 && !containsFold(selector.OperatingSystems, device.OS) {
		return false
	}
	if len(selector.Architectures) > 0 && !containsFold(selector.Architectures, device.Arch) {
		return false
	}
	for _, wanted := range selector.AllLabels {
		if !containsFold(device.Labels, wanted) {
			return false
		}
	}
	if len(selector.AnyLabels) > 0 {
		matched := false
		for _, wanted := range selector.AnyLabels {
			if containsFold(device.Labels, wanted) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	return true
}

func containsFold(values []string, wanted string) bool {
	wanted = strings.ToLower(wanted)
	return slices.ContainsFunc(values, func(value string) bool {
		return strings.ToLower(value) == wanted
	})
}
