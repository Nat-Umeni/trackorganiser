package downloader

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// searchResult is what the fake yt-dlp "finds" for one search: a title and channel,
// and the audio to write as the download.
type searchResult struct {
	title   string
	channel string
	fixture string
}

// audioFixture generates real audio of the given length with ffmpeg. The length
// is how a test tells afterwards which search's download was kept.
func audioFixture(t *testing.T, seconds int) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), fmt.Sprintf("%ds.mp3", seconds))
	cmd := exec.Command("ffmpeg", "-f", "lavfi", "-i", "anullsrc=r=44100:cl=mono",
		"-t", fmt.Sprint(seconds), "-y", path)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("ffmpeg could not generate test audio: %v\n%s", err, output)
	}

	return path
}

// fakeSearchingYtdlp writes a yt-dlp stand-in that answers each search
// differently. For a known query it copies that result's audio to the --output
// path and writes the info line where --print-to-file asks, exactly as the real
// one does after a download. Any other query finds nothing: exit 0, no file.
// Every search is appended to the returned calls file, so a test can check how
// many ran.
func fakeSearchingYtdlp(t *testing.T, results map[string]searchResult) (script, calls string) {
	t.Helper()

	if runtime.GOOS == "windows" {
		t.Skip("the yt-dlp stand-in relies on a shell script")
	}

	dir := t.TempDir()
	calls = filepath.Join(dir, "calls")

	var body strings.Builder
	body.WriteString(`#!/bin/sh
out=""; info=""; query=""
while [ $# -gt 0 ]; do
	case "$1" in
		--output) out="$2"; shift 2 ;;
		--print-to-file) info="$3"; shift 3 ;;
		ytsearch:*) query="${1#ytsearch:}"; shift ;;
		*) shift ;;
	esac
done
`)
	fmt.Fprintf(&body, "printf '%%s\\n' \"$query\" >> '%s'\n", calls)

	// The output template ends in ".%(ext)s", which yt-dlp replaces with mp3.
	body.WriteString(`dest="${out%.%(ext)s}.mp3"` + "\n")
	body.WriteString(`case "$query" in` + "\n")
	for query, result := range results {
		fmt.Fprintf(&body, "\t'%s') cp '%s' \"$dest\"; printf 'vid123\\t%%s\\t180\\t%%s\\n' '%s' '%s' > \"$info\" ;;\n",
			query, result.fixture, result.channel, result.title)
	}
	body.WriteString("esac\nexit 0\n")

	script = filepath.Join(dir, "yt-dlp-searching")
	if err := os.WriteFile(script, []byte(body.String()), 0755); err != nil {
		t.Fatalf("setting up: %v", err)
	}

	return script, calls
}

func countCalls(t *testing.T, calls string) int {
	t.Helper()

	contents, err := os.ReadFile(calls)
	if errors.Is(err, os.ErrNotExist) {
		return 0
	}
	if err != nil {
		t.Fatalf("reading the calls file: %v", err)
	}

	return len(strings.Split(strings.TrimSpace(string(contents)), "\n"))
}

