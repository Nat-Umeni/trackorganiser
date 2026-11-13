package downloader

import (
	"errors"
	"fmt"
	"hash/fnv"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// searchResult is one video the fake yt-dlp "finds": its title and channel, the
// audio to write if it is downloaded, and the length a listing claims for it.
// The claimed length is separate from the audio's real length so a test can
// make a listing lie, which the real one occasionally does.
type searchResult struct {
	title   string
	channel string
	fixture string
	seconds int
}

// resultID gives each fake result a stable video ID, so a later "download this
// exact video" call can be matched back to it.
func resultID(query string, index int) string {
	hash := fnv.New32a()
	hash.Write([]byte(query))
	return fmt.Sprintf("v%x%d", hash.Sum32(), index)
}

// audioFixture generates real audio of the given length with ffmpeg. The length
// is how a test tells afterwards which download was kept.
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

// fakeSearchingYtdlp writes a yt-dlp stand-in that knows a list of results per
// search, and answers the three kinds of call the downloader makes, matched on
// the exact target - always the last argument:
//
//	ytsearch:<query>       download result 1: copy its audio to --output and
//	                       write its info line where --print-to-file asks
//	ytsearch5:<query>      list every result's info line on stdout, as
//	                       --flat-playlist does, downloading nothing
//	https://...watch?v=ID  download that one result
//
// Anything else finds nothing: exit 0, no file - the real yt-dlp's behaviour for
// a search with no results. Every call's target is appended to the returned
// calls file, so a test can check exactly what ran.
func fakeSearchingYtdlp(t *testing.T, results map[string][]searchResult) (script, calls string) {
	t.Helper()

	if runtime.GOOS == "windows" {
		t.Skip("the yt-dlp stand-in relies on a shell script")
	}

	dir := t.TempDir()
	calls = filepath.Join(dir, "calls")

	var body strings.Builder
	body.WriteString(`#!/bin/sh
for target; do :; done
out=""; info=""
while [ $# -gt 0 ]; do
	case "$1" in
		--output) out="$2"; shift 2 ;;
		--print-to-file) info="$3"; shift 3 ;;
		*) shift ;;
	esac
done
`)
	fmt.Fprintf(&body, "printf '%%s\\n' \"$target\" >> '%s'\n", calls)

	// The output template ends in ".%(ext)s", which yt-dlp replaces with mp3.
	body.WriteString(`dest="${out%.%(ext)s}.mp3"` + "\n")
	body.WriteString(`case "$target" in` + "\n")

	infoLine := func(query string, index int, result searchResult) string {
		return fmt.Sprintf("%s\\t%s\\t%d\\t%s", resultID(query, index), result.channel, result.seconds, result.title)
	}

	for query, list := range results {
		if len(list) == 0 {
			continue
		}

		// Download result 1.
		fmt.Fprintf(&body, "\t'ytsearch:%s') cp '%s' \"$dest\"; printf '%s\\n' > \"$info\" ;;\n",
			query, list[0].fixture, infoLine(query, 0, list[0]))

		// List them all.
		fmt.Fprintf(&body, "\t'ytsearch5:%s') printf '", query)
		for index, result := range list {
			fmt.Fprintf(&body, "%s\\n", infoLine(query, index, result))
		}
		body.WriteString("' ;;\n")

		// Download any one of them by URL.
		for index, result := range list {
			fmt.Fprintf(&body, "\t'https://www.youtube.com/watch?v=%s') cp '%s' \"$dest\"; printf '%s\\n' > \"$info\" ;;\n",
				resultID(query, index), result.fixture, infoLine(query, index, result))
		}
	}
	body.WriteString("esac\nexit 0\n")

	script = filepath.Join(dir, "yt-dlp-searching")
	if err := os.WriteFile(script, []byte(body.String()), 0755); err != nil {
		t.Fatalf("setting up: %v", err)
	}

	return script, calls
}

// readCalls returns every target the fake yt-dlp was called with, in order.
func readCalls(t *testing.T, calls string) []string {
	t.Helper()

	contents, err := os.ReadFile(calls)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatalf("reading the calls file: %v", err)
	}

	return strings.Split(strings.TrimSpace(string(contents)), "\n")
}

// assertKept checks the one file left in outputDir is fileName.mp3 at about
// wantMS long - which fixture it is says which result was kept. Anything else
// left behind would be counted as a track by the next run's scan.
func assertKept(t *testing.T, outputDir, fileName string, wantMS int) {
	t.Helper()

	gotMS, err := probeDurationMS(filepath.Join(outputDir, fileName+".mp3"))
	if err != nil {
		t.Fatalf("the kept file can't be read: %v", err)
	}

	// mp3 frames pad the length slightly.
	if diff := gotMS - wantMS; diff < -300 || diff > 300 {
		t.Errorf("kept a %dms file, want the %dms one", gotMS, wantMS)
	}

	entries, err := os.ReadDir(outputDir)
	if err != nil {
		t.Fatalf("listing the output: %v", err)
	}
	if len(entries) != 1 {
		var names []string
		for _, entry := range entries {
			names = append(names, entry.Name())
		}
		t.Errorf("output holds %q, want only the kept mp3", names)
	}
}

