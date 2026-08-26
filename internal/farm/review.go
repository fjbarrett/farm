package farm

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	ScreenshotVerdictHealthy  = "healthy"
	ScreenshotVerdictDegraded = "degraded"
	ScreenshotVerdictFailed   = "failed"
	ScreenshotVerdictUnknown  = "unknown"

	maximumReviewLineBytes = 64 << 10
	maximumReviewSummary   = 4096
)

var reviewWriteMutex sync.Mutex

// LoadPendingScreenshotReviews returns queued artifacts that do not yet have
// a review in the append-only review ledger.
func LoadPendingScreenshotReviews(root string) ([]ScreenshotArtifact, error) {
	artifacts, err := loadScreenshotQueue(filepath.Join(root, "review-queue.jsonl"))
	if err != nil {
		return nil, err
	}
	reviews, err := LoadScreenshotReviews(root)
	if err != nil {
		return nil, err
	}
	reviewed := make(map[string]bool, len(reviews))
	for _, review := range reviews {
		reviewed[screenshotReviewKey(review.RunID, review.DeviceID, review.Path)] = true
	}
	pending := make([]ScreenshotArtifact, 0, len(artifacts))
	seen := make(map[string]bool, len(artifacts))
	for _, artifact := range artifacts {
		key := screenshotReviewKey(artifact.RunID, artifact.DeviceID, artifact.Path)
		if seen[key] || reviewed[key] {
			continue
		}
		seen[key] = true
		pending = append(pending, artifact)
	}
	sort.Slice(pending, func(left, right int) bool {
		if pending[left].CapturedAt.Equal(pending[right].CapturedAt) {
			if pending[left].RunID == pending[right].RunID {
				return pending[left].DeviceID < pending[right].DeviceID
			}
			return pending[left].RunID < pending[right].RunID
		}
		return pending[left].CapturedAt.Before(pending[right].CapturedAt)
	})
	return pending, nil
}

// LoadScreenshotReviews reads the complete semantic-review audit trail.
func LoadScreenshotReviews(root string) ([]ScreenshotReview, error) {
	path := filepath.Join(root, "reviews.jsonl")
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return []ScreenshotReview{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open screenshot reviews: %w", err)
	}
	defer file.Close()

	var reviews []ScreenshotReview
	if err := scanJSONLines(file, maximumReviewLineBytes, func(line []byte, lineNumber int) error {
		var review ScreenshotReview
		if err := json.Unmarshal(line, &review); err != nil {
			return fmt.Errorf("decode screenshot reviews line %d: %w", lineNumber, err)
		}
		if err := validateStoredScreenshotReview(review); err != nil {
			return fmt.Errorf("invalid screenshot reviews line %d: %w", lineNumber, err)
		}
		reviews = append(reviews, review)
		return nil
	}); err != nil {
		return nil, err
	}
	sort.SliceStable(reviews, func(left, right int) bool {
		return reviews[left].ReviewedAt.After(reviews[right].ReviewedAt)
	})
	return reviews, nil
}

// RecordScreenshotReview resolves a submission against the immutable capture
// queue and appends one audit record. Repeating the exact submission is safe;
// changing an existing verdict requires a new screenshot rather than history
// mutation.
func RecordScreenshotReview(root string, submission ScreenshotReviewSubmission) (ScreenshotReview, bool, error) {
	if err := validateScreenshotReviewSubmission(submission); err != nil {
		return ScreenshotReview{}, false, err
	}

	reviewWriteMutex.Lock()
	defer reviewWriteMutex.Unlock()

	artifacts, err := loadScreenshotQueue(filepath.Join(root, "review-queue.jsonl"))
	if err != nil {
		return ScreenshotReview{}, false, err
	}
	var matched []ScreenshotArtifact
	for _, artifact := range artifacts {
		if artifact.RunID == strings.TrimSpace(submission.RunID) && artifact.DeviceID == strings.TrimSpace(submission.DeviceID) {
			matched = append(matched, artifact)
		}
	}
	if len(matched) == 0 {
		return ScreenshotReview{}, false, fmt.Errorf("no queued screenshot for run %q and device %q", submission.RunID, submission.DeviceID)
	}
	if len(matched) > 1 {
		return ScreenshotReview{}, false, fmt.Errorf("multiple queued screenshots for run %q and device %q", submission.RunID, submission.DeviceID)
	}
	artifact := matched[0]
	key := screenshotReviewKey(artifact.RunID, artifact.DeviceID, artifact.Path)
	reviews, err := LoadScreenshotReviews(root)
	if err != nil {
		return ScreenshotReview{}, false, err
	}
	for _, review := range reviews {
		if screenshotReviewKey(review.RunID, review.DeviceID, review.Path) != key {
			continue
		}
		if sameScreenshotReview(review, submission) {
			return review, false, nil
		}
		return ScreenshotReview{}, false, fmt.Errorf("screenshot for run %q and device %q was already reviewed", submission.RunID, submission.DeviceID)
	}

	now := time.Now().UTC()
	reviewer := strings.TrimSpace(submission.Reviewer)
	if reviewer == "" {
		reviewer = "operator"
	}
	digest := sha256.Sum256([]byte(key + "\x00" + now.Format(time.RFC3339Nano)))
	review := ScreenshotReview{
		ID:         "review-" + hex.EncodeToString(digest[:8]),
		RunID:      artifact.RunID,
		DeviceID:   artifact.DeviceID,
		DeviceName: artifact.DeviceName,
		Path:       artifact.Path,
		ReviewedAt: now,
		Verdict:    strings.ToLower(strings.TrimSpace(submission.Verdict)),
		Summary:    strings.TrimSpace(submission.Summary),
		Reviewer:   reviewer,
	}
	if err := appendScreenshotReview(filepath.Join(root, "reviews.jsonl"), review); err != nil {
		return ScreenshotReview{}, false, err
	}
	return review, true, nil
}

