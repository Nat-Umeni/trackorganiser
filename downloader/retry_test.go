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

// searchResult is one video the fake yt-dlp "finds": its title and channel (the
// uploader, on SoundCloud), the audio to write if it is downloaded, and the
// length a listing claims for it. The claimed length is separate from the
// audio's real length so a test can make a listing lie, which the real one
// occasionally does - a SoundCloud preview is listed at full length.
type searchResult struct {
	title   string
	channel string
	fixture string
	seconds int
}

// drmProtected, used as a result's fixture, makes downloading it fail the way
// yt-dlp does on a protected SoundCloud track - before anything is fetched.
const drmProtected = "DRM"

// resultID gives each fake result a stable video ID, so a later "download this
// exact video" call can be matched back to it.
func resultID(query string, index int) string {
	hash := fnv.New32a()
	hash.Write([]byte(query))
	return fmt.Sprintf("v%x%d", hash.Sum32(), index)
}

// soundcloudURL is the fake link a SoundCloud result is listed with and
// downloaded by - deliberately nothing like a YouTube watch link, so code that
// assumed one would ask for something the fake doesn't know.
func soundcloudURL(query string, index int) string {
	return "https://soundcloud.com/fake/" + resultID("sc "+query, index)
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
// search, on YouTube and SoundCloud, and answers every kind of call the
// downloader makes, matched on the exact target - always the last argument:
//
//	ytsearch:<query>          download YouTube result 1: copy its audio to
//	                          --output, write its info line where
//	                          --print-to-file asks
//	ytsearchN:<query>         list YouTube results on stdout, as
//	                          --flat-playlist does (N is deeperListSize)
//	scsearchN:<query>         the same for SoundCloud
//	https://...watch?v=ID     download that YouTube result
//	https://soundcloud.com/…  download that SoundCloud result - or fail as
//	                          DRM-protected, if it's marked so
//
// Anything else finds nothing: exit 0, no file - the real yt-dlp's behaviour for
// a search with no results. Every call's target is appended to the returned
// calls file, so a test can check exactly what ran.
func fakeSearchingYtdlp(t *testing.T, results, soundcloud map[string][]searchResult) (script, calls string) {
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

	// One site's worth of branches: listing, downloading by link, and - for
	// YouTube - downloading a search's first result.
	addSite := func(listing string, link func(query string, index int) string, id func(query string, index int) string,
		searchDownload bool, results map[string][]searchResult) {

		infoLine := func(query string, index int, result searchResult) string {
			return fmt.Sprintf("%s\\t%s\\t%d\\t%s\\t%s", id(query, index), result.channel, result.seconds,
				link(query, index), result.title)
		}

		download := func(query string, index int, result searchResult) string {
			if result.fixture == drmProtected {
				return fmt.Sprintf("echo 'ERROR: [soundcloud] %s: This video is DRM protected' >&2; exit 1", id(query, index))
			}
			return fmt.Sprintf("cp '%s' \"$dest\"; printf '%s\\n' > \"$info\"", result.fixture, infoLine(query, index, result))
		}

		for query, list := range results {
			if len(list) == 0 {
				continue
			}

			if searchDownload {
				fmt.Fprintf(&body, "\t'ytsearch:%s') %s ;;\n", query, download(query, 0, list[0]))
			}

			// List them - but no more than the listing asks for, as the real
			// one does, so a result past the depth is genuinely out of reach.
			fmt.Fprintf(&body, "\t'%s%s') printf '", listing, query)
			for index, result := range list {
				if index >= deeperListSize {
					break
				}
				fmt.Fprintf(&body, "%s\\n", infoLine(query, index, result))
			}
			body.WriteString("' ;;\n")

			for index, result := range list {
				fmt.Fprintf(&body, "\t'%s') %s ;;\n", link(query, index), download(query, index, result))
			}
		}
	}

	addSite(listPrefix(), func(query string, index int) string {
		return "https://www.youtube.com/watch?v=" + resultID(query, index)
	}, resultID, true, results)

	addSite(scListPrefix(), soundcloudURL, func(query string, index int) string {
		return resultID("sc "+query, index)
	}, false, soundcloud)

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
//
// A wantMS of 0 means nothing should have been saved at all: a wrong song is
// not kept, so the next run tries again rather than finding it "downloaded".
func assertKept(t *testing.T, outputDir, fileName string, wantMS int) {
	t.Helper()

	if wantMS == 0 {
		entries, err := os.ReadDir(outputDir)
		if err != nil {
			t.Fatalf("listing the output: %v", err)
		}
		for _, entry := range entries {
			t.Errorf("%s was saved, but a wrong song must leave nothing behind", entry.Name())
		}
		return
	}

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
		name       string
		results    map[string][]searchResult
		soundcloud map[string][]searchResult
		fallbacks  []string
		wantErr    error
		wantMS     int
		wantCalls  int
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
			// Nothing right turned up anywhere - every search and both listings
			// - so nothing is saved. A wrong song is never worth having, and
			// leaving no file means the next run tries again.
			name: "every search wrong",
			results: map[string][]searchResult{
				topicQuery:    {{"Snow on The Bluff", "evening elephants", wrong, 2}},
				fallbackQuery: {{"The Bluff", "Wiz Khalifa", right, 3}},
			},
			fallbacks: []string{fallbackQuery},
			wantErr:   ErrSongMismatch,
			wantMS:    0,
			wantCalls: 5,
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
			script, calls := fakeSearchingYtdlp(t, test.results, test.soundcloud)
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

	// Within the 5s that flags a download, but outside the stand-in's 2s.
	nearly := audioFixture(t, 13)

	const (
		topicQuery    = "Easy Nokaut topic"
		fallbackQuery = "Easy Nokaut"
	)

	// Eleven other songs by the same artist, then the right one at #12 - where
	// "Everybody Dance" by CHIC turned up in a real listing. Out of reach of a
	// 5-deep listing.
	deepList := []searchResult{}
	for index := 1; index <= 11; index++ {
		deepList = append(deepList, searchResult{fmt.Sprintf("Other Song %d", index), "Nokaut - Topic", short, 2})
	}
	deepList = append(deepList, searchResult{"Easy", "Nokaut - Topic", long, 10})

	tests := []struct {
		name       string
		results    map[string][]searchResult
		soundcloud map[string][]searchResult
		fallbacks  []string
		wantErr    error
		wantMS     int
		wantCalls  []string
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
				listPrefix() + topicQuery,
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
				listPrefix() + topicQuery,
				listPrefix() + fallbackQuery,
				scListPrefix() + fallbackQuery,
			},
		},
		{
			// An upload naming no credited artist is only accepted when its
			// length is within the stand-in's 2s - near enough to be the same
			// recording. Within the usual 5s is not enough: 3s out could be
			// someone else's song of the same name.
			name: "uncredited upload a few seconds out",
			results: map[string][]searchResult{topicQuery: {
				{"Easy", "Nokaut - Topic", short, 2},
				{"Easy", "Some Other Band", long, 13},
			}},
			wantErr: ErrDurationMismatch,
			wantMS:  2000,
			wantCalls: []string{
				"ytsearch:" + topicQuery,
				listPrefix() + topicQuery,
				scListPrefix() + fallbackQuery,
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
				listPrefix() + topicQuery,
				"https://www.youtube.com/watch?v=" + resultID(topicQuery, 1),
				scListPrefix() + fallbackQuery,
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
				listPrefix() + topicQuery,
				"https://www.youtube.com/watch?v=" + resultID(topicQuery, 1),
			},
		},
		{
			// From a real run: GRIM SAINT's tracks are uploaded by "DiEM
			// ARCHiVE", which names no artist anywhere - the right song, at
			// Spotify's length to the second, rejected by the artist check.
			// A length that close stands in for the missing credit.
			name:      "label upload at the right length",
			results:   map[string][]searchResult{topicQuery: {{"Easy", "DiEM ARCHiVE", long, 10}}},
			wantMS:    10000,
			wantCalls: []string{"ytsearch:" + topicQuery},
		},
		{
			// The same upload at the wrong length has nothing standing in for
			// the artist, so the next search runs and its credited upload wins.
			name: "label upload at the wrong length",
			results: map[string][]searchResult{
				topicQuery:    {{"Easy", "DiEM ARCHiVE", short, 2}},
				fallbackQuery: {{"Nokaut - Easy", "Raveart Records", long, 10}},
			},
			fallbacks: []string{fallbackQuery},
			wantMS:    10000,
			wantCalls: []string{
				"ytsearch:" + topicQuery,
				"ytsearch:" + fallbackQuery,
			},
		},
		{
			// Chosen on the stand-in from a listing that claims 10s, but the
			// file measures 13s - inside the usual 5s, outside the stand-in's
			// 2s. With nobody credited, that's not close enough, so it is
			// discarded, and with nothing else right, nothing is saved.
			name: "stand-in that doesn't measure up",
			results: map[string][]searchResult{topicQuery: {
				{"Snow on The Bluff", "evening elephants", short, 2},
				{"Easy (Original Mix)", "Release - Topic", nearly, 10},
			}},
			wantErr: ErrSongMismatch,
			wantMS:  0,
			wantCalls: []string{
				"ytsearch:" + topicQuery,
				listPrefix() + topicQuery,
				"https://www.youtube.com/watch?v=" + resultID(topicQuery, 1),
				scListPrefix() + fallbackQuery,
			},
		},
		{
			// Length must not be what throws a right song away. The title is
			// right but nobody credited appears, and the length is too far out
			// to vouch for it - it could be an extended mix on a label's
			// channel, or someone else's song of the same name. Uncertain, so
			// it is kept and flagged for a listen, never discarded. Only a
			// wrong title means certainly a different song.
			name:      "right title, nobody credited, length off",
			results:   map[string][]searchResult{topicQuery: {{"Easy", "DiEM ARCHiVE", short, 2}}},
			wantErr:   ErrArtistUnconfirmed,
			wantMS:    2000,
			wantCalls: []string{"ytsearch:" + topicQuery, listPrefix() + topicQuery, scListPrefix() + fallbackQuery},
		},
		{
			// When one search found the right title and another found nothing
			// right at all, the right title is the one kept - even when it
			// came second.
			name: "the uncertain one is preferred over a wrong song",
			results: map[string][]searchResult{
				topicQuery:    {{"Snow on The Bluff", "evening elephants", tooLong, 20}},
				fallbackQuery: {{"Easy", "DiEM ARCHiVE", short, 2}},
			},
			fallbacks: []string{fallbackQuery},
			wantErr:   ErrArtistUnconfirmed,
			wantMS:    2000,
			wantCalls: []string{
				"ytsearch:" + topicQuery,
				"ytsearch:" + fallbackQuery,
				listPrefix() + fallbackQuery,
				listPrefix() + topicQuery,
				scListPrefix() + fallbackQuery,
			},
		},
		{
			name:    "the right one is result 12",
			results: map[string][]searchResult{topicQuery: deepList},
			wantMS:  10000,
			wantCalls: []string{
				"ytsearch:" + topicQuery,
				listPrefix() + topicQuery,
				"https://www.youtube.com/watch?v=" + resultID(topicQuery, 11),
			},
		},
		{
			// The stand-in works in a listing too: "Fallen (Original Mix)" from
			// "Release - Topic", 435s against Spotify's 435s.
			name: "label upload found deeper in the list",
			results: map[string][]searchResult{topicQuery: {
				{"Snow on The Bluff", "evening elephants", short, 2},
				{"Easy (Original Mix)", "Release - Topic", long, 10},
			}},
			wantMS: 10000,
			wantCalls: []string{
				"ytsearch:" + topicQuery,
				listPrefix() + topicQuery,
				"https://www.youtube.com/watch?v=" + resultID(topicQuery, 1),
			},
		},

		// SoundCloud: the last resort, for a track YouTube can't satisfy. From
		// a real run, "Talk To You (Xav Funky Remix)" was only ever on Xav's
		// own SoundCloud. It's searched without "topic", which means nothing
		// there and found nothing for "I Remember".
		{
			name:       "only on soundcloud",
			results:    map[string][]searchResult{topicQuery: {{"Snow on The Bluff", "evening elephants", short, 2}}},
			soundcloud: map[string][]searchResult{fallbackQuery: {{"Nokaut - Easy", "Nokaut", long, 10}}},
			wantMS:     10000,
			wantCalls: []string{
				"ytsearch:" + topicQuery,
				listPrefix() + topicQuery,
				scListPrefix() + fallbackQuery,
				soundcloudURL(fallbackQuery, 0),
			},
		},
		{
			// YouTube has nothing at all - which used to end the track before
			// any second look.
			name:       "youtube finds nothing, soundcloud has it",
			soundcloud: map[string][]searchResult{fallbackQuery: {{"Nokaut - Easy", "Nokaut", long, 10}}},
			wantMS:     10000,
			wantCalls: []string{
				"ytsearch:" + topicQuery,
				scListPrefix() + fallbackQuery,
				soundcloudURL(fallbackQuery, 0),
			},
		},
		{
			// Label releases on SoundCloud are DRM-protected and refused -
			// never worked around. The refusal costs nothing, so the next
			// result that passes gets its turn.
			name:    "drm-protected result skipped for the next",
			results: map[string][]searchResult{topicQuery: {{"Snow on The Bluff", "evening elephants", short, 2}}},
			soundcloud: map[string][]searchResult{fallbackQuery: {
				{"Easy", "Nokaut", drmProtected, 10},
				{"Nokaut - Easy (Original Mix)", "Nokaut", long, 10},
			}},
			wantMS: 10000,
			wantCalls: []string{
				"ytsearch:" + topicQuery,
				listPrefix() + topicQuery,
				scListPrefix() + fallbackQuery,
				soundcloudURL(fallbackQuery, 0),
				soundcloudURL(fallbackQuery, 1),
			},
		},
		{
			// SoundCloud gets exactly the same checks: nobody credited, and the
			// length too far out to stand in. Not chosen.
			name:       "soundcloud result fails the checks",
			results:    map[string][]searchResult{topicQuery: {{"Snow on The Bluff", "evening elephants", short, 2}}},
			soundcloud: map[string][]searchResult{fallbackQuery: {{"Easy", "Someone Else", long, 20}}},
			wantErr:    ErrSongMismatch,
			wantMS:     0,
			wantCalls: []string{
				"ytsearch:" + topicQuery,
				listPrefix() + topicQuery,
				scListPrefix() + fallbackQuery,
			},
		},
		{
			// A track only playable to subscribers is a 30-second preview to
			// everyone else, listed at full length. Measured, not trusted.
			name:       "soundcloud preview discarded",
			results:    map[string][]searchResult{topicQuery: {{"Snow on The Bluff", "evening elephants", short, 2}}},
			soundcloud: map[string][]searchResult{fallbackQuery: {{"Nokaut - Easy", "Nokaut", short, 10}}},
			wantErr:    ErrSongMismatch,
			wantMS:     0,
			wantCalls: []string{
				"ytsearch:" + topicQuery,
				listPrefix() + topicQuery,
				scListPrefix() + fallbackQuery,
				soundcloudURL(fallbackQuery, 0),
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			script, calls := fakeSearchingYtdlp(t, test.results, test.soundcloud)
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
	}, nil)
	dl := &AudioDownloader{OutputDir: t.TempDir(), YtdlpPath: script}

	err := dl.DownloadBestAudio(Request{
		Query:    "I Feel So Good Bluff topic",
		FileName: "I Feel So Good - Bluff",
		Tags:     Tags{Title: "I Feel So Good"},
	})

	if !errors.Is(err, ErrSongMismatch) {
		t.Fatalf("error = %v, want ErrSongMismatch", err)
	}

	// This line lands in the end-of-run "not found" list, so it has to say what
	// was asked for, the nearest thing found, and that nothing was kept -
	// otherwise someone goes looking for a file that isn't there.
	for _, want := range []string{"I Feel So Good", "Snow on The Bluff", "not saved"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q doesn't mention %q", err, want)
		}
	}
}

