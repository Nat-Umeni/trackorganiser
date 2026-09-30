package downloader

import (
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bogem/id3v2/v2"
)

// writeFiles creates each named file, making parent directories as needed, so a
// test can describe a folder layout as a list of relative paths.
func writeFiles(t *testing.T, root string, names []string) {
	t.Helper()

	for _, name := range names {
		path := filepath.Join(root, name)

		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatalf("setting up %q: %v", name, err)
		}
		if err := os.WriteFile(path, []byte("not really audio"), 0644); err != nil {
			t.Fatalf("setting up %q: %v", name, err)
		}
	}
}

func TestFindExistingTracks(t *testing.T) {
	tests := []struct {
		name    string
		files   []string
		want    []string
		wantNot []string
	}{
		{
			name:  "top level mp3s",
			files: []string{"WITNESS - Logic.mp3", "1945 - Black Prussia.mp3"},
			want:  []string{"witness - logic.mp3", "1945 - black prussia.mp3"},
		},
		{
			// The whole point of walking rather than reading one directory:
			// genre folders are made by hand inside the playlist folder.
			name:  "mp3s in genre subfolders",
			files: []string{"Hip Hop/WITNESS - Logic.mp3", "Jazz/Blue in Green - Miles Davis.mp3"},
			want:  []string{"witness - logic.mp3", "blue in green - miles davis.mp3"},
		},
		{
			name:  "nested several levels deep",
			files: []string{"Electronic/Dub/One Night Version - Chopstick.mp3"},
			want:  []string{"one night version - chopstick.mp3"},
		},
		{
			name:    "non-mp3 files are ignored",
			files:   []string{"cover.jpg", "notes.txt", "Real Track - Someone.mp3"},
			want:    []string{"real track - someone.mp3"},
			wantNot: []string{"cover.jpg", "notes.txt"},
		},
		{
			// Spotify names carry all sorts; the key is only ever the base name.
			name:  "unicode and punctuation survive",
			files: []string{"¥ØU$UK€/ETA - NewJeans.mp3", "🥱 - Someone.mp3"},
			want:  []string{"eta - newjeans.mp3", "🥱 - someone.mp3"},
		},
		{
			name:  "empty directory",
			files: nil,
			want:  nil,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			writeFiles(t, root, test.files)

			existing := findExistingTracks(root)

			if len(existing) != len(test.want) {
				t.Errorf("found %d track(s) %v, want %d %v",
					len(existing), existing, len(test.want), test.want)
			}
			for _, key := range test.want {
				path, found := existing[key]
				if !found {
					t.Errorf("expected %q to be found, got %v", key, existing)
					continue
				}
				// The path is the point of storing more than a bool: it is what
				// lets an already-downloaded track be corrected where it
				// actually sits, which may be a genre subfolder.
				if filepath.Base(strings.ToLower(path)) != key {
					t.Errorf("%q maps to %q, which is a different file", key, path)
				}
				if !filepath.IsAbs(path) && !strings.HasPrefix(path, root) {
					t.Errorf("%q maps to %q, which is not under the scanned root", key, path)
				}
			}
			for _, key := range test.wantNot {
				if existing[key] != "" {
					t.Errorf("did not expect %q to be found", key)
				}
			}
		})
	}
}

func TestFindExistingTracksOnMissingDirectory(t *testing.T) {
	// A directory that cannot be walked means "assume nothing is here", which
	// costs a re-download. It must not panic or return a nil map, because the
	// caller indexes into it.
	existing := findExistingTracks(filepath.Join(t.TempDir(), "does-not-exist"))

	if existing == nil {
		t.Fatal("got a nil map, want an empty one")
	}
	if len(existing) != 0 {
		t.Errorf("got %v, want no entries", existing)
	}
}

