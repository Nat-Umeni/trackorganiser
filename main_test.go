package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/Nat-Umeni/trackorganiser/downloader"
	"github.com/Nat-Umeni/trackorganiser/spotify"
)

// feedStdin replaces the package level scanner for the duration of one test so
// prompts can be answered without a terminal.
func feedStdin(t *testing.T, input string) {
	t.Helper()
	previous := stdin
	stdin = bufio.NewScanner(strings.NewReader(input))
	t.Cleanup(func() { stdin = previous })
}

func TestParsePlaylistSelection(t *testing.T) {
	const max = 10

	tests := []struct {
		name    string
		input   string
		want    []int
		wantErr bool
	}{
		{name: "single numbers", input: "1,3,5", want: []int{0, 2, 4}},
		{name: "spaces are trimmed", input: "1, 3, 5", want: []int{0, 2, 4}},
		{name: "range", input: "4-7", want: []int{3, 4, 5, 6}},
		{name: "range and single mixed", input: "2-4,1", want: []int{1, 2, 3, 0}},
		{name: "single element range", input: "3-3", want: []int{2}},
		{name: "duplicates collapse", input: "1,1,1", want: []int{0}},
		{name: "overlapping ranges collapse", input: "1-3,2-4", want: []int{0, 1, 2, 3}},
		{name: "trailing comma ignored", input: "1,2,", want: []int{0, 1}},
		{name: "whole range", input: "1-10", want: []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9}},
		{name: "empty input selects nothing", input: "", want: nil},

		{name: "backwards range", input: "7-4", wantErr: true},
		{name: "not a number", input: "banana", wantErr: true},
		{name: "below range", input: "0", wantErr: true},
		{name: "above range", input: "99", wantErr: true},
		{name: "open ended range", input: "4-", wantErr: true},
		{name: "range end out of bounds", input: "8-12", wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := parsePlaylistSelection(test.input, max)

			if test.wantErr {
				if err == nil {
					t.Fatalf("parsePlaylistSelection(%q) = %v, want an error", test.input, got)
				}
				return
			}

			if err != nil {
				t.Fatalf("parsePlaylistSelection(%q) returned unexpected error: %v", test.input, err)
			}
			if !slices.Equal(got, test.want) {
				t.Errorf("parsePlaylistSelection(%q) = %v, want %v", test.input, got, test.want)
			}
		})
	}
}

func TestEnsureOutputLocationExistsUsesExistingDirectory(t *testing.T) {
	dir := t.TempDir()

	got, err := ensureOutputLocationExists(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != dir {
		t.Errorf("got %q, want %q", got, dir)
	}
}

func TestEnsureOutputLocationExistsMakesPathAbsolute(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	if err := os.Mkdir("downloads", 0755); err != nil {
		t.Fatalf("setting up: %v", err)
	}

	got, err := ensureOutputLocationExists("downloads")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !filepath.IsAbs(got) {
		t.Errorf("got %q, want an absolute path", got)
	}
}

func TestEnsureOutputLocationExistsCreatesWhenAccepted(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "DJSets", "nested")

	for _, answer := range []string{"y\n", "Y\n", "yes\n", "YES\n"} {
		t.Run(strings.TrimSpace(answer), func(t *testing.T) {
			os.RemoveAll(missing)
			feedStdin(t, answer)

			got, err := ensureOutputLocationExists(missing)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != missing {
				t.Errorf("got %q, want %q", got, missing)
			}

			info, err := os.Stat(missing)
			if err != nil {
				t.Fatalf("directory was not created: %v", err)
			}
			if !info.IsDir() {
				t.Errorf("%q was created but is not a directory", missing)
			}
		})
	}
}

