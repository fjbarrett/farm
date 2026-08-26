package farm

import "time"

const Version = "0.2.0"

type Inventory struct {
	Version int      `json:"version"`
	Network Network  `json:"network"`
	Devices []Device `json:"devices"`
}

type Network struct {
	Name       string `json:"name"`
	Subnet     string `json:"subnet"`
	Connection string `json:"connection"`
}

type Device struct {
	ID               string   `json:"id"`
	Name             string   `json:"name"`
	Hostname         string   `json:"hostname"`
	Address          string   `json:"address"`
	OS               string   `json:"os"`
	OSVersion        string   `json:"osVersion"`
	Arch             string   `json:"arch"`
	Transport        string   `json:"transport"`
	SSHUser          string   `json:"sshUser,omitempty"`
	HealthURL        string   `json:"healthUrl,omitempty"`
	AllowSelfSigned  bool     `json:"allowSelfSigned,omitempty"`
	PhysicalLocation string   `json:"physicalLocation"`
	Labels           []string `json:"labels"`
	AgentURL         string   `json:"agentUrl,omitempty"`
}

type Job struct {
	APIVersion string    `json:"apiVersion"`
	Name       string    `json:"name"`
	Kind       string    `json:"kind"`
	Selector   Selector  `json:"selector"`
	Strategy   Strategy  `json:"strategy"`
	Execution  Execution `json:"execution,omitempty"`
	Steps      []Step    `json:"steps,omitempty"`
}

type Selector struct {
	DeviceIDs        []string `json:"deviceIds,omitempty"`
	AllLabels        []string `json:"allLabels,omitempty"`
	AnyLabels        []string `json:"anyLabels,omitempty"`
	OperatingSystems []string `json:"operatingSystems,omitempty"`
	Architectures    []string `json:"architectures,omitempty"`
}

type Strategy struct {
	MaxParallel int  `json:"maxParallel"`
	FailFast    bool `json:"failFast"`
}

type Execution struct {
	Isolation string `json:"isolation,omitempty"`
	Network   string `json:"network,omitempty"`
	Workspace string `json:"workspace,omitempty"`
}

type Step struct {
	Name           string   `json:"name"`
	Command        []string `json:"command"`
	TimeoutSeconds int      `json:"timeoutSeconds"`
}

type Plan struct {
	JobName       string  `json:"jobName"`
	Kind          string  `json:"kind"`
	MaxParallel   int     `json:"maxParallel"`
	SelectedCount int     `json:"selectedCount"`
	RunnableCount int     `json:"runnableCount"`
	Shards        []Shard `json:"shards"`
}

type Shard struct {
	Index    int      `json:"index"`
	DeviceID string   `json:"deviceId"`
	Name     string   `json:"name"`
	OS       string   `json:"os"`
	Arch     string   `json:"arch"`
	Labels   []string `json:"labels"`
	Runnable bool     `json:"runnable"`
	Reason   string   `json:"reason,omitempty"`
}

type ProbeResult struct {
	DeviceID   string `json:"deviceId"`
	DeviceName string `json:"deviceName"`
	Status     string `json:"status"`
	Transport  string `json:"transport"`
	DurationMS int64  `json:"durationMs"`
	Output     string `json:"output,omitempty"`
	Error      string `json:"error,omitempty"`
}

type RunReport struct {
	ID         string        `json:"id"`
	JobName    string        `json:"jobName"`
	Kind       string        `json:"kind"`
	StartedAt  time.Time     `json:"startedAt"`
	FinishedAt time.Time     `json:"finishedAt"`
	Status     string        `json:"status"`
	Results    []ProbeResult `json:"results"`
}

