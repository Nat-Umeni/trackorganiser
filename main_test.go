package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/Nat-Umeni/trackorganiser/config"
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
	// The default now reads the saved folder first, so the config has to be
	// faked too - otherwise a folder saved by a real run would leak in and this
	// test would fail on any machine where XDG_CONFIG_HOME is set. Config
	// first, home second: both set HOME, and the later one wins.
	fakeConfigDir(t)
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

func TestDefaultDownloadDirPrefersTheSavedOne(t *testing.T) {
	fakeConfigDir(t)
	fakeHome(t)

	saved := t.TempDir()
	if err := config.SaveDownloadDir(saved); err != nil {
		t.Fatalf("setting up: %v", err)
	}

	got, err := defaultDownloadDir()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != saved {
		t.Errorf("defaultDownloadDir() = %q, want the saved %q", got, saved)
	}
}

func TestDefaultDownloadDirFallsBackToDownloads(t *testing.T) {
	fakeConfigDir(t)
	home := fakeHome(t)

	got, err := defaultDownloadDir()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := filepath.Join(home, "Downloads"); got != want {
		t.Errorf("defaultDownloadDir() = %q, want %q the first time", got, want)
	}
}

func TestEnsureOutputLocationExistsUsesTheSavedDefault(t *testing.T) {
	fakeConfigDir(t)
	fakeHome(t)

	saved := t.TempDir()
	if err := config.SaveDownloadDir(saved); err != nil {
		t.Fatalf("setting up: %v", err)
	}

	// Pressing enter at the prompt.
	got, err := ensureOutputLocationExists("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != saved {
		t.Errorf("got %q, want the saved folder %q", got, saved)
	}
}

