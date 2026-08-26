package farm

import (
	"context"
	"fmt"
	"time"
)

type LoopConfig struct {
	InventoryPath        string
	RunsDirectory        string
	IntelligencePath     string
	Job                  Job
	Interval             time.Duration
	MaxIterations        int
	CaptureScreenshots   bool
	ScreenshotsDirectory string
	OnIteration          func(iteration int, report RunReport, intelligence Intelligence, screenshots ScreenshotBatch, reportPath string)
	OnError              func(iteration int, err error)
}

// RunLoop continuously reloads inventory and executes the job until the
// context is cancelled. A positive MaxIterations is useful for bounded runs
// and automated verification; zero means keep going indefinitely.
func RunLoop(ctx context.Context, config LoopConfig) error {
	if config.Interval <= 0 {
		return fmt.Errorf("loop interval must be greater than zero")
	}
	if config.MaxIterations < 0 {
		return fmt.Errorf("loop iterations cannot be negative")
	}
	if err := ValidateJob(config.Job); err != nil {
		return err
	}

	for iteration := 1; ; iteration++ {
		if err := ctx.Err(); err != nil {
			return nil
		}
		inventory, err := LoadInventory(config.InventoryPath)
		if err == nil {
			_, err = BuildPlan(inventory, config.Job)
		}
		if err == nil {
			var report RunReport
			report, err = RunJob(ctx, inventory, config.Job)
			if err == nil {
				var reportPath string
				reportPath, err = SaveRunReport(config.RunsDirectory, report)
				if err == nil {
					var intelligence Intelligence
					intelligence, err = UpdateIntelligence(config.IntelligencePath, report)
					if err == nil {
						var screenshots ScreenshotBatch
						if config.CaptureScreenshots {
							var captureErr error
							screenshots, captureErr = CaptureScreenshots(ctx, config.ScreenshotsDirectory, inventory, report)
							if captureErr != nil && config.OnError != nil {
								config.OnError(iteration, fmt.Errorf("capture screenshots: %w", captureErr))
							}
							if len(screenshots.Artifacts) > 0 {
								updated, intelligenceErr := UpdateScreenshotIntelligence(config.IntelligencePath, screenshots)
								if intelligenceErr != nil {
									if config.OnError != nil {
										config.OnError(iteration, fmt.Errorf("update screenshot intelligence: %w", intelligenceErr))
									}
								} else {
									intelligence = updated
								}
							}
						}
						if config.OnIteration != nil {
							config.OnIteration(iteration, report, intelligence, screenshots, reportPath)
						}
					}
				}
			}
		}
		if err != nil && config.OnError != nil {
			config.OnError(iteration, err)
		}
		if config.MaxIterations > 0 && iteration >= config.MaxIterations {
			return nil
		}

		timer := time.NewTimer(config.Interval)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return nil
		case <-timer.C:
		}
	}
}