// Intelligence is the durable, cumulative view derived from raw run reports.
// Raw reports remain the source evidence; this structure makes trends and
// changes cheap to consume while a loop is running.
type Intelligence struct {
	Version           int                  `json:"version"`
	GeneratedAt       time.Time            `json:"generatedAt"`
	FirstRunAt        time.Time            `json:"firstRunAt"`
	LastRunAt         time.Time            `json:"lastRunAt"`
	LastRunID         string               `json:"lastRunId"`
	TotalRuns         int64                `json:"totalRuns"`
	PassedRuns        int64                `json:"passedRuns"`
	FailedRuns        int64                `json:"failedRuns"`
	TotalObservations int64                `json:"totalObservations"`
	Devices           []DeviceIntelligence `json:"devices"`
	RecentFindings    []Finding            `json:"recentFindings,omitempty"`
}

type DeviceIntelligence struct {
	DeviceID               string            `json:"deviceId"`
	DeviceName             string            `json:"deviceName"`
	Transport              string            `json:"transport"`
	FirstSeenAt            time.Time         `json:"firstSeenAt"`
	LastSeenAt             time.Time         `json:"lastSeenAt"`
	Observations           int64             `json:"observations"`
	Passed                 int64             `json:"passed"`
	Failed                 int64             `json:"failed"`
	Availability           float64           `json:"availability"`
	LatestStatus           string            `json:"latestStatus"`
	ConsecutiveFailures    int64             `json:"consecutiveFailures"`
	StatusTransitions      int64             `json:"statusTransitions"`
	LatestDurationMS       int64             `json:"latestDurationMs"`
	AverageDurationMS      float64           `json:"averageDurationMs"`
	MinDurationMS          int64             `json:"minDurationMs"`
	MaxDurationMS          int64             `json:"maxDurationMs"`
	TotalDurationMS        int64             `json:"totalDurationMs"`
	Attributes             map[string]string `json:"attributes,omitempty"`
	LastError              string            `json:"lastError,omitempty"`
	ScreenshotCaptures     int64             `json:"screenshotCaptures"`
	ScreenshotFailures     int64             `json:"screenshotFailures"`
	BlankFrames            int64             `json:"blankFrames"`
	PossiblyFrozenFrames   int64             `json:"possiblyFrozenFrames"`
	LatestScreenshotStatus string            `json:"latestScreenshotStatus,omitempty"`
	LastScreenshotPath     string            `json:"lastScreenshotPath,omitempty"`
	LastScreenshotAt       time.Time         `json:"lastScreenshotAt,omitempty"`
	SemanticReviewsPending int64             `json:"semanticReviewsPending"`
}

type Finding struct {
	RunID      string    `json:"runId"`
	ObservedAt time.Time `json:"observedAt"`
	DeviceID   string    `json:"deviceId"`
	Kind       string    `json:"kind"`
	Message    string    `json:"message"`
}

type ScreenshotBatch struct {
	RunID      string               `json:"runId"`
	CapturedAt time.Time            `json:"capturedAt"`
	Artifacts  []ScreenshotArtifact `json:"artifacts"`
}

type ScreenshotArtifact struct {
	RunID                string    `json:"runId"`
	DeviceID             string    `json:"deviceId"`
	DeviceName           string    `json:"deviceName"`
	CapturedAt           time.Time `json:"capturedAt"`
	Status               string    `json:"status"`
	Path                 string    `json:"path,omitempty"`
	Error                string    `json:"error,omitempty"`
	Width                int       `json:"width,omitempty"`
	Height               int       `json:"height,omitempty"`
	MeanLuminance        float64   `json:"meanLuminance,omitempty"`
	LuminanceDeviation   float64   `json:"luminanceDeviation,omitempty"`
	LooksBlank           bool      `json:"looksBlank,omitempty"`
	PreviousPath         string    `json:"previousPath,omitempty"`
	ChangedPixelsPercent float64   `json:"changedPixelsPercent,omitempty"`
	UnchangedFrames      int       `json:"unchangedFrames,omitempty"`
	PossiblyFrozen       bool      `json:"possiblyFrozen,omitempty"`
	SemanticReviewStatus string    `json:"semanticReviewStatus"`
}