// The real case from 2026-11-13, played through the retry: the "topic" search
// for Bluff's track returned an unrelated song, and the same search without
// "topic" found the right one.
func TestDownloadBestAudioRetries(t *testing.T) {
	// Wrong results are 2 seconds and right ones 3, so the length of the file
	// left behind says which search it came from.
	wrong := audioFixture(t, 2)
	right := audioFixture(t, 3)

	const (
		topicQuery    = "I Feel So Good Bluff topic"
		fallbackQuery = "I Feel So Good Bluff"
		secondArtist  = "I Feel So Good Someone Else"
	)

	tests := []struct {
		name      string
		results   map[string]searchResult
		fallbacks []string
		wantErr   error
		wantMS    int
		wantCalls int
	}{
		{
			// The usual case, and the one that must cost nothing extra.
			name:      "first search right",
			results:   map[string]searchResult{topicQuery: {"I Feel So Good", "Bluff - Topic", right}},
			fallbacks: []string{fallbackQuery},
			wantMS:    3000,
			wantCalls: 1,
		},
		{
			name: "first wrong, fallback right",
			results: map[string]searchResult{
				topicQuery:    {"Snow on The Bluff", "evening elephants", wrong},
				fallbackQuery: {"Bluff - I Feel So Good", "UKF", right},
			},
			fallbacks: []string{fallbackQuery},
			wantMS:    3000,
			wantCalls: 2,
		},
		{
			// Nothing better turned up, so the first search's result stays -
			// flagged, because it is probably still wrong.
			name: "every search wrong",
			results: map[string]searchResult{
				topicQuery:    {"Snow on The Bluff", "evening elephants", wrong},
				fallbackQuery: {"The Bluff", "Wiz Khalifa", right},
			},
			fallbacks: []string{fallbackQuery},
			wantErr:   ErrSongMismatch,
			wantMS:    2000,
			wantCalls: 2,
		},
		{
			// The fallbacks also give a track the first search couldn't find at
			// all a second chance.
			name:      "first finds nothing, fallback right",
			results:   map[string]searchResult{fallbackQuery: {"Bluff - I Feel So Good", "UKF", right}},
			fallbacks: []string{fallbackQuery},
			wantMS:    3000,
			wantCalls: 2,
		},
		{
			name: "second artist rescues it",
			results: map[string]searchResult{
				topicQuery:    {"Snow on The Bluff", "evening elephants", wrong},
				fallbackQuery: {"Call My Bluff", "JEV Lyrics", wrong},
				secondArtist:  {"Someone Else - I Feel So Good", "Someone Else", right},
			},
			fallbacks: []string{fallbackQuery, secondArtist},
			wantMS:    3000,
			wantCalls: 3,
		},
		{
			// The "123" case from a real run: the song's name is in the title,
			// but nobody credited on Spotify appears anywhere. The title check
			// alone passed it.
			name: "title right, artist wrong",
			results: map[string]searchResult{
				topicQuery:    {"I Feel So Good", "Some Other Band", wrong},
				fallbackQuery: {"Bluff - I Feel So Good", "UKF", right},
			},
			fallbacks: []string{fallbackQuery},
			wantMS:    3000,
			wantCalls: 2,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			script, calls := fakeSearchingYtdlp(t, test.results)
			outputDir := t.TempDir()
			dl := &AudioDownloader{OutputDir: outputDir, YtdlpPath: script}

			err := dl.DownloadBestAudio(Request{
				Query:           topicQuery,
				FallbackQueries: test.fallbacks,
				Artists:         []string{"Bluff", "Someone Else"},
				FileName:        "I Feel So Good - Bluff",
				Tags:            Tags{Title: "I Feel So Good", Artist: "Bluff, Someone Else"},
			})

			switch {
			case test.wantErr == nil && err != nil:
				t.Fatalf("unexpected error: %v", err)
			case test.wantErr != nil && !errors.Is(err, test.wantErr):
				t.Fatalf("error = %v, want %v", err, test.wantErr)
			}

			finalPath := filepath.Join(outputDir, "I Feel So Good - Bluff.mp3")
			gotMS, probeErr := probeDurationMS(finalPath)
			if probeErr != nil {
				t.Fatalf("the kept file can't be read: %v", probeErr)
			}

			// mp3 frames pad the length slightly, so within a tenth of the gap
			// between the two fixtures.
			if diff := gotMS - test.wantMS; diff < -300 || diff > 300 {
				t.Errorf("kept a %dms file, want the %dms one", gotMS, test.wantMS)
			}

			// Every retry that wasn't kept must be gone, or the next run's scan
			// would count "<name>.try2.mp3" as a track.
			entries, readErr := os.ReadDir(outputDir)
			if readErr != nil {
				t.Fatalf("listing the output: %v", readErr)
			}
			if len(entries) != 1 {
				var names []string
				for _, entry := range entries {
					names = append(names, entry.Name())
				}
				t.Errorf("output holds %q, want only the kept mp3", names)
			}

			if got := countCalls(t, calls); got != test.wantCalls {
				t.Errorf("yt-dlp ran %d time(s), want %d", got, test.wantCalls)
			}
		})
	}
}

func TestDownloadBestAudioNamesBothTitlesOnASongMismatch(t *testing.T) {
	script, _ := fakeSearchingYtdlp(t, map[string]searchResult{
		"I Feel So Good Bluff topic": {"Snow on The Bluff", "evening elephants", audioFixture(t, 2)},
	})
	dl := &AudioDownloader{OutputDir: t.TempDir(), YtdlpPath: script}

	err := dl.DownloadBestAudio(Request{
		Query:    "I Feel So Good Bluff topic",
		FileName: "I Feel So Good - Bluff",
		Tags:     Tags{Title: "I Feel So Good"},
	})

	if !errors.Is(err, ErrSongMismatch) {
		t.Fatalf("error = %v, want ErrSongMismatch", err)
	}

	// This line lands in the end-of-run list, so it has to say what was asked
	// for and what arrived - otherwise there is nothing to go and check by ear.
	for _, want := range []string{"I Feel So Good", "Snow on The Bluff"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q doesn't mention %q", err, want)
		}
	}
}