// listPrefix is the target prefix of a deeper-pick listing, taken from the
// constant itself so the fake and the code can't disagree about the depth.
func listPrefix() string {
	return fmt.Sprintf("ytsearch%d:", deeperListSize)
}

// scListPrefix is the target prefix of a SoundCloud listing, from the same
// constant as listPrefix.
func scListPrefix() string {
	return fmt.Sprintf("scsearch%d:", deeperListSize)
}

// From a real run: "Bounce" by bullet tooth is on the artist's SoundCloud, but
// DRM-protected - the program never works around that. What it can do is say
// so, with the exact filename to save a bought copy as: the next run then finds
// it with Has() and tags it, like any track already downloaded.
func TestDownloadBestAudioReportsDRM(t *testing.T) {
	script, _ := fakeSearchingYtdlp(t,
		map[string][]searchResult{"Easy Nokaut topic": {{"Snow on The Bluff", "evening elephants", audioFixture(t, 2), 2}}},
		map[string][]searchResult{"Easy Nokaut": {{"Easy", "Nokaut", drmProtected, 10}}},
	)
	outputDir := t.TempDir()
	dl := &AudioDownloader{OutputDir: outputDir, YtdlpPath: script}

	err := dl.DownloadBestAudio(Request{
		Query:      "Easy Nokaut topic",
		Artists:    []string{"Nokaut"},
		FileName:   "Easy - Nokaut",
		Tags:       Tags{Title: "Easy", Artist: "Nokaut"},
		DurationMS: 10000,
	})

	// Still the same outcome as any song not found...
	if !errors.Is(err, ErrSongMismatch) {
		t.Fatalf("error = %v, want ErrSongMismatch", err)
	}

	// ...but saying where it is, and what to do about it.
	for _, want := range []string{"DRM", `"Easy - Nokaut.mp3"`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q doesn't mention %s", err, want)
		}
	}

	assertKept(t, outputDir, "Easy - Nokaut", 0)
}