func TestEnsureOutputLocationExistsDeclined(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nope")

	for _, answer := range []string{"n\n", "N\n", "no\n", "\n", "anything else\n"} {
		t.Run(strings.TrimSpace(answer), func(t *testing.T) {
			feedStdin(t, answer)

			got, err := ensureOutputLocationExists(missing)
			if !errors.Is(err, ErrDownloadDirDeclined) {
				t.Fatalf("got (%q, %v), want ErrDownloadDirDeclined", got, err)
			}
			if _, statErr := os.Stat(missing); statErr == nil {
				t.Errorf("%q was created despite being declined", missing)
			}
		})
	}
}

func TestEnsureOutputLocationExistsRejectsFile(t *testing.T) {
	file := filepath.Join(t.TempDir(), "mix.mp3")
	if err := os.WriteFile(file, []byte("not a directory"), 0644); err != nil {
		t.Fatalf("setting up: %v", err)
	}

	got, err := ensureOutputLocationExists(file)
	if err == nil {
		t.Fatalf("got (%q, nil), want an error", got)
	}
	if !strings.Contains(err.Error(), "not a directory") {
		t.Errorf("error %q does not explain that the path is a file", err)
	}
}

// fakeHome points os.UserHomeDir at a temporary directory so tests can exercise
// home-relative paths without touching the real one.
func fakeHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)        // unix
	t.Setenv("USERPROFILE", home) // windows
	return home
}

func TestEnsureOutputLocationExistsDefaultsToHomeDownloads(t *testing.T) {
	home := fakeHome(t)
	want := filepath.Join(home, "Downloads")
	if err := os.Mkdir(want, 0755); err != nil {
		t.Fatalf("setting up: %v", err)
	}

	got, err := ensureOutputLocationExists("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestEnsureOutputLocationExistsExpandsTilde(t *testing.T) {
	t.Run("tilde alone is the home directory", func(t *testing.T) {
		home := fakeHome(t)

		got, err := ensureOutputLocationExists("~")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != home {
			t.Errorf("got %q, want %q", got, home)
		}
	})

	t.Run("tilde slash resolves under home", func(t *testing.T) {
		home := fakeHome(t)
		feedStdin(t, "y\n")

		got, err := ensureOutputLocationExists("~/Music/dj")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := filepath.Join(home, "Music", "dj")
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
		if info, err := os.Stat(want); err != nil || !info.IsDir() {
			t.Errorf("%q was not created as a directory: %v", want, err)
		}
	})

	t.Run("another user's home is left alone", func(t *testing.T) {
		home := fakeHome(t)
		workDir := t.TempDir()
		t.Chdir(workDir)

		// Created so the path exists and no prompt is needed; the point is only
		// where it resolves to.
		if err := os.Mkdir("~otheruser", 0755); err != nil {
			t.Fatalf("setting up: %v", err)
		}

		got, err := ensureOutputLocationExists("~otheruser")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if strings.HasPrefix(got, home) {
			t.Errorf("got %q, which was wrongly expanded under home %q", got, home)
		}
		if !strings.HasSuffix(got, string(filepath.Separator)+"~otheruser") {
			t.Errorf("got %q, want a path ending in ~otheruser", got)
		}
	})
}

func TestClampJobs(t *testing.T) {
	tests := []struct {
		name string
		jobs int
		want int
	}{
		{name: "the default", jobs: 4, want: 4},
		{name: "one is allowed", jobs: 1, want: 1},
		{name: "the ceiling is allowed", jobs: maxJobs, want: maxJobs},
		{name: "just under the ceiling", jobs: maxJobs - 1, want: maxJobs - 1},

		// A typo must not fire hundreds of requests at YouTube.
		{name: "above the ceiling", jobs: maxJobs + 1, want: maxJobs},
		{name: "wildly above the ceiling", jobs: 5000, want: maxJobs},

		// Zero would mean a semaphore with no slots, which deadlocks on the
		// first send. Negative would panic in make().
		{name: "zero", jobs: 0, want: 1},
		{name: "negative", jobs: -3, want: 1},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := clampJobs(test.jobs); got != test.want {
				t.Errorf("clampJobs(%d) = %d, want %d", test.jobs, got, test.want)
			}
		})
	}
}

