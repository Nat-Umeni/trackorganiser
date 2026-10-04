package downloader

import (
	"archive/tar"
	"archive/zip"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/ulikunitz/xz"
)

func TestFfmpegAssetName(t *testing.T) {
	tests := []struct {
		name     string
		goos     string
		arch     string
		want     string
		wantErr  bool
		contains string
	}{
		{name: "windows amd64", goos: "windows", arch: "amd64", want: "ffmpeg-master-latest-win64-gpl.zip"},
		{name: "windows arm64", goos: "windows", arch: "arm64", want: "ffmpeg-master-latest-winarm64-gpl.zip"},
		{name: "linux amd64", goos: "linux", arch: "amd64", want: "ffmpeg-master-latest-linux64-gpl.tar.xz"},
		{name: "linux arm64", goos: "linux", arch: "arm64", want: "ffmpeg-master-latest-linuxarm64-gpl.tar.xz"},

		// Not an oversight: yt-dlp/FFmpeg-Builds publishes no macOS asset, so
		// there is nothing to download and brew is the honest answer.
		{name: "macos has no build", goos: "darwin", arch: "arm64", wantErr: true, contains: "brew install ffmpeg"},

		{name: "unknown platform", goos: "plan9", arch: "amd64", wantErr: true, contains: "ffmpeg.org/download"},
		{name: "unknown windows arch", goos: "windows", arch: "386", wantErr: true, contains: "winget"},
		{name: "unknown linux arch", goos: "linux", arch: "mips", wantErr: true, contains: "package manager"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := ffmpegAssetName(test.goos, test.arch)

			if test.wantErr {
				if err == nil {
					t.Fatalf("ffmpegAssetName(%q, %q) = %q, want an error", test.goos, test.arch, got)
				}
				if !strings.Contains(err.Error(), test.contains) {
					t.Errorf("error %q does not mention %q", err, test.contains)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != test.want {
				t.Errorf("ffmpegAssetName(%q, %q) = %q, want %q", test.goos, test.arch, got, test.want)
			}
		})
	}
}

func TestAddToPath(t *testing.T) {
	t.Run("prepends so our copy is found first", func(t *testing.T) {
		t.Setenv("PATH", "/usr/bin")

		AddToPath("/our/cache")

		want := "/our/cache" + string(os.PathListSeparator) + "/usr/bin"
		if got := os.Getenv("PATH"); got != want {
			t.Errorf("PATH = %q, want %q", got, want)
		}
	})

	t.Run("an empty dir changes nothing", func(t *testing.T) {
		// FindFfmpeg returns "" when ffmpeg is already on PATH, and that value
		// gets passed straight here.
		t.Setenv("PATH", "/usr/bin")

		AddToPath("")

		if got := os.Getenv("PATH"); got != "/usr/bin" {
			t.Errorf("PATH = %q, want it untouched", got)
		}
	})

	t.Run("an empty PATH does not gain a leading separator", func(t *testing.T) {
		t.Setenv("PATH", "")

		AddToPath("/our/cache")

		if got := os.Getenv("PATH"); got != "/our/cache" {
			t.Errorf("PATH = %q, want no stray separator", got)
		}
	})
}

func TestAddToPathMakesABinaryFindable(t *testing.T) {
	// The whole reason this approach was chosen over threading a path through
	// NewAudioDownloader, ytdlpArgs and probeDurationMS.
	if runtime.GOOS == "windows" {
		t.Skip("relies on a shell script")
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "faketool"), []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatalf("setting up: %v", err)
	}

	t.Setenv("PATH", t.TempDir())

	if _, err := exec.LookPath("faketool"); err == nil {
		t.Fatal("found the tool before adding its directory")
	}

	AddToPath(dir)

	if _, err := exec.LookPath("faketool"); err != nil {
		t.Errorf("still not findable after AddToPath: %v", err)
	}
}