func TestAudioDownloaderHas(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, []string{
		"WITNESS - Logic.mp3",
		"Hip Hop/2-30 - Asake.mp3",
		"cover.jpg",
	})

	downloader := &AudioDownloader{OutputDir: root, existing: findExistingTracks(root)}

	tests := []struct {
		name     string
		fileName string
		want     bool
	}{
		{name: "top level track", fileName: "WITNESS - Logic", want: true},
		{name: "track filed into a genre folder", fileName: "2-30 - Asake", want: true},

		// macOS and Windows treat these as the same file, so matching must not
		// depend on case or we would download twice on those platforms.
		{name: "different case still matches", fileName: "witness - LOGIC", want: true},

		{name: "track we do not have", fileName: "Boiled Egg - Velveteen Hush", want: false},
		{name: "non-mp3 file is not a track", fileName: "cover", want: false},
		{name: "empty name", fileName: "", want: false},

		// Has adds the extension itself; callers pass Track.FileName() as-is.
		{name: "caller must not add the extension", fileName: "WITNESS - Logic.mp3", want: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := downloader.Has(test.fileName); got != test.want {
				t.Errorf("Has(%q) = %v, want %v", test.fileName, got, test.want)
			}
		})
	}
}

func TestFfmpegInstallHint(t *testing.T) {
	tests := []struct {
		name     string
		goos     string
		contains string
	}{
		{name: "linux names a package manager", goos: "linux", contains: "apt install ffmpeg"},
		{name: "macos names brew", goos: "darwin", contains: "brew install ffmpeg"},
		{name: "windows names winget", goos: "windows", contains: "winget install ffmpeg"},

		// The old message linked to a Windows-only download on every platform.
		// Anything unrecognised should fall back to somewhere genuinely useful.
		{name: "unknown platform falls back to the download page", goos: "plan9", contains: "ffmpeg.org/download"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := ffmpegInstallHint(test.goos)
			if !strings.Contains(got, test.contains) {
				t.Errorf("ffmpegInstallHint(%q) = %q, want it to mention %q",
					test.goos, got, test.contains)
			}
		})
	}
}

func TestNewAudioDownloaderRequiresFfprobe(t *testing.T) {
	// ffprobe ships with ffmpeg but is its own binary. Without it every track
	// silently skips duration verification, since probeDurationMS failing is
	// deliberately not fatal - so the constructor has to catch it.
	t.Setenv("PATH", t.TempDir())

	_, err := NewAudioDownloader(t.TempDir(), "yt-dlp")
	if err == nil {
		t.Fatal("got no error with an empty PATH, want one")
	}
	if !strings.Contains(err.Error(), "ffmpeg") {
		t.Errorf("error %q does not name the missing binary", err)
	}
}

func TestDownloadBestAudioReportsNoResults(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in relies on a shell script")
	}

	// yt-dlp exits 0 when a search finds nothing, having written no file. Stand
	// in for it with a script that does exactly that, so the "no results" path
	// can be exercised without touching the network.
	fakeYtdlp := filepath.Join(t.TempDir(), "yt-dlp-no-results")
	if err := os.WriteFile(fakeYtdlp, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatalf("setting up: %v", err)
	}

	dl := &AudioDownloader{OutputDir: t.TempDir(), YtdlpPath: fakeYtdlp}

	err := dl.DownloadBestAudio(Request{
		Query:    "Boiled Egg Velveteen Hush topic",
		FileName: "Boiled Egg - Velveteen Hush",
	})

	if err == nil {
		t.Fatal("got no error when nothing was downloaded, want one")
	}
	// Without the os.Stat check this surfaced as "Error while opening mp3 file",
	// which reads as a code defect rather than a track YouTube does not have.
	if !strings.Contains(err.Error(), "no YouTube results") {
		t.Errorf("error %q does not explain that the search found nothing", err)
	}
	if !strings.Contains(err.Error(), "Boiled Egg") {
		t.Errorf("error %q does not say which search failed", err)
	}
}