// From a real run: the same DRM-protected "Bounce" came up in two different
// searches' listings, and was refused twice. A refused result is remembered as
// tried, so a later listing doesn't ask for it again.
func TestDownloadBestAudioDoesNotRetryARefusedResult(t *testing.T) {
	const (
		topicQuery    = "Easy Nokaut topic"
		fallbackQuery = "Easy Nokaut"
	)

	script, calls := fakeSearchingYtdlp(t,
		map[string][]searchResult{topicQuery: {{"Snow on The Bluff", "evening elephants", audioFixture(t, 2), 2}}},
		map[string][]searchResult{fallbackQuery: {{"Easy", "Nokaut", drmProtected, 10}}},
	)
	dl := &AudioDownloader{OutputDir: t.TempDir(), YtdlpPath: script}

	// The same search twice gives the same listing twice - the same result
	// under the same ID, as two overlapping real searches do.
	_ = dl.DownloadBestAudio(Request{
		Query:           topicQuery,
		FallbackQueries: []string{fallbackQuery, fallbackQuery},
		Artists:         []string{"Nokaut"},
		FileName:        "Easy - Nokaut",
		Tags:            Tags{Title: "Easy", Artist: "Nokaut"},
		DurationMS:      10000,
	})

	attempts := 0
	for _, call := range readCalls(t, calls) {
		if call == soundcloudURL(fallbackQuery, 0) {
			attempts++
		}
	}
	if attempts != 1 {
		t.Errorf("the refused result was downloaded %d times, want once", attempts)
	}
}
