package downloader

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// durationToleranceMS is how far a downloaded file may sit from Spotify's length
// before it is worth a second look. A few seconds covers encoding differences and
// trimmed silence; anything more usually means a different upload - a music video
// with an intro, an extended mix, or the wrong song entirely.
const durationToleranceMS = 5000

// probeDurationMS reads the real length of an audio file with ffprobe, which is
// already a hard dependency via ffmpeg.
func probeDurationMS(path string) (int, error) {
	cmd := exec.Command(
		"ffprobe",
		"-v", "error",
		"-show_entries", "format=duration",
		"-of", "csv=p=0",
		path,
	)

	output, err := cmd.Output()
	if err != nil {
		return 0, fmt.Errorf("ffprobe %q: %w", path, err)
	}

	seconds, err := strconv.ParseFloat(strings.TrimSpace(string(output)), 64)
	if err != nil {
		// Include the raw output - if ffprobe ever changes format, the error
		// should show what it actually printed rather than just "invalid syntax".
		return 0, fmt.Errorf("ffprobe %q returned %q: %w", path, string(output), err)
	}

	return int(seconds * 1000), nil
}

// durationsMatch reports whether a downloaded file is close enough to the length
// Spotify advertised.
func durationsMatch(expectedMS, actualMS int) bool {
	// Spotify not supplying a duration is not a mismatch, and should not flag
	// every track on the run.
	if expectedMS <= 0 {
		return true
	}

	difference := expectedMS - actualMS
	if difference < 0 {
		difference = -difference
	}

	return difference <= durationToleranceMS
}
