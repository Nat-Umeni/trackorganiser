package downloader

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
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
				if !existing[key] {
					t.Errorf("expected %q to be found, got %v", key, existing)
				}
			}
			for _, key := range test.wantNot {
				if existing[key] {
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
