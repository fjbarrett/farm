package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"farm/internal/farm"
)

const defaultInventory = "farm.inventory.json"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "farm:", err)
		os.Exit(1)
	}
}

func run(arguments []string) error {
	if len(arguments) == 0 {
		printUsage()
		return nil
	}
	switch arguments[0] {
	case "inventory":
		return inventoryCommand(arguments[1:])
	case "plan":
		return planCommand(arguments[1:])
	case "doctor":
		return doctorCommand(arguments[1:])
	case "run":
		return runCommand(arguments[1:])
	case "loop":
		return loopCommand(arguments[1:])
	case "serve":
		return serveCommand(arguments[1:])
	case "version", "--version", "-v":
		fmt.Println(farm.Version)
		return nil
	case "help", "--help", "-h":
		printUsage()
		return nil
	default:
		return fmt.Errorf("unknown command %q", arguments[0])
	}
}

func readinessJob() farm.Job {
	return farm.Job{
		APIVersion: "farm/v1",
		Name:       "farm-readiness",
		Kind:       "probe",
		Selector:   farm.Selector{AnyLabels: []string{"test-worker", "infrastructure"}},
		Strategy:   farm.Strategy{MaxParallel: 5},
	}
}

func inventoryCommand(arguments []string) error {
	flags := flag.NewFlagSet("inventory", flag.ContinueOnError)
	path := flags.String("inventory", defaultInventory, "inventory file")
	jsonOutput := flags.Bool("json", false, "print JSON")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	inventory, err := farm.LoadInventory(*path)
	if err != nil {
		return err
	}
	if *jsonOutput {
		return printJSON(inventory)
	}
	writer := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(writer, "ID\tOS / ARCH\tADDRESS\tTRANSPORT\tCODE EXECUTION\tLOCATION")
	for _, device := range inventory.Devices {
		execution := "agent needed"
		if device.AgentURL != "" {
			execution = "ready"
		}
		fmt.Fprintf(writer, "%s\t%s / %s\t%s\t%s\t%s\t%s\n", device.ID, device.OS, device.Arch, device.Address, device.Transport, execution, device.PhysicalLocation)
	}
	return writer.Flush()
}

func planCommand(arguments []string) error {
	flags := flag.NewFlagSet("plan", flag.ContinueOnError)
	path := flags.String("inventory", defaultInventory, "inventory file")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if flags.NArg() != 1 {
		return errors.New("usage: farm plan [flags] JOB.json")
	}
	inventory, err := farm.LoadInventory(*path)
	if err != nil {
		return err
	}
	job, err := farm.LoadJob(flags.Arg(0))
	if err != nil {
		return err
	}
	plan, err := farm.BuildPlan(inventory, job)
	if err != nil {
		return err
	}
	return printJSON(plan)
}

func doctorCommand(arguments []string) error {
	flags := flag.NewFlagSet("doctor", flag.ContinueOnError)
	path := flags.String("inventory", defaultInventory, "inventory file")
	runs := flags.String("runs", "runs", "run report directory")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	inventory, err := farm.LoadInventory(*path)
	if err != nil {
		return err
	}
	job := readinessJob()
	report, err := farm.RunJob(context.Background(), inventory, job)
	if err != nil {
		return err
	}
	pathWritten, err := farm.SaveRunReport(*runs, report)
	if err != nil {
		return err
	}
	printProbeReport(report)
	fmt.Println("report:", pathWritten)
	if report.Status != "passed" {
		return errors.New("one or more devices failed their readiness probe")
	}
	return nil
}