func TestNewAudioDownloaderScansExistingFiles(t *testing.T) {
	root := filepath.Join(t.TempDir(), "The Living Room")
	if err := os.MkdirAll(root, 0755); err != nil {
		t.Fatalf("setting up: %v", err)
	}
	writeFiles(t, root, []string{"Hip Hop/WITNESS - Logic.mp3"})

	// The ytdlp path is never executed here, only stored.
	downloader, err := NewAudioDownloader(root, "yt-dlp")
	if err != nil {
		t.Skipf("downloader unavailable on this machine: %v", err)
	}

	if !downloader.Has("WITNESS - Logic") {
		t.Error("constructor did not pick up the file already in a subfolder")
	}
	if downloader.Has("Boiled Egg - Velveteen Hush") {
		t.Error("constructor reported a track that is not there")
	}
}

func TestYtdlpArgs(t *testing.T) {
	const outputPath = "/music/Playlist/Touch - KATSEYE"
	const query = "Touch KATSEYE topic"

	tests := []struct {
		name       string
		jsRuntime  string
		titlePath  string
		useCookies bool
		wantFlags  []string
		notWant    []string
	}{
		{
			name:      "nothing optional available",
			notWant:   []string{"--js-runtimes", "--cookies-from-browser", "--print-to-file"},
			wantFlags: []string{"--audio-format", "mp3", "--no-overwrites", "-f", "bestaudio/best"},
		},
		{
			// "after_move" is what stops --print implying --simulate, which
			// would download nothing at all.
			name:      "title capture",
			titlePath: "/tmp/some-title-file",
			wantFlags: []string{"--print-to-file", "after_move:%(title)s", "/tmp/some-title-file"},
		},
		{
			name:      "js runtime only",
			jsRuntime: "node",
			wantFlags: []string{"--js-runtimes", "node"},
			notWant:   []string{"--cookies-from-browser"},
		},
		{
			name:       "cookies only",
			useCookies: true,
			wantFlags:  []string{"--cookies-from-browser", "firefox"},
			notWant:    []string{"--js-runtimes"},
		},
		{
			// The combination that fixes age-restricted tracks - neither alone
			// is enough.
			name:       "all three",
			jsRuntime:  "deno",
			titlePath:  "/tmp/t",
			useCookies: true,
			wantFlags: []string{
				"--js-runtimes", "deno",
				"--cookies-from-browser", "firefox",
				"--print-to-file", "after_move:%(title)s", "/tmp/t",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			args := ytdlpArgs(outputPath, query, test.jsRuntime, test.titlePath, test.useCookies)
			joined := strings.Join(args, " ")

			for _, flag := range test.wantFlags {
				if !slices.Contains(args, flag) {
					t.Errorf("args %q missing %q", joined, flag)
				}
			}
			for _, flag := range test.notWant {
				if slices.Contains(args, flag) {
					t.Errorf("args %q should not contain %q", joined, flag)
				}
			}

			// The search term has to stay last - yt-dlp takes it positionally,
			// so a flag appended after it would be read as another URL.
			if last := args[len(args)-1]; last != "ytsearch:"+query {
				t.Errorf("last arg is %q, want the ytsearch term", last)
			}
			if !slices.Contains(args, outputPath+".%(ext)s") {
				t.Errorf("args %q missing the output template", joined)
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

// silentTrack generates two seconds of real audio with ffmpeg, which is already a
// hard dependency. Real audio is needed because tagging and ffprobe both run over
// it - arbitrary bytes would fail before the timestamp is ever applied.
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

// downloaderWithPlacedFile builds a downloader whose yt-dlp stand-in writes
// nothing, with real audio already sitting where the download would land. That
// gets DownloadBestAudio past its os.Stat check and through tagging, which is
// where the timestamp is applied.
func downloaderWithPlacedFile(t *testing.T, fileName string) (*AudioDownloader, string) {
	t.Helper()

	if runtime.GOOS == "windows" {
		t.Skip("the yt-dlp stand-in relies on a shell script")
	}

	outputDir := t.TempDir()
	path := filepath.Join(outputDir, fileName+".mp3")
	if err := os.WriteFile(path, silentTrack(t), 0644); err != nil {
		t.Fatalf("setting up: %v", err)
	}

	return &AudioDownloader{OutputDir: outputDir, YtdlpPath: fakeYtdlp(t)}, path
}

func TestDownloadBestAudioStampsTheAddedDate(t *testing.T) {
	dl, path := downloaderWithPlacedFile(t, "Some Track - Someone")

	// Well in the past, so it cannot be confused with the file being written now.
	added := time.Date(2026, time.March, 14, 9, 26, 53, 0, time.UTC)

	// The duration is left at zero so the length check passes and the timestamp
	// is what the test is actually about.
	_ = dl.DownloadBestAudio(Request{
		Query:    "Some Track Someone topic",
		FileName: "Some Track - Someone",
		Tags:     Tags{Title: "Some Track"},
		AddedAt:  added,
	})

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("file went missing: %v", err)
	}
	if !info.ModTime().Equal(added) {
		t.Errorf("mtime = %v, want %v", info.ModTime(), added)
	}
}

func TestDownloadBestAudioLeavesMtimeAloneWithoutADate(t *testing.T) {
	dl, path := downloaderWithPlacedFile(t, "Some Track - Someone")

	before, err := os.Stat(path)
	if err != nil {
		t.Fatalf("setting up: %v", err)
	}

	// No AddedAt. Stamping the zero time would date the file to year 1, which is
	// worse than leaving it at whatever it already was.
	_ = dl.DownloadBestAudio(Request{
		Query:    "Some Track Someone topic",
		FileName: "Some Track - Someone",
		Tags:     Tags{Title: "Some Track"},
	})

	after, err := os.Stat(path)
	if err != nil {
		t.Fatalf("file went missing: %v", err)
	}
	if after.ModTime().Year() < 1900 {
		t.Errorf("mtime = %v, which means the zero time was stamped", after.ModTime())
	}
	if after.ModTime().Before(before.ModTime()) {
		t.Errorf("mtime went backwards: %v then %v", before.ModTime(), after.ModTime())
	}
}

func TestDownloadBestAudioStampsAfterTagging(t *testing.T) {
	// Tagging rewrites the file, so a timestamp applied before it would be
	// undone. This is the ordering test for that.
	dl, path := downloaderWithPlacedFile(t, "Tagged Track - Someone")

	added := time.Date(2025, time.December, 25, 0, 0, 0, 0, time.UTC)

	_ = dl.DownloadBestAudio(Request{
		Query:    "Tagged Track Someone topic",
		FileName: "Tagged Track - Someone",
		Tags:     Tags{Title: "Tagged Track", Artist: "Someone", Album: "An Album"},
		AddedAt:  added,
	})

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("file went missing: %v", err)
	}
	if !info.ModTime().Equal(added) {
		t.Errorf("mtime = %v, want %v - tagging probably ran last and overwrote it",
			info.ModTime(), added)
	}
}

// downloaderWithExisting places real audio under root, optionally in a
// subfolder, and returns a downloader that has scanned it.
func downloaderWithExisting(t *testing.T, relativePath string) (*AudioDownloader, string) {
	t.Helper()

	root := t.TempDir()
	path := filepath.Join(root, relativePath)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("setting up: %v", err)
	}
	if err := os.WriteFile(path, silentTrack(t), 0644); err != nil {
		t.Fatalf("setting up: %v", err)
	}

	dl, err := NewAudioDownloader(root, "yt-dlp")
	if err != nil {
		t.Skipf("downloader unavailable on this machine: %v", err)
	}

	return dl, path
}

func TestPathOf(t *testing.T) {
	dl, path := downloaderWithExisting(t, filepath.Join("Hip Hop", "WITNESS - Logic.mp3"))

	tests := []struct {
		name     string
		fileName string
		want     string
	}{
		// The whole reason the scan stores paths: OutputDir alone cannot tell
		// you a track was filed into a genre folder by hand.
		{name: "track in a subfolder", fileName: "WITNESS - Logic", want: path},
		{name: "case insensitive", fileName: "witness - LOGIC", want: path},
		{name: "track we do not have", fileName: "Nothing", want: ""},
		{name: "empty name", fileName: "", want: ""},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := dl.PathOf(test.fileName); got != test.want {
				t.Errorf("PathOf(%q) = %q, want %q", test.fileName, got, test.want)
			}
		})
	}
}