func validateScreenshotReviewSubmission(submission ScreenshotReviewSubmission) error {
	if strings.TrimSpace(submission.RunID) == "" || strings.TrimSpace(submission.DeviceID) == "" {
		return errors.New("review requires runId and deviceId")
	}
	if err := validateScreenshotVerdict(submission.Verdict); err != nil {
		return err
	}
	summary := strings.TrimSpace(submission.Summary)
	if summary == "" {
		return errors.New("review summary is required")
	}
	if len(summary) > maximumReviewSummary {
		return fmt.Errorf("review summary cannot exceed %d bytes", maximumReviewSummary)
	}
	if len(strings.TrimSpace(submission.Reviewer)) > 200 {
		return errors.New("reviewer cannot exceed 200 bytes")
	}
	return nil
}

func validateScreenshotVerdict(verdict string) error {
	switch strings.ToLower(strings.TrimSpace(verdict)) {
	case ScreenshotVerdictHealthy, ScreenshotVerdictDegraded, ScreenshotVerdictFailed, ScreenshotVerdictUnknown:
		return nil
	default:
		return fmt.Errorf("unsupported review verdict %q; expected healthy, degraded, failed, or unknown", verdict)
	}
}

func validateStoredScreenshotReview(review ScreenshotReview) error {
	if review.ID == "" || review.RunID == "" || review.DeviceID == "" || review.Path == "" || review.ReviewedAt.IsZero() {
		return errors.New("stored review is missing identity, path, or timestamp")
	}
	if err := validateScreenshotVerdict(review.Verdict); err != nil {
		return err
	}
	if strings.TrimSpace(review.Summary) == "" || strings.TrimSpace(review.Reviewer) == "" {
		return errors.New("stored review requires summary and reviewer")
	}
	return nil
}

func sameScreenshotReview(review ScreenshotReview, submission ScreenshotReviewSubmission) bool {
	reviewer := strings.TrimSpace(submission.Reviewer)
	if reviewer == "" {
		reviewer = "operator"
	}
	return review.Verdict == strings.ToLower(strings.TrimSpace(submission.Verdict)) &&
		review.Summary == strings.TrimSpace(submission.Summary) && review.Reviewer == reviewer
}

func loadScreenshotQueue(path string) ([]ScreenshotArtifact, error) {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return []ScreenshotArtifact{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open screenshot review queue: %w", err)
	}
	defer file.Close()

	var artifacts []ScreenshotArtifact
	if err := scanJSONLines(file, maximumReviewLineBytes, func(line []byte, lineNumber int) error {
		var artifact ScreenshotArtifact
		if err := json.Unmarshal(line, &artifact); err != nil {
			return fmt.Errorf("decode screenshot review queue line %d: %w", lineNumber, err)
		}
		if artifact.RunID == "" || artifact.DeviceID == "" || artifact.Path == "" {
			return fmt.Errorf("invalid screenshot review queue line %d: missing runId, deviceId, or path", lineNumber)
		}
		artifacts = append(artifacts, artifact)
		return nil
	}); err != nil {
		return nil, err
	}
	return artifacts, nil
}

func scanJSONLines(file *os.File, maximumBytes int, consume func([]byte, int) error) error {
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 4096), maximumBytes)
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if err := consume([]byte(line), lineNumber); err != nil {
			return err
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read JSON lines: %w", err)
	}
	return nil
}

func appendScreenshotReview(path string, review ScreenshotReview) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	data, err := json.Marshal(review)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o640)
	if err != nil {
		return err
	}
	if _, err := file.Write(append(data, '\n')); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	return file.Close()
}

func screenshotReviewKey(runID, deviceID, path string) string {
	return runID + "\x00" + deviceID + "\x00" + filepath.Clean(path)
}
