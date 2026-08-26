# Farm

Farm is a local-first control plane for a private device inventory. It gives applications and CI systems a versioned job format, device selection, test-matrix planning, readiness probes, and a localhost HTTP API.

The first version deliberately separates **reachability** from **code execution**. Health probes can run today. Submitted code is not executed until a sandboxed `farm-agent` is installed and registered on a target device.

## Build and inspect

```sh
cp farm.inventory.example.json farm.inventory.json
# Edit farm.inventory.json with your local device details.

mkdir -p bin
go build -o bin/farm ./cmd/farm

./bin/farm inventory
./bin/farm doctor
./bin/farm plan jobs/cross-platform-example.json
```

`farm.inventory.json` and `DEVICE_INVENTORY.md` are deliberately ignored because they may contain hostnames, IP addresses, usernames, physical locations, and hardware identifiers. Safe starting points are provided in [farm.inventory.example.json](farm.inventory.example.json) and [DEVICE_INVENTORY.example.md](DEVICE_INVENTORY.example.md). Do not put passwords, private keys, access tokens, or other credentials in either file.

`doctor` performs read-only checks over the configured local, SSH, or HTTPS transport and saves a timestamped JSON report under `runs/`.

## Continuous testing loop

Run the default farm-readiness probe immediately and then every 30 seconds until interrupted:

```sh
./bin/farm loop
```

The loop is deliberately persistent: a failed probe or a temporarily unreadable inventory is reported, then the next iteration still runs. Press `Ctrl-C` for a clean stop. The inventory is reloaded on every iteration so device changes are picked up without restarting.

Each successful iteration produces three kinds of output:

- `runs/run-*.json` is immutable raw evidence for that iteration.
- `runs/intelligence.json` is an atomically updated cumulative view with availability, latency trends, consecutive failures, status transitions, discovered devices, recoveries, and changed probe attributes.
- `runs/screenshots/<run-id>/` contains timestamped screenshots and a manifest tied to the exact run and device.

Screenshot capture is enabled by default for continuous loops. It is non-interactive and independent of the probe result, so a screen can reveal a stuck dialog, blank render, or visual failure even when reachability passes. Every captured image gets immediate checks for blank frames, pixel changes, and repeated unchanged frames. After three unchanged captures it is marked `possiblyFrozen`; this is evidence for review, not a definitive failure.

Captured images are appended to `runs/screenshots/review-queue.jsonl` with `semanticReviewStatus: pending`. That queue is the handoff for later vision-based review of UI state and functionality. Screenshots can contain private on-screen information, so their files are created with restricted permissions. Disable capture when necessary with `--screenshots=false`.

The entire `runs/` directory is ignored. Treat it as sensitive even when it only contains JSON: probe output records hostnames and endpoint titles, while screenshot artifacts can include any information visible in a logged-in desktop session.

Remote capture requires an active desktop session. macOS also requires Screen Recording permission for the SSH user; Linux needs one of `grim`, `gnome-screenshot`, `scrot`, or ImageMagick `import` plus access to the graphical session. Missing permission or tooling is written to the run manifest and visual intelligence as a capture failure, while the testing loop continues.

Use another probe job, change the cadence, or make a bounded run like this:

```sh
./bin/farm loop --interval 5m jobs/farm-readiness.json
./bin/farm loop --interval 1s --iterations 10
```

Visual findings are also folded into `intelligence.json`: per-device capture/failure counts, blank and possibly-frozen frame counts, last screenshot paths, and the number awaiting semantic review. `--iterations 0` is the default and means run forever. Existing intelligence and screenshot state are loaded on restart, so learning continues across controller sessions.

## Programmatic API

Start the controller:

```sh
./bin/farm serve
```

It listens only on `127.0.0.1:7331`. The available endpoints are:

| Method | Path | Purpose |
| --- | --- | --- |
| `GET` | `/health` | Controller health and version |
| `GET` | `/v1/devices` | Machine-readable inventory |
| `POST` | `/v1/plan` | Validate and expand a `farm/v1` job |
| `POST` | `/v1/runs` | Execute a safe probe job and save its report |

Example planning request:

```sh
curl -sS \
  -H 'content-type: application/json' \
  --data-binary @jobs/cross-platform-example.json \
  http://127.0.0.1:7331/v1/plan
```

## Job contract

Jobs use `apiVersion: farm/v1` and one of two kinds:

- `probe` checks identity and reachability without accepting arbitrary commands.
- `test` contains commands and device selectors. It must request required isolation and an ephemeral workspace. Planning works now; execution stays blocked until the selected devices have registered agents.

Selectors can match device IDs, operating systems, architectures, and labels such as `cuda`, `macos`, `linux`, or `gpu`. One selected device becomes one test shard, bounded by `strategy.maxParallel`.

## Execution boundary

The controller validates and schedules; it does not run submitted commands. Worker agents will be responsible for:

1. Creating a disposable workspace.
2. Verifying the uploaded artifact digest.
3. Running Linux work in a locked-down container (no privileges, explicit network policy, resource limits, and GPU access only when requested).
4. Running macOS-native work under a dedicated test account or ephemeral VM.
5. Returning exit status, logs, timing, and artifacts, then destroying the workspace.

The API remains loopback-only until authentication, signed artifacts, and agent enrollment are implemented.
