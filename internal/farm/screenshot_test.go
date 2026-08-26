package farm

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func TestAnalyzeScreenshotDetectsBlankAndUnchangedFrames(t *testing.T) {
	directory := t.TempDir()
	firstPath := filepath.Join(directory, "first.png")
	secondPath := filepath.Join(directory, "second.png")
	writeTestImage(t, firstPath, color.RGBA{R: 20, G: 20, B: 20, A: 255})
	writeTestImage(t, secondPath, color.RGBA{R: 20, G: 20, B: 20, A: 255})

	artifact := ScreenshotArtifact{}
	if err := analyzeScreenshot(secondPath, firstPath, &artifact); err != nil {
		t.Fatal(err)
	}
	if artifact.Width != 8 || artifact.Height != 6 || !artifact.LooksBlank {
		t.Fatalf("unexpected image analysis: %#v", artifact)
	}
	if artifact.ChangedPixelsPercent != 0 || artifact.PreviousPath != firstPath {
		t.Fatalf("expected identical frames: %#v", artifact)
	}
}

func TestChangedPixelsPercentDetectsVisualChange(t *testing.T) {
	first := image.NewRGBA(image.Rect(0, 0, 10, 10))
	second := image.NewRGBA(image.Rect(0, 0, 10, 10))
	for y := 0; y < 10; y++ {
		for x := 0; x < 10; x++ {
			first.Set(x, y, color.Black)
			second.Set(x, y, color.White)
		}
	}
	if changed := changedPixelsPercent(first, second); changed != 100 {
		t.Fatalf("expected 100%% change, got %f", changed)
	}
}

func writeTestImage(t *testing.T, path string, fill color.Color) {
	t.Helper()
	value := image.NewRGBA(image.Rect(0, 0, 8, 6))
	for y := 0; y < 6; y++ {
		for x := 0; x < 8; x++ {
			value.Set(x, y, fill)
		}
	}
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(file, value); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}