// writeFakeBinaries puts ffmpeg and ffprobe in dir, named for this platform.
func writeFakeBinaries(t *testing.T, dir string, names ...string) {
	t.Helper()

	suffix := ""
	if runtime.GOOS == "windows" {
		suffix = ".exe"
	}

	for _, name := range names {
		if err := os.WriteFile(filepath.Join(dir, name+suffix), []byte("not really a binary"), 0755); err != nil {
			t.Fatalf("setting up: %v", err)
		}
	}
}

func TestFindFfmpeg(t *testing.T) {
	t.Run("already on PATH returns empty, not the cache", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("relies on extensionless executables")
		}

		binDir := t.TempDir()
		writeFakeBinaries(t, binDir, "ffmpeg", "ffprobe")
		t.Setenv("PATH", binDir)
		fakeCacheHome(t)

		dir, err := FindFfmpeg()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Empty means "nothing to add to PATH", which is the signal that a real
		// install is being used in preference to our copy.
		if dir != "" {
			t.Errorf("FindFfmpeg() = %q, want empty when both are on PATH", dir)
		}
	})

	t.Run("falls back to the cache", func(t *testing.T) {
		t.Setenv("PATH", t.TempDir())
		fakeCacheHome(t)

		cacheDir, err := ffmpegCacheDir()
		if err != nil {
			t.Fatalf("resolving the cache dir: %v", err)
		}
		if err := os.MkdirAll(cacheDir, 0755); err != nil {
			t.Fatalf("setting up: %v", err)
		}
		writeFakeBinaries(t, cacheDir, "ffmpeg", "ffprobe")

		dir, err := FindFfmpeg()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if dir != cacheDir {
			t.Errorf("FindFfmpeg() = %q, want the cache dir %q", dir, cacheDir)
		}
	})

	t.Run("ffmpeg without ffprobe is still missing", func(t *testing.T) {
		// The trap worth pinning: ffprobe is its own binary, and without it
		// every track silently skips duration verification.
		t.Setenv("PATH", t.TempDir())
		fakeCacheHome(t)

		cacheDir, _ := ffmpegCacheDir()
		if err := os.MkdirAll(cacheDir, 0755); err != nil {
			t.Fatalf("setting up: %v", err)
		}
		writeFakeBinaries(t, cacheDir, "ffmpeg")

		if _, err := FindFfmpeg(); !errors.Is(err, ErrFfmpegMissing) {
			t.Errorf("FindFfmpeg() error = %v, want ErrFfmpegMissing with ffprobe absent", err)
		}
	})

	t.Run("neither anywhere", func(t *testing.T) {
		t.Setenv("PATH", t.TempDir())
		fakeCacheHome(t)

		// A sentinel rather than any error, because main matches on it to decide
		// whether to offer the download.
		if _, err := FindFfmpeg(); !errors.Is(err, ErrFfmpegMissing) {
			t.Errorf("FindFfmpeg() error = %v, want ErrFfmpegMissing", err)
		}
	})
}

// archiveEntry is one file to put inside a test archive.
type archiveEntry struct {
	path     string
	contents string
}

// realArchiveLayout mirrors what the published archives actually contain,
// verified against both: the binaries nest inside a dated directory's bin/, with
// ffplay and docs alongside.
func realArchiveLayout() []archiveEntry {
	return []archiveEntry{
		{path: "ffmpeg-master-latest-linux64-gpl/bin/ffmpeg", contents: "the ffmpeg binary"},
		{path: "ffmpeg-master-latest-linux64-gpl/bin/ffprobe", contents: "the ffprobe binary"},
		{path: "ffmpeg-master-latest-linux64-gpl/bin/ffplay", contents: "not wanted"},
		{path: "ffmpeg-master-latest-linux64-gpl/LICENSE.txt", contents: "not wanted"},
		{path: "ffmpeg-master-latest-linux64-gpl/doc/ffmpeg-all.html", contents: "not wanted"},
	}
}