func TestChooseDownloadDirRemembersIt(t *testing.T) {
	fakeConfigDir(t)
	fakeHome(t)

	folder := t.TempDir()
	got, err := chooseDownloadDir(folder, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != folder {
		t.Errorf("got %q, want %q", got, folder)
	}

	if saved := config.LoadDownloadDir(); saved != folder {
		t.Errorf("saved %q, want %q remembered for next run", saved, folder)
	}
}

// What's remembered is the resolved path, not what was typed - so the next
// run's default is exact even if "~" meant something else by then.
func TestChooseDownloadDirSavesTheResolvedPath(t *testing.T) {
	fakeConfigDir(t)
	home := fakeHome(t)

	music := filepath.Join(home, "Music")
	if err := os.Mkdir(music, 0755); err != nil {
		t.Fatalf("setting up: %v", err)
	}

	if _, err := chooseDownloadDir("~/Music", true); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if saved := config.LoadDownloadDir(); saved != music {
		t.Errorf("saved %q, want the resolved %q", saved, music)
	}
}

// Saying no to creating a folder means it wasn't chosen, so last run's choice
// stands.
func TestChooseDownloadDirKeepsTheOldOneOnDecline(t *testing.T) {
	fakeConfigDir(t)
	fakeHome(t)

	previous := t.TempDir()
	if err := config.SaveDownloadDir(previous); err != nil {
		t.Fatalf("setting up: %v", err)
	}

	feedStdin(t, "n\n")
	_, err := chooseDownloadDir(filepath.Join(t.TempDir(), "does-not-exist"), true)
	if !errors.Is(err, ErrDownloadDirDeclined) {
		t.Fatalf("error = %v, want ErrDownloadDirDeclined", err)
	}

	if saved := config.LoadDownloadDir(); saved != previous {
		t.Errorf("saved %q after a decline, want the previous %q kept", saved, previous)
	}
}

// A dry run is for seeing what would happen, so a folder typed into one
// shouldn't become the default.
func TestChooseDownloadDirOnADryRunRemembersNothing(t *testing.T) {
	fakeConfigDir(t)
	fakeHome(t)

	if _, err := chooseDownloadDir(t.TempDir(), false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if saved := config.LoadDownloadDir(); saved != "" {
		t.Errorf("a dry run saved %q, want nothing saved", saved)
	}
}

func TestDownloadDirPromptShowsTheDefault(t *testing.T) {
	fakeConfigDir(t)
	fakeHome(t)

	saved := t.TempDir()
	if err := config.SaveDownloadDir(saved); err != nil {
		t.Fatalf("setting up: %v", err)
	}

	// Otherwise pressing enter would put the downloads somewhere the prompt
	// never mentioned.
	if prompt := downloadDirPrompt(); !strings.Contains(prompt, saved) {
		t.Errorf("prompt %q doesn't name the default folder %q", prompt, saved)
	}
}

func TestEnsureOutputLocationExistsTypedBeatsSaved(t *testing.T) {
	fakeConfigDir(t)
	fakeHome(t)

	if err := config.SaveDownloadDir(t.TempDir()); err != nil {
		t.Fatalf("setting up: %v", err)
	}

	typed := t.TempDir()
	got, err := ensureOutputLocationExists(typed)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != typed {
		t.Errorf("got %q, want the typed folder %q", got, typed)
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

func TestReportBucket(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bucket
	}{
		{name: "downloaded fine", err: nil, want: bucketOK},

		// A file was saved but may not be the right cut - worth a listen.
		// Length on its own is never grounds for anything harsher.
		{name: "wrong length", err: fmt.Errorf("%w: expected 3m, got 4m", downloader.ErrDurationMismatch), want: bucketListen},
		{name: "wrong version", err: fmt.Errorf("%w: wanted x, got y", downloader.ErrVersionMismatch), want: bucketListen},
		{name: "artist unconfirmed", err: fmt.Errorf("%w: kept it", downloader.ErrArtistUnconfirmed), want: bucketListen},

		// Nothing was saved, so there is nothing to listen to - the run says it
		// couldn't get them, and the next run tries again.
		{name: "wrong song, not saved", err: fmt.Errorf("%w: nearest was x", downloader.ErrSongMismatch), want: bucketNotFound},
		{name: "no results at all", err: fmt.Errorf("%w for %q", downloader.ErrNoResults, "query"), want: bucketNotFound},

		// yt-dlp itself breaking is neither - it's printed as a failure.
		{name: "yt-dlp failed", err: errors.New("yt-dlp failed: exit status 1"), want: bucketFailed},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := reportBucket(test.err); got != test.want {
				t.Errorf("reportBucket(%v) = %v, want %v", test.err, got, test.want)
			}
		})
	}
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
	mismatches, _ := downloadTracks(dl, tracks, 8)

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

	// Nothing is pre-placed, so every track finds no YouTube results. That is
	// "not found", listed so the run says what it couldn't get - not a
	// mismatch, since there is no file to listen to.
	mismatches, notFound := downloadTracks(dl, tracks, 1)
	if len(mismatches) != 0 {
		t.Errorf("got %v, want no mismatches when nothing downloaded", mismatches)
	}
	if len(notFound) != len(tracks) {
		t.Errorf("got %d not found, want all %d: %v", len(notFound), len(tracks), notFound)
	}
}

func TestDownloadTracksWithNoTracks(t *testing.T) {
	dl, err := downloader.NewAudioDownloader(t.TempDir(), fakeYtdlp(t))
	if err != nil {
		t.Skipf("downloader unavailable on this machine: %v", err)
	}

	if mismatches, notFound := downloadTracks(dl, nil, 4); len(mismatches)+len(notFound) != 0 {
		t.Errorf("got %v and %v, want nothing back for an empty track list", mismatches, notFound)
	}
}

// fakeConfigDir points os.UserConfigDir at a temp directory so tests never touch
// the real one.
//
// fakeHome alone is not enough: on Linux os.UserConfigDir checks XDG_CONFIG_HOME
// *before* falling back to $HOME/.config, so a machine with it set would write
// into my actual config.
func fakeConfigDir(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)  // linux
	t.Setenv("HOME", dir)             // macos falls back through HOME
	t.Setenv("AppData", dir)          // windows
	t.Setenv("USERPROFILE", dir)      // windows fallback
	t.Setenv("SPOTIFY_CLIENT_ID", "") // so a real env var cannot leak in

	return dir
}