// fakeYtdlp writes a stand-in for yt-dlp that exits 0 without downloading, the
// way the real one behaves when a search finds nothing.
func fakeYtdlp(t *testing.T) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "yt-dlp-stub")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatalf("setting up: %v", err)
	}

	return path
}

// silentTrack generates a two second mp3 with ffmpeg, which is already a hard
// dependency. Real audio is needed because the duration check runs ffprobe over
// it - arbitrary bytes would fail to probe and never reach the mismatch path.
func silentTrack(t *testing.T) []byte {
	t.Helper()

	path := filepath.Join(t.TempDir(), "silence.mp3")
	cmd := exec.Command("ffmpeg", "-f", "lavfi", "-i", "anullsrc=r=44100:cl=mono", "-t", "2", "-y", path)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("ffmpeg could not generate test audio: %v\n%s", err, output)
	}

	audio, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("setting up: %v", err)
	}

	return audio
}

func TestDownloadTracksReportsEveryMismatch(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the yt-dlp stand-in relies on a shell script")
	}

	const trackCount = 20
	outputDir := t.TempDir()
	audio := silentTrack(t)

	// Pre-place the audio where each download would land. The stand-in yt-dlp
	// writes nothing, so this is what lets DownloadBestAudio get past its
	// os.Stat check and reach the tagging and duration steps.
	tracks := make([]spotify.Track, 0, trackCount)
	for index := range trackCount {
		track := spotify.Track{
			ID:      fmt.Sprintf("id%d", index),
			Name:    fmt.Sprintf("Track %d", index),
			Artists: []spotify.Artist{{Name: "Someone"}},
			// Three minutes against two seconds of audio, so every track is
			// well outside the tolerance and takes the locked append path.
			DurationMS: 180000,
		}
		tracks = append(tracks, track)

		path := filepath.Join(outputDir, track.FileName()+".mp3")
		if err := os.WriteFile(path, audio, 0644); err != nil {
			t.Fatalf("setting up: %v", err)
		}
	}

	dl, err := downloader.NewAudioDownloader(outputDir, fakeYtdlp(t))
	if err != nil {
		t.Skipf("downloader unavailable on this machine: %v", err)
	}

	// Deliberately more workers than the semaphore would need, to get real
	// contention on the shared slice. Run under -race to make this meaningful.
	mismatches := downloadTracks(dl, tracks, 8)

	// The point of the test: a lost append under concurrency shows up here as a
	// short list, and nowhere else.
	if len(mismatches) != trackCount {
		t.Errorf("got %d mismatch(es), want %d - an append was lost", len(mismatches), trackCount)
	}
}

func TestDownloadTracksWithOneWorker(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the yt-dlp stand-in relies on a shell script")
	}

	// A semaphore off by one would deadlock here rather than anywhere else, so
	// the single worker case is worth its own test.
	tracks := []spotify.Track{
		{ID: "a", Name: "First", Artists: []spotify.Artist{{Name: "Someone"}}},
		{ID: "b", Name: "Second", Artists: []spotify.Artist{{Name: "Someone"}}},
	}

	dl, err := downloader.NewAudioDownloader(t.TempDir(), fakeYtdlp(t))
	if err != nil {
		t.Skipf("downloader unavailable on this machine: %v", err)
	}

	// Nothing is pre-placed, so every track fails with "no YouTube results" and
	// none of them count as a mismatch.
	if mismatches := downloadTracks(dl, tracks, 1); len(mismatches) != 0 {
		t.Errorf("got %v, want no mismatches when nothing downloaded", mismatches)
	}
}

func TestDownloadTracksWithNoTracks(t *testing.T) {
	dl, err := downloader.NewAudioDownloader(t.TempDir(), fakeYtdlp(t))
	if err != nil {
		t.Skipf("downloader unavailable on this machine: %v", err)
	}

	if mismatches := downloadTracks(dl, nil, 4); len(mismatches) != 0 {
		t.Errorf("got %v, want nothing back for an empty track list", mismatches)
	}
}