func TestRefreshExistingStampsInPlace(t *testing.T) {
	// In a subfolder specifically, because that is the case a path derived from
	// OutputDir would get wrong.
	dl, path := downloaderWithExisting(t, filepath.Join("Hip Hop", "WITNESS - Logic.mp3"))

	added := time.Date(2026, time.May, 27, 18, 32, 39, 0, time.UTC)

	changed, err := dl.RefreshExisting(Request{FileName: "WITNESS - Logic", AddedAt: added}, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !changed {
		t.Error("reported no change, but the timestamp was wrong")
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("file went missing: %v", err)
	}
	if !info.ModTime().Equal(added) {
		t.Errorf("mtime = %v, want %v", info.ModTime(), added)
	}
}

func TestRefreshExistingIsIdempotent(t *testing.T) {
	// Without this, every re-run would rewrite every inode and a backup tool
	// would see the whole library as changed.
	dl, path := downloaderWithExisting(t, "Track - Someone.mp3")

	added := time.Date(2026, time.May, 27, 18, 32, 39, 0, time.UTC)
	req := Request{FileName: "Track - Someone", AddedAt: added}

	if changed, err := dl.RefreshExisting(req, false); err != nil || !changed {
		t.Fatalf("first refresh: changed=%v err=%v, want true and no error", changed, err)
	}

	changed, err := dl.RefreshExisting(req, false)
	if err != nil {
		t.Fatalf("second refresh: %v", err)
	}
	if changed {
		t.Error("second refresh reported a change, but the timestamp was already correct")
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("file went missing: %v", err)
	}
	if !info.ModTime().Equal(added) {
		t.Errorf("mtime drifted to %v, want %v", info.ModTime(), added)
	}
}

func TestRefreshExistingRetagsThenStamps(t *testing.T) {
	// Retagging rewrites the whole file, so a timestamp applied before it would
	// be thrown away. This is the ordering test, and the reason both live in one
	// method rather than being left to the caller to sequence.
	dl, path := downloaderWithExisting(t, "Track - Someone.mp3")

	added := time.Date(2026, time.May, 27, 18, 32, 39, 0, time.UTC)

	changed, err := dl.RefreshExisting(Request{
		FileName: "Track - Someone",
		Tags:     Tags{Title: "Track", Artist: "Someone", Album: "An Album"},
		AddedAt:  added,
	}, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !changed {
		t.Error("reported no change after retagging")
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("file went missing: %v", err)
	}
	if !info.ModTime().Equal(added) {
		t.Errorf("mtime = %v, want %v - retagging probably ran last and overwrote it",
			info.ModTime(), added)
	}
}

func TestRefreshExistingLeavesTagsAloneWithoutTheFlag(t *testing.T) {
	dl, path := downloaderWithExisting(t, "Track - Someone.mp3")

	// Tag it with one thing, then refresh with different tags and retag off.
	if err := dl.addTagsToFile(path, Tags{Title: "Original Title"}); err != nil {
		t.Fatalf("setting up: %v", err)
	}

	_, err := dl.RefreshExisting(Request{
		FileName: "Track - Someone",
		Tags:     Tags{Title: "Replacement Title"},
		AddedAt:  time.Date(2026, time.May, 27, 18, 32, 39, 0, time.UTC),
	}, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	tag, err := id3v2.Open(path, id3v2.Options{Parse: true})
	if err != nil {
		t.Fatalf("reading tags back: %v", err)
	}
	defer tag.Close()

	if got := tag.Title(); got != "Original Title" {
		t.Errorf("title = %q, want it untouched without -retag-existing", got)
	}
}

func TestRefreshExistingOnATrackWeDoNotHave(t *testing.T) {
	dl, _ := downloaderWithExisting(t, "Track - Someone.mp3")

	changed, err := dl.RefreshExisting(Request{
		FileName: "Something Else - Nobody",
		AddedAt:  time.Now(),
	}, true)

	if err != nil {
		t.Errorf("unexpected error for a track we don't have: %v", err)
	}
	if changed {
		t.Error("reported a change for a track that isn't on disk")
	}
}

func TestRefreshExistingWithoutADate(t *testing.T) {
	// A track Spotify gave no timestamp for must not be stamped to year 1.
	dl, path := downloaderWithExisting(t, "Track - Someone.mp3")

	before, err := os.Stat(path)
	if err != nil {
		t.Fatalf("setting up: %v", err)
	}

	changed, err := dl.RefreshExisting(Request{FileName: "Track - Someone"}, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if changed {
		t.Error("reported a change with nothing to change")
	}

	after, err := os.Stat(path)
	if err != nil {
		t.Fatalf("file went missing: %v", err)
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Errorf("mtime moved from %v to %v", before.ModTime(), after.ModTime())
	}
}

// tinyJPEG is a real, minimal JPEG. It has to decode as an image rather than be
// arbitrary bytes, because ffprobe is what verifies the frame landed.
func tinyJPEG(t *testing.T) []byte {
	t.Helper()

	path := filepath.Join(t.TempDir(), "cover.jpg")
	cmd := exec.Command("ffmpeg", "-f", "lavfi", "-i", "color=c=red:s=300x300:d=1",
		"-frames:v", "1", "-y", path)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("ffmpeg could not generate a test image: %v\n%s", err, output)
	}

	image, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("setting up: %v", err)
	}

	return image
}

// downloaderWithArtServer gives a downloader, a tagged mp3 on disk, and a URL
// serving a real JPEG with a counter so fetches can be proven or disproven.
func downloaderWithArtServer(t *testing.T) (dl *AudioDownloader, path, url string, requests *atomic.Int64) {
	t.Helper()

	if runtime.GOOS == "windows" {
		t.Skip("relies on ffmpeg and a shell stand-in")
	}

	root := t.TempDir()
	path = filepath.Join(root, "Track - Someone.mp3")
	if err := os.WriteFile(path, silentTrack(t), 0644); err != nil {
		t.Fatalf("setting up: %v", err)
	}

	url, requests = fakeCDN(t, tinyJPEG(t), http.StatusOK)

	dl, err := NewAudioDownloader(root, "yt-dlp")
	if err != nil {
		t.Skipf("downloader unavailable on this machine: %v", err)
	}

	return dl, path, url, requests
}

// hasArt reports whether the file carries an attached picture frame.
func hasArt(t *testing.T, path string) bool {
	t.Helper()

	tag, err := id3v2.Open(path, id3v2.Options{Parse: true})
	if err != nil {
		t.Fatalf("reading tags: %v", err)
	}
	defer tag.Close()

	return len(tag.GetFrames(tag.CommonID("Attached picture"))) > 0
}

func TestAddTagsToFileEmbedsArt(t *testing.T) {
	dl, path, url, requests := downloaderWithArtServer(t)

	if hasArt(t, path) {
		t.Fatal("the file already had art before we started")
	}

	err := dl.addTagsToFile(path, Tags{
		Title:       "Track",
		Artist:      "Someone",
		Album:       "An Album",
		CoverArtURL: url,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !hasArt(t, path) {
		t.Error("no attached picture after tagging")
	}
	if requests.Load() != 1 {
		t.Errorf("made %d requests, want 1", requests.Load())
	}
}

func TestAddTagsToFileLeavesExistingArtAlone(t *testing.T) {
	// The "only if one isn't there" rule, and the reason a re-run over 297
	// already-arted tracks costs nothing extra: the fetch never happens.
	dl, path, url, requests := downloaderWithArtServer(t)

	tags := Tags{Title: "Track", Artist: "Someone", CoverArtURL: url}

	if err := dl.addTagsToFile(path, tags); err != nil {
		t.Fatalf("first pass: %v", err)
	}
	if requests.Load() != 1 {
		t.Fatalf("first pass made %d requests, want 1", requests.Load())
	}

	// Fresh downloader so the in-memory cache cannot be what saves us - the
	// check has to come from reading the file's existing frames.
	second, err := NewAudioDownloader(filepath.Dir(path), "yt-dlp")
	if err != nil {
		t.Skipf("downloader unavailable: %v", err)
	}

	if err := second.addTagsToFile(path, tags); err != nil {
		t.Fatalf("second pass: %v", err)
	}

	if requests.Load() != 1 {
		t.Errorf("made %d requests over two passes, want 1 - existing art was not detected", requests.Load())
	}
	if !hasArt(t, path) {
		t.Error("the art went missing on the second pass")
	}
}

func TestAddTagsToFileWithoutAnArtURL(t *testing.T) {
	dl, path, url, requests := downloaderWithArtServer(t)
	_ = url

	// No CoverArtURL - an album with no images, or a track Spotify gave none for.
	err := dl.addTagsToFile(path, Tags{Title: "Track", Artist: "Someone"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if requests.Load() != 0 {
		t.Errorf("made %d requests with no URL, want none", requests.Load())
	}
	if hasArt(t, path) {
		t.Error("art was attached despite there being no URL")
	}

	// A bare http.Get("") fails on its own, so "no requests" alone would pass
	// even without the guard. What the guard actually prevents is the cache
	// being consulted at all, so assert that directly.
	dl.cache.coverArtsLocker.Lock()
	entries := len(dl.cache.coverArts)
	dl.cache.coverArtsLocker.Unlock()

	if entries != 0 {
		t.Errorf("the cache holds %d entries, want none - the empty URL was looked up anyway", entries)
	}
}

func TestAddTagsToFileSurvivesAFailedArtFetch(t *testing.T) {
	// A cover that won't download is cosmetic. Losing the title and artist over
	// it would not be.
	if runtime.GOOS == "windows" {
		t.Skip("relies on ffmpeg")
	}

	root := t.TempDir()
	path := filepath.Join(root, "Track - Someone.mp3")
	if err := os.WriteFile(path, silentTrack(t), 0644); err != nil {
		t.Fatalf("setting up: %v", err)
	}

	deadURL, _ := fakeCDN(t, []byte("<html>gone</html>"), http.StatusNotFound)

	dl, err := NewAudioDownloader(root, "yt-dlp")
	if err != nil {
		t.Skipf("downloader unavailable: %v", err)
	}

	if err := dl.addTagsToFile(path, Tags{
		Title:       "Track",
		Artist:      "Someone",
		Album:       "An Album",
		CoverArtURL: deadURL,
	}); err != nil {
		t.Fatalf("a failed cover fetch should not fail tagging: %v", err)
	}

	tag, err := id3v2.Open(path, id3v2.Options{Parse: true})
	if err != nil {
		t.Fatalf("reading tags back: %v", err)
	}
	defer tag.Close()

	if got := tag.Title(); got != "Track" {
		t.Errorf("title = %q, want it saved despite the cover failing", got)
	}
	if got := tag.Artist(); got != "Someone" {
		t.Errorf("artist = %q, want it saved despite the cover failing", got)
	}
	if len(tag.GetFrames(tag.CommonID("Attached picture"))) != 0 {
		t.Error("an empty picture frame was attached after the fetch failed")
	}
}

func TestRefreshExistingAddsMissingArt(t *testing.T) {
	// The path that brought 297 already-downloaded tracks into line: art is
	// filled in on a re-run, not only on a fresh download.
	dl, path, url, requests := downloaderWithArtServer(t)

	added := time.Date(2026, time.May, 27, 18, 32, 39, 0, time.UTC)

	changed, err := dl.RefreshExisting(Request{
		FileName: "Track - Someone",
		Tags:     Tags{Title: "Track", Artist: "Someone", CoverArtURL: url},
		AddedAt:  added,
	}, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !changed {
		t.Error("reported no change")
	}

	if !hasArt(t, path) {
		t.Error("no art after refreshing")
	}
	if requests.Load() != 1 {
		t.Errorf("made %d requests, want 1", requests.Load())
	}

	// Retagging rewrites the file, so the timestamp must still be applied after
	// the art goes in.
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("file went missing: %v", err)
	}
	if !info.ModTime().Equal(added) {
		t.Errorf("mtime = %v, want %v - embedding art overwrote it", info.ModTime(), added)
	}
}