func assertErr(t *testing.T, err, want error) {
	t.Helper()

	switch {
	case want == nil && err != nil:
		t.Fatalf("unexpected error: %v", err)
	case want != nil && !errors.Is(err, want):
		t.Fatalf("error = %v, want %v", err, want)
	}
}

// The real case from 2026-11-13, played through the retry: the first search for
// Bluff's track returned an unrelated song, and the fallback found the right one.
func TestDownloadBestAudioRetries(t *testing.T) {
	// Wrong results are 2 seconds and right ones 3, so the length of the file
	// left behind says which search it came from. Spotify's length is left at
	// zero here, so only the title and artist decide.
	wrong := audioFixture(t, 2)
	right := audioFixture(t, 3)

	const (
		topicQuery    = "I Feel So Good Bluff topic"
		fallbackQuery = "I Feel So Good Bluff"
		secondArtist  = "I Feel So Good Someone Else"
	)

	tests := []struct {
		name      string
		results   map[string][]searchResult
		fallbacks []string
		wantErr   error
		wantMS    int
		wantCalls int
	}{
		{
			// The usual case, and the one that must cost nothing extra.
			name:      "first search right",
			results:   map[string][]searchResult{topicQuery: {{"I Feel So Good", "Bluff - Topic", right, 3}}},
			fallbacks: []string{fallbackQuery},
			wantMS:    3000,
			wantCalls: 1,
		},
		{
			name: "first wrong, fallback right",
			results: map[string][]searchResult{
				topicQuery:    {{"Snow on The Bluff", "evening elephants", wrong, 2}},
				fallbackQuery: {{"Bluff - I Feel So Good", "UKF", right, 3}},
			},
			fallbacks: []string{fallbackQuery},
			wantMS:    3000,
			wantCalls: 2,
		},
		{
			// Nothing better turned up, so the first search's result stays -
			// flagged, because it is probably still wrong. Both searches are
			// also listed, finding nothing new, before giving up.
			name: "every search wrong",
			results: map[string][]searchResult{
				topicQuery:    {{"Snow on The Bluff", "evening elephants", wrong, 2}},
				fallbackQuery: {{"The Bluff", "Wiz Khalifa", right, 3}},
			},
			fallbacks: []string{fallbackQuery},
			wantErr:   ErrSongMismatch,
			wantMS:    2000,
			wantCalls: 4,
		},
		{
			// The fallbacks also give a track the first search couldn't find at
			// all a second chance.
			name:      "first finds nothing, fallback right",
			results:   map[string][]searchResult{fallbackQuery: {{"Bluff - I Feel So Good", "UKF", right, 3}}},
			fallbacks: []string{fallbackQuery},
			wantMS:    3000,
			wantCalls: 2,
		},
		{
			name: "second artist rescues it",
			results: map[string][]searchResult{
				topicQuery:    {{"Snow on The Bluff", "evening elephants", wrong, 2}},
				fallbackQuery: {{"Call My Bluff", "JEV Lyrics", wrong, 2}},
				secondArtist:  {{"Someone Else - I Feel So Good", "Someone Else", right, 3}},
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
			results: map[string][]searchResult{
				topicQuery:    {{"I Feel So Good", "Some Other Band", wrong, 2}},
				fallbackQuery: {{"Bluff - I Feel So Good", "UKF", right, 3}},
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

			assertErr(t, err, test.wantErr)
			assertKept(t, outputDir, "I Feel So Good - Bluff", test.wantMS)

			if got := readCalls(t, calls); len(got) != test.wantCalls {
				t.Errorf("yt-dlp ran %d time(s), want %d: %q", len(got), test.wantCalls, got)
			}
		})
	}
}

// The real cases from the full re-run of Art Gallery: "Easy" by Nokaut and
// "Find Me" by Dominic B both came back as the right song in the wrong edit,
// and in both the edit Spotify has was the second result of the very same
// search. Spotify's length is 10 seconds here; the wrong edits are 2.
func TestDownloadBestAudioLooksDeeper(t *testing.T) {
	short := audioFixture(t, 2)
	long := audioFixture(t, 10)

	// Wrong in the other direction. A lying listing's real audio has to differ
	// from the original's, or keeping it by mistake would look identical.
	tooLong := audioFixture(t, 20)

	const (
		topicQuery    = "Easy Nokaut topic"
		fallbackQuery = "Easy Nokaut"
	)

	tests := []struct {
		name      string
		results   map[string][]searchResult
		fallbacks []string
		wantErr   error
		wantMS    int
		wantCalls []string
	}{
		{
			name: "the right edit is result 2",
			results: map[string][]searchResult{topicQuery: {
				{"Nokaut – Easy [RAR492]", "Raveart Records", short, 2},
				{"NOKAUT - (Easy) - original mix", "breaksbeat5.0", long, 10},
			}},
			wantMS: 10000,
			wantCalls: []string{
				"ytsearch:" + topicQuery,
				"ytsearch5:" + topicQuery,
				"https://www.youtube.com/watch?v=" + resultID(topicQuery, 1),
			},
		},
		{
			// Right first time: no listing, no extra cost.
			name: "already right",
			results: map[string][]searchResult{topicQuery: {
				{"Easy", "Nokaut - Topic", long, 10},
				{"NOKAUT - (Easy) - original mix", "breaksbeat5.0", long, 10},
			}},
			wantMS:    10000,
			wantCalls: []string{"ytsearch:" + topicQuery},
		},
		{
			// Every search is listed in turn, then the original is kept and
			// flagged as before.
			name: "nothing listed has the length",
			results: map[string][]searchResult{
				topicQuery: {
					{"Nokaut – Easy [RAR492]", "Raveart Records", short, 2},
					{"Nokaut - Easy (Live)", "Nokaut", short, 2},
				},
				fallbackQuery: {{"Nokaut - Easy", "Raveart Records", short, 2}},
			},
			fallbacks: []string{fallbackQuery},
			wantErr:   ErrDurationMismatch,
			wantMS:    2000,
			wantCalls: []string{
				"ytsearch:" + topicQuery,
				"ytsearch5:" + topicQuery,
				"ytsearch5:" + fallbackQuery,
			},
		},
		{
			// Length only chooses between results that already pass the title
			// and artist checks - it never lets an unrelated upload through.
			name: "right length but nobody credited",
			results: map[string][]searchResult{topicQuery: {
				{"Easy", "Nokaut - Topic", short, 2},
				{"Easy", "Some Other Band", long, 10},
			}},
			wantErr: ErrDurationMismatch,
			wantMS:  2000,
			wantCalls: []string{
				"ytsearch:" + topicQuery,
				"ytsearch5:" + topicQuery,
			},
		},
		{
			// A listing's length is a claim. The file is measured before it
			// replaces anything, and discarded when it doesn't match.
			name: "the listing lies about the length",
			results: map[string][]searchResult{topicQuery: {
				{"Nokaut – Easy [RAR492]", "Raveart Records", short, 2},
				{"NOKAUT - Easy", "breaksbeat5.0", tooLong, 10},
			}},
			wantErr: ErrDurationMismatch,
			wantMS:  2000,
			wantCalls: []string{
				"ytsearch:" + topicQuery,
				"ytsearch5:" + topicQuery,
				"https://www.youtube.com/watch?v=" + resultID(topicQuery, 1),
			},
		},
		{
			// Result 1 of every search is a different song, but the right one
			// sits at result 2 of the first search.
			name: "a wrong song is rescued from deeper in the list",
			results: map[string][]searchResult{
				topicQuery: {
					{"Snow on The Bluff", "evening elephants", short, 2},
					{"Easy", "Nokaut - Topic", long, 10},
				},
				fallbackQuery: {{"Something Else", "Nokaut", short, 2}},
			},
			fallbacks: []string{fallbackQuery},
			wantMS:    10000,
			wantCalls: []string{
				"ytsearch:" + topicQuery,
				"ytsearch:" + fallbackQuery,
				"ytsearch5:" + topicQuery,
				"https://www.youtube.com/watch?v=" + resultID(topicQuery, 1),
			},
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
				Artists:         []string{"Nokaut"},
				FileName:        "Easy - Nokaut",
				Tags:            Tags{Title: "Easy", Artist: "Nokaut"},
				DurationMS:      10000,
			})

			assertErr(t, err, test.wantErr)
			assertKept(t, outputDir, "Easy - Nokaut", test.wantMS)

			got := readCalls(t, calls)
			if strings.Join(got, "\n") != strings.Join(test.wantCalls, "\n") {
				t.Errorf("yt-dlp calls were\n  %q\nwant\n  %q", got, test.wantCalls)
			}
		})
	}
}

func TestDownloadBestAudioNamesBothTitlesOnASongMismatch(t *testing.T) {
	script, _ := fakeSearchingYtdlp(t, map[string][]searchResult{
		"I Feel So Good Bluff topic": {{"Snow on The Bluff", "evening elephants", audioFixture(t, 2), 2}},
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