func loopCommand(arguments []string) error {
	flags := flag.NewFlagSet("loop", flag.ContinueOnError)
	inventoryPath := flags.String("inventory", defaultInventory, "inventory file")
	runs := flags.String("runs", "runs", "raw run report directory")
	intelligencePath := flags.String("intelligence", "", "cumulative intelligence file (default RUNS/intelligence.json)")
	screenshots := flags.Bool("screenshots", true, "capture and analyze desktop screenshots")
	screenshotsDirectory := flags.String("screenshots-directory", "", "screenshot artifact directory (default RUNS/screenshots)")
	interval := flags.Duration("interval", 30*time.Second, "delay between loop iterations")
	iterations := flags.Int("iterations", 0, "stop after this many iterations; 0 runs forever")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if flags.NArg() > 1 {
		return errors.New("usage: farm loop [flags] [JOB.json]")
	}
	job := readinessJob()
	if flags.NArg() == 1 {
		loaded, err := farm.LoadJob(flags.Arg(0))
		if err != nil {
			return err
		}
		job = loaded
	}
	if _, err := farm.LoadInventory(*inventoryPath); err != nil {
		return err
	}
	if *intelligencePath == "" {
		*intelligencePath = filepath.Join(*runs, "intelligence.json")
	}
	if *screenshotsDirectory == "" {
		*screenshotsDirectory = filepath.Join(*runs, "screenshots")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	fmt.Printf("continuous loop started: job=%s interval=%s (Ctrl-C to stop)\n", job.Name, interval.String())
	return farm.RunLoop(ctx, farm.LoopConfig{
		InventoryPath:        *inventoryPath,
		RunsDirectory:        *runs,
		IntelligencePath:     *intelligencePath,
		Job:                  job,
		Interval:             *interval,
		MaxIterations:        *iterations,
		CaptureScreenshots:   *screenshots,
		ScreenshotsDirectory: *screenshotsDirectory,
		OnIteration: func(iteration int, report farm.RunReport, intelligence farm.Intelligence, screenshotBatch farm.ScreenshotBatch, reportPath string) {
			fmt.Printf("\niteration %d: %s (%d observations, %d findings retained)\n", iteration, report.Status, intelligence.TotalObservations, len(intelligence.RecentFindings))
			printProbeReport(report)
			fmt.Println("report:", reportPath)
			fmt.Println("intelligence:", *intelligencePath)
			if *screenshots {
				captured, failed, unsupported, pending := 0, 0, 0, 0
				for _, artifact := range screenshotBatch.Artifacts {
					if artifact.Status == "captured" || artifact.Status == "analysis_failed" {
						captured++
					}
					if artifact.Status == "failed" {
						failed++
					}
					if artifact.Status == "unsupported" {
						unsupported++
					}
					if artifact.SemanticReviewStatus == "pending" {
						pending++
					}
				}
				fmt.Printf("screenshots: %s (%d captured, %d failed, %d unsupported, %d pending semantic UI review)\n", *screenshotsDirectory, captured, failed, unsupported, pending)
			}
		},
		OnError: func(iteration int, err error) {
			fmt.Fprintf(os.Stderr, "iteration %d error: %v (loop will continue)\n", iteration, err)
		},
	})
}

func runCommand(arguments []string) error {
	flags := flag.NewFlagSet("run", flag.ContinueOnError)
	path := flags.String("inventory", defaultInventory, "inventory file")
	runs := flags.String("runs", "runs", "run report directory")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if flags.NArg() != 1 {
		return errors.New("usage: farm run [flags] JOB.json")
	}
	inventory, err := farm.LoadInventory(*path)
	if err != nil {
		return err
	}
	job, err := farm.LoadJob(flags.Arg(0))
	if err != nil {
		return err
	}
	report, err := farm.RunJob(context.Background(), inventory, job)
	if err != nil {
		return err
	}
	pathWritten, err := farm.SaveRunReport(*runs, report)
	if err != nil {
		return err
	}
	printProbeReport(report)
	fmt.Println("report:", pathWritten)
	if report.Status != "passed" {
		return errors.New("run failed")
	}
	return nil
}

func serveCommand(arguments []string) error {
	flags := flag.NewFlagSet("serve", flag.ContinueOnError)
	path := flags.String("inventory", defaultInventory, "inventory file")
	runs := flags.String("runs", "runs", "run report directory")
	listen := flags.String("listen", "127.0.0.1:7331", "loopback listen address")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if _, err := farm.LoadInventory(*path); err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	controller := farm.Controller{InventoryPath: *path, RunsDirectory: *runs}
	fmt.Printf("farm controller %s listening on http://%s\n", farm.Version, *listen)
	return controller.ListenAndServe(ctx, *listen)
}

func printProbeReport(report farm.RunReport) {
	writer := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(writer, "DEVICE\tSTATUS\tTRANSPORT\tTIME\tDETAIL")
	for _, result := range report.Results {
		detail := result.Output
		if result.Error != "" {
			detail = result.Error
		}
		detail = strings.ReplaceAll(strings.TrimSpace(detail), "\n", "; ")
		fmt.Fprintf(writer, "%s\t%s\t%s\t%dms\t%s\n", result.DeviceID, result.Status, result.Transport, result.DurationMS, detail)
	}
	_ = writer.Flush()
}

func printJSON(value any) error {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func printUsage() {
	fmt.Print(`farm — local device-farm controller

Usage:
  farm inventory               List recorded devices and worker readiness
  farm plan JOB.json           Expand a job across matching devices
  farm doctor                  Run safe health probes and save a report
  farm run JOB.json            Run a probe job (test jobs require farm-agent)
  farm loop [JOB.json]         Continuously test, retain raw data, and derive trends
  farm serve                   Start the localhost HTTP API on port 7331
  farm version                 Print the controller version

The controller never executes submitted test commands itself.
`)
}