func TestResolveClientIDPrefersTheFlag(t *testing.T) {
	fakeConfigDir(t)

	if err := config.SaveClientID("saved-one"); err != nil {
		t.Fatalf("setting up: %v", err)
	}
	t.Setenv("SPOTIFY_CLIENT_ID", "env-one")

	// The flag has to win, or a wrong saved value could never be corrected
	// without hand-editing the config file - which is the thing this whole
	// change exists to avoid.
	if got := resolveClientID("flag-one"); got != "flag-one" {
		t.Errorf("resolveClientID() = %q, want the flag value", got)
	}

	// And it replaces what was stored, so the next run agrees.
	if got := config.LoadClientID(); got != "flag-one" {
		t.Errorf("saved value = %q, want the flag to have overwritten it", got)
	}
}

func TestResolveClientIDFallsBackInOrder(t *testing.T) {
	tests := []struct {
		name  string
		flag  string
		saved string
		env   string
		want  string
	}{
		{name: "flag only", flag: "from-flag", want: "from-flag"},
		{name: "saved only", saved: "from-config", want: "from-config"},
		{name: "env only", env: "from-env", want: "from-env"},

		// Saved beats the environment, so someone who once used a .env and has
		// since passed the flag does not silently revert.
		{name: "saved beats env", saved: "from-config", env: "from-env", want: "from-config"},

		{name: "nothing at all", want: ""},

		// Whitespace is what a copy-paste from a dashboard actually looks like,
		// and a trailing space gives an opaque Spotify rejection.
		{name: "flag is trimmed", flag: "  padded  ", want: "padded"},
		{name: "env is trimmed", env: "\tpadded\n", want: "padded"},
		{name: "flag of only spaces is treated as absent", flag: "   ", saved: "from-config", want: "from-config"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fakeConfigDir(t)

			if test.saved != "" {
				if err := config.SaveClientID(test.saved); err != nil {
					t.Fatalf("setting up: %v", err)
				}
			}
			if test.env != "" {
				t.Setenv("SPOTIFY_CLIENT_ID", test.env)
			}

			if got := resolveClientID(test.flag); got != test.want {
				t.Errorf("resolveClientID(%q) = %q, want %q", test.flag, got, test.want)
			}
		})
	}
}

func TestClientIDHelpNamesTheCommand(t *testing.T) {
	fakeConfigDir(t)

	help := clientIDHelp()

	// Whoever reads this has been handed a binary and an ID and nothing else,
	// so the exact flag has to appear or the message is no better than the bare
	// "SPOTIFY_CLIENT_ID not set" it replaced.
	for _, want := range []string{"-client-id", "client ID", "once"} {
		if !strings.Contains(help, want) {
			t.Errorf("the help text does not mention %q:\n%s", want, help)
		}
	}

	// It must not tell someone to go and make a Spotify app.
	for _, unwanted := range []string{"dashboard", "developer.spotify.com", "create an app"} {
		if strings.Contains(help, unwanted) {
			t.Errorf("the help text mentions %q, but users are not creating apps", unwanted)
		}
	}
}

func TestWriteUsageHidesThePortFlag(t *testing.T) {
	// The port only works against a redirect URI registered in the Spotify
	// dashboard, so advertising it would mostly produce confusing failures.
	// This test is what keeps it hidden if anyone touches flag.Usage later.
	set := flag.NewFlagSet("trackorganiser", flag.ContinueOnError)
	set.Int("jobs", 4, "how many tracks at once")
	set.Bool("dry-run", false, "list filenames only")
	set.String("client-id", "", "your Spotify client ID")
	set.Int("port", 8080, "")

	var out strings.Builder
	set.SetOutput(&out)
	writeUsage(set)

	if strings.Contains(out.String(), "-port") {
		t.Errorf("-port appears in the usage output:\n%s", out.String())
	}

	// The others must still be listed, or "hidden" has become "broken".
	for _, want := range []string{"-jobs", "-dry-run", "-client-id"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("%s is missing from the usage output:\n%s", want, out.String())
		}
	}
}

func TestShouldOfferUpdate(t *testing.T) {
	tests := []struct {
		name     string
		current  string
		latest   string
		declined string
		want     bool
	}{
		{name: "newer, never declined", current: "v0.1.0", latest: "v0.2.0", want: true},
		{name: "already on the latest", current: "v0.2.0", latest: "v0.2.0", want: false},

		// The whole point of DeclinedVersion: no means no for that version...
		{name: "that version was declined", current: "v0.1.0", latest: "v0.2.0", declined: "v0.2.0", want: false},

		// ...but not for the one after it.
		{name: "a newer one than was declined", current: "v0.1.0", latest: "v0.3.0", declined: "v0.2.0", want: true},

		{name: "dev build", current: "dev", latest: "v0.2.0", want: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := shouldOfferUpdate(test.current, test.latest, test.declined)
			if got != test.want {
				t.Errorf("shouldOfferUpdate(%q, %q, %q) = %v, want %v",
					test.current, test.latest, test.declined, got, test.want)
			}
		})
	}
}