func buildZip(t *testing.T, path string, entries []archiveEntry) {
	t.Helper()

	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("setting up: %v", err)
	}
	defer file.Close()

	archive := zip.NewWriter(file)
	for _, entry := range entries {
		writer, err := archive.Create(entry.path)
		if err != nil {
			t.Fatalf("setting up %q: %v", entry.path, err)
		}
		if _, err := writer.Write([]byte(entry.contents)); err != nil {
			t.Fatalf("setting up %q: %v", entry.path, err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatalf("setting up: %v", err)
	}
}

func buildTarXz(t *testing.T, path string, entries []archiveEntry) {
	t.Helper()

	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("setting up: %v", err)
	}
	defer file.Close()

	compressor, err := xz.NewWriter(file)
	if err != nil {
		t.Fatalf("setting up: %v", err)
	}

	archive := tar.NewWriter(compressor)
	for _, entry := range entries {
		header := &tar.Header{
			Name:     entry.path,
			Mode:     0755,
			Size:     int64(len(entry.contents)),
			Typeflag: tar.TypeReg,
		}
		if err := archive.WriteHeader(header); err != nil {
			t.Fatalf("setting up %q: %v", entry.path, err)
		}
		if _, err := archive.Write([]byte(entry.contents)); err != nil {
			t.Fatalf("setting up %q: %v", entry.path, err)
		}
	}

	if err := archive.Close(); err != nil {
		t.Fatalf("setting up: %v", err)
	}
	if err := compressor.Close(); err != nil {
		t.Fatalf("setting up: %v", err)
	}
}

func TestExtractFfmpeg(t *testing.T) {
	// Both archive formats, built here rather than downloaded - 340 MB of real
	// archives is not a test dependency.
	formats := []struct {
		name  string
		file  string
		build func(*testing.T, string, []archiveEntry)
	}{
		{name: "zip", file: "ffmpeg-master-latest-win64-gpl.zip", build: buildZip},
		{name: "tar.xz", file: "ffmpeg-master-latest-linux64-gpl.tar.xz", build: buildTarXz},
	}

	for _, format := range formats {
		t.Run(format.name, func(t *testing.T) {
			work := t.TempDir()
			archivePath := filepath.Join(work, format.file)
			format.build(t, archivePath, realArchiveLayout())

			out := t.TempDir()
			found, err := extractFfmpeg(archivePath, out)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if found != 2 {
				t.Errorf("extracted %d binaries, want 2", found)
			}

			// Flattened out of the nested bin/ directory, so they sit where
			// AddToPath will make them findable.
			for name, wantContents := range map[string]string{
				"ffmpeg":  "the ffmpeg binary",
				"ffprobe": "the ffprobe binary",
			} {
				path := filepath.Join(out, name)
				contents, err := os.ReadFile(path)
				if err != nil {
					t.Errorf("%s was not extracted: %v", name, err)
					continue
				}
				if string(contents) != wantContents {
					t.Errorf("%s contains %q, want %q", name, contents, wantContents)
				}

				info, err := os.Stat(path)
				if err != nil {
					t.Fatalf("stat: %v", err)
				}
				// Executable, or it cannot be run.
				if info.Mode().Perm()&0100 == 0 {
					t.Errorf("%s has mode %04o, want it executable", name, info.Mode().Perm())
				}
			}

			// ffplay and the docs must not come out.
			for _, unwanted := range []string{"ffplay", "LICENSE.txt", "ffmpeg-all.html"} {
				if _, err := os.Stat(filepath.Join(out, unwanted)); err == nil {
					t.Errorf("%s was extracted but is not wanted", unwanted)
				}
			}

			// Nothing half-written left behind.
			leftovers, _ := filepath.Glob(filepath.Join(out, "*.partial"))
			if len(leftovers) != 0 {
				t.Errorf("left temporary files behind: %v", leftovers)
			}
		})
	}
}

func TestExtractFfmpegWithNothingWanted(t *testing.T) {
	// If the archive layout ever changes, this has to be an error rather than an
	// apparently successful install that later reports "ffmpeg not found".
	work := t.TempDir()
	archivePath := filepath.Join(work, "ffmpeg-master-latest-win64-gpl.zip")
	buildZip(t, archivePath, []archiveEntry{
		{path: "somewhere/else/readme.txt", contents: "nothing useful"},
	})

	found, err := extractFfmpeg(archivePath, t.TempDir())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if found != 0 {
		t.Errorf("extracted %d binaries from an archive with none", found)
	}
}

func TestExtractFfmpegIgnoresTraversalPaths(t *testing.T) {
	// Matching base names is what stops an archive entry escaping the output
	// directory. A hostile "../../ffmpeg" flattens to "ffmpeg" and lands inside
	// out, not above it.
	work := t.TempDir()
	out := t.TempDir()
	archivePath := filepath.Join(work, "ffmpeg-master-latest-win64-gpl.zip")
	buildZip(t, archivePath, []archiveEntry{
		{path: "../../../../etc/passwd", contents: "hostile"},
		{path: "../../ffmpeg", contents: "hostile but matches"},
		{path: "bin/ffprobe", contents: "the ffprobe binary"},
	})

	if _, err := extractFfmpeg(archivePath, out); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Nothing written outside out.
	if _, err := os.Stat(filepath.Join(filepath.Dir(out), "etc")); err == nil {
		t.Error("something was written outside the output directory")
	}

	// And the entry that did match landed inside it.
	for _, name := range []string{"ffmpeg", "ffprobe"} {
		if _, err := os.Stat(filepath.Join(out, name)); err != nil {
			t.Errorf("%s should have been extracted into the output dir: %v", name, err)
		}
	}
}

func TestExtractFfmpegOnACorruptArchive(t *testing.T) {
	tests := []struct {
		name string
		file string
	}{
		{name: "not a zip", file: "ffmpeg-master-latest-win64-gpl.zip"},
		{name: "not xz", file: "ffmpeg-master-latest-linux64-gpl.tar.xz"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// A truncated download must fail loudly rather than look like an
			// archive with no binaries in it.
			work := t.TempDir()
			archivePath := filepath.Join(work, test.file)
			if err := os.WriteFile(archivePath, []byte("this is not an archive"), 0644); err != nil {
				t.Fatalf("setting up: %v", err)
			}

			if _, err := extractFfmpeg(archivePath, t.TempDir()); err == nil {
				t.Error("got no error for a corrupt archive, want one")
			}
		})
	}
}

func TestFfmpegDownloadSize(t *testing.T) {
	// The prompt has to say what it is about to cost - a silent 150 MB pause
	// reads as a hang. The two platforms differ enough to be worth stating
	// accurately rather than rounding to one number.
	tests := []struct {
		goos     string
		contains string
	}{
		{goos: "windows", contains: "190 MB"},
		{goos: "linux", contains: "150 MB"},
	}

	for _, test := range tests {
		t.Run(test.goos, func(t *testing.T) {
			got := FfmpegDownloadSize(test.goos)
			if !strings.Contains(got, test.contains) {
				t.Errorf("FfmpegDownloadSize(%q) = %q, want it to mention %q", test.goos, got, test.contains)
			}

			// Unpacked size matters more than the download: these are static
			// builds, so 150 MB compressed becomes 335 MB on disk.
			if !strings.Contains(got, "335 MB") {
				t.Errorf("FfmpegDownloadSize(%q) = %q, want the unpacked size too", test.goos, got)
			}
		})
	}
}

func TestMacosNeverReachesTheDownloadPrompt(t *testing.T) {
	// FfmpegDownloadSize has a value for darwin, but it is unreachable: main
	// only prompts on ErrFfmpegMissing, and ffmpegAssetName fails first on macOS
	// because no asset is published. This pins that, so if a macOS build ever
	// appears the gap is obvious rather than silently offering a broken download.
	if _, err := ffmpegAssetName("darwin", "arm64"); err == nil {
		t.Fatal("ffmpegAssetName succeeded for darwin - if a build now exists, the prompt wiring needs revisiting")
	}
}