// Tests build with version = "dev", which is exactly the case that must not
// reach the network - otherwise every `go run .` while developing would ask
// GitHub, and offer to overwrite the build with a release.
func TestCheckForUpdateSkipsDevBuilds(t *testing.T) {
	if version != "dev" {
		t.Skipf("built with version %q, this test needs the dev default", version)
	}

	var updated bool
	output := captureStdout(t, func() { updated = checkForUpdate() })

	if updated {
		t.Error("checkForUpdate reported an update on a dev build")
	}
	if output != "" {
		t.Errorf("a dev build printed something, so it got past the release check:\n%s", output)
	}
}

// captureStdout runs fn with os.Stdout redirected, returning what was written.
// The same shape as the helper in spotify/auth_test.go - a separate copy because
// it is a different package.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()

	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("setting up: %v", err)
	}

	previous := os.Stdout
	os.Stdout = writer
	fn()
	os.Stdout = previous
	writer.Close()

	var captured strings.Builder
	if _, err := io.Copy(&captured, reader); err != nil {
		t.Fatalf("reading captured output: %v", err)
	}

	return captured.String()
}

func TestReportYtdlpExtrasAlwaysSaysSomething(t *testing.T) {
	// Both extras are survivable when missing, which is exactly why they get
	// announced: a run quietly lacking either looks like unrelated download
	// failures later. Silence is the one unacceptable outcome, whatever this
	// machine happens to have installed.
	output := captureStdout(t, reportYtdlpExtras)

	mentionsRuntime := strings.Contains(output, "for YouTube extraction") ||
		strings.Contains(output, "No JavaScript runtime found")
	if !mentionsRuntime {
		t.Errorf("nothing said about the JavaScript runtime:\n%s", output)
	}

	mentionsCookies := strings.Contains(output, "Firefox cookies") ||
		strings.Contains(output, "No Firefox cookies")
	if !mentionsCookies {
		t.Errorf("nothing said about cookies:\n%s", output)
	}
}

func TestReportYtdlpExtrasWithNeitherAvailable(t *testing.T) {
	// The case that matters: an empty PATH and no home directory means no
	// runtime and no cookie store, and the warnings have to name what is
	// missing rather than just going quiet.
	t.Setenv("PATH", t.TempDir())
	fakeHome(t)

	output := captureStdout(t, reportYtdlpExtras)

	if !strings.Contains(output, "No JavaScript runtime found") {
		t.Errorf("no warning about the missing runtime:\n%s", output)
	}
	if !strings.Contains(output, "No Firefox cookies") {
		t.Errorf("no warning about missing cookies:\n%s", output)
	}

	// The warning has to name the runtimes, or someone reading it cannot act
	// on it.
	for _, runtimeName := range []string{"deno", "node", "quickjs", "bun"} {
		if !strings.Contains(output, runtimeName) {
			t.Errorf("the warning does not mention %q:\n%s", runtimeName, output)
		}
	}
}

func TestClientIDHelpIsCopyPasteable(t *testing.T) {
	fakeConfigDir(t)

	help := clientIDHelp()

	// Without a leading ./ PowerShell refuses to run a binary in the current
	// directory, and so do sh and bash. This message exists to unblock someone
	// who has been handed a binary and nothing else, so the command in it has to
	// work as printed.
	prefix := "." + string(os.PathSeparator)
	if !strings.Contains(help, prefix) {
		t.Errorf("the command has no %q prefix, so it won't run as printed:\n%s", prefix, help)
	}

	// And it must name the binary as actually invoked, not a hardcoded name -
	// the README tells people to rename it.
	if !strings.Contains(help, filepath.Base(os.Args[0])) {
		t.Errorf("the command does not name this binary:\n%s", help)
	}
}
