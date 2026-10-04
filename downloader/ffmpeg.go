package downloader

import (
	"archive/tar"
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/ulikunitz/xz"
)

// ErrFfmpegMissing means neither binary is on PATH or in our cache. A sentinel so
// main can offer the download, the same shape as ErrYtDlpMissing.
var ErrFfmpegMissing = errors.New("ffmpeg not found")

const ffmpegPackageUrl = "https://github.com/yt-dlp/FFmpeg-Builds/releases/download/latest/"

// ffmpegBinaries are the two we want out of the archive. ffplay ships alongside
// them and is no use here.
//
// Matching on base name is also the path traversal guard: an archive entry called
// "../../something" never matches, so nothing can be written outside the cache
// directory.
var ffmpegBinaries = map[string]bool{
	"ffmpeg":      true,
	"ffmpeg.exe":  true,
	"ffprobe":     true,
	"ffprobe.exe": true,
}

// FfmpegDownloadSize describes what the prompt is about to cost, because a
// silent pause on a download this size reads as a hang. Measured, not guessed:
// the archives are 190 MB on Windows and 149 MB on Linux, and they extract to
// roughly 335 MB because these are static builds with every codec compiled in.
func FfmpegDownloadSize(goos string) string {
	if goos == "windows" {
		return "a 190 MB download, around 335 MB once unpacked"
	}

	return "a 150 MB download, around 335 MB once unpacked"
}

// ffmpegAssetName returns the archive to fetch for a platform.
//
// goos is a parameter rather than runtime.GOOS so every platform is assertable
// from one machine, the same as ytdlpAssetName and ffmpegInstallHint.
//
// macOS is a deliberate error: yt-dlp/FFmpeg-Builds publishes nothing for it, so
// there is no download to offer and brew is the honest answer.
func ffmpegAssetName(goos, arch string) (string, error) {
	switch goos {
	case "windows":
		switch arch {
		case "amd64":
			return "ffmpeg-master-latest-win64-gpl.zip", nil
		case "arm64":
			return "ffmpeg-master-latest-winarm64-gpl.zip", nil
		}

		return "", fmt.Errorf("unsupported windows architecture %s - %s", arch, ffmpegInstallHint(goos))

	case "linux":
		switch arch {
		case "amd64":
			return "ffmpeg-master-latest-linux64-gpl.tar.xz", nil
		case "arm64":
			return "ffmpeg-master-latest-linuxarm64-gpl.tar.xz", nil
		}

		return "", fmt.Errorf("unsupported linux architecture %s - %s", arch, ffmpegInstallHint(goos))

	default:
		return "", fmt.Errorf("no ffmpeg build published for %s - %s", goos, ffmpegInstallHint(goos))
	}
}

// ffmpegCacheDir is where the extracted binaries live - the same directory yt-dlp
// is downloaded into, so one place holds everything we fetch.
func ffmpegCacheDir() (string, error) {
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}

	return filepath.Join(cacheDir, "trackorganiser"), nil
}

// cacheHasBoth reports whether the cache holds ffmpeg and ffprobe for this
// platform.
//
// Both, not just ffmpeg: ffprobe is its own binary, and without it every track
// silently skips duration verification.
func cacheHasBoth(dir string) bool {
	suffix := ""
	if runtime.GOOS == "windows" {
		suffix = ".exe"
	}

	for _, name := range []string{"ffmpeg" + suffix, "ffprobe" + suffix} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			return false
		}
	}

	return true
}

// FindFfmpeg returns the directory to add to PATH, or an empty string when both
// binaries are already on PATH.
//
// A real install always wins over our cached copy, the same rule FindYtDlp
// follows.
func FindFfmpeg() (string, error) {
	_, ffmpegErr := exec.LookPath("ffmpeg")
	_, ffprobeErr := exec.LookPath("ffprobe")
	if ffmpegErr == nil && ffprobeErr == nil {
		return "", nil
	}

	dir, err := ffmpegCacheDir()
	if err != nil {
		return "", err
	}

	if cacheHasBoth(dir) {
		return dir, nil
	}

	return "", ErrFfmpegMissing
}

// AddToPath prepends dir to PATH for this process and everything it launches.
//
// This is what makes a cached ffmpeg usable without threading its location
// through three call sites: exec.LookPath reads PATH on each call, and child
// processes inherit it, so yt-dlp finds ffmpeg and exec.Command("ffprobe")
// resolves.
func AddToPath(dir string) {
	if dir == "" {
		return
	}

	existing := os.Getenv("PATH")
	if existing == "" {
		os.Setenv("PATH", dir)
		return
	}

	os.Setenv("PATH", dir+string(os.PathListSeparator)+existing)
}

// InstallFfmpeg downloads the archive for this platform and extracts the two
// binaries into the cache directory, returning it for AddToPath.
func InstallFfmpeg() (string, error) {
	assetName, err := ffmpegAssetName(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return "", err
	}

	dir, err := ffmpegCacheDir()
	if err != nil {
		return "", err
	}

	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}

	archivePath := filepath.Join(dir, assetName+".tmp")
	if err := downloadTo(ffmpegPackageUrl+assetName, archivePath); err != nil {
		return "", err
	}

	// The archive is only ever a means to the two binaries.
	defer os.Remove(archivePath)

	extracted, err := extractFfmpeg(archivePath, dir)
	if err != nil {
		return "", err
	}

	// Finding neither binary means the archive layout changed, which would
	// otherwise surface later as a confusing "ffmpeg not found" after an
	// apparently successful download.
	if extracted == 0 {
		return "", fmt.Errorf("no ffmpeg binaries inside %s - the archive layout may have changed", assetName)
	}

	return dir, nil
}

// downloadTo fetches a URL to a path, checking the status before writing so an
// error page cannot land on disk looking like an archive.
func downloadTo(url, path string) error {
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("downloading %s: HTTP %s", url, resp.Status)
	}

	file, err := os.Create(path)
	if err != nil {
		return err
	}

	if _, err := io.Copy(file, resp.Body); err != nil {
		file.Close()
		os.Remove(path)
		return err
	}

	return file.Close()
}

// extractFfmpeg pulls the wanted binaries out of an archive into dir, flattened
// out of its nested bin/ directory, and returns how many it found.
func extractFfmpeg(archivePath, dir string) (int, error) {
	if strings.HasSuffix(archivePath, ".zip.tmp") || strings.HasSuffix(archivePath, ".zip") {
		return extractFromZip(archivePath, dir)
	}

	return extractFromTarXz(archivePath, dir)
}

// writeBinary saves one extracted binary, via a .tmp name so an interrupted
// extract cannot leave a half-written file that FindFfmpeg then reports as
// installed.
func writeBinary(dir, name string, contents io.Reader) error {
	finalPath := filepath.Join(dir, name)
	tempPath := finalPath + ".partial"

	file, err := os.OpenFile(tempPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0755)
	if err != nil {
		return err
	}

	if _, err := io.Copy(file, contents); err != nil {
		file.Close()
		os.Remove(tempPath)
		return err
	}

	if err := file.Close(); err != nil {
		os.Remove(tempPath)
		return err
	}

	return os.Rename(tempPath, finalPath)
}

// extractFromZip handles the Windows archives. archive/zip needs an io.ReaderAt,
// which is why the download goes to a file first rather than streaming.
func extractFromZip(archivePath, dir string) (int, error) {
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		return 0, fmt.Errorf("reading %s: %w", filepath.Base(archivePath), err)
	}
	defer reader.Close()

	found := 0
	for _, entry := range reader.File {
		name := filepath.Base(entry.Name)
		if !ffmpegBinaries[name] {
			continue
		}

		contents, err := entry.Open()
		if err != nil {
			return found, err
		}

		writeErr := writeBinary(dir, name, contents)
		contents.Close()
		if writeErr != nil {
			return found, writeErr
		}

		found++
	}

	return found, nil
}

// extractFromTarXz handles the Linux archives - xz wrapping tar, both of which
// stream, so no seeking is needed.
func extractFromTarXz(archivePath, dir string) (int, error) {
	file, err := os.Open(archivePath)
	if err != nil {
		return 0, err
	}
	defer file.Close()

	decompressed, err := xz.NewReader(file)
	if err != nil {
		return 0, fmt.Errorf("decompressing %s: %w", filepath.Base(archivePath), err)
	}

	archive := tar.NewReader(decompressed)

	found := 0
	for {
		header, err := archive.Next()
		if err == io.EOF {
			return found, nil
		}
		if err != nil {
			return found, fmt.Errorf("reading %s: %w", filepath.Base(archivePath), err)
		}

		if header.Typeflag != tar.TypeReg {
			continue
		}

		name := filepath.Base(header.Name)
		if !ffmpegBinaries[name] {
			continue
		}

		if err := writeBinary(dir, name, archive); err != nil {
			return found, err
		}

		found++
	}
}
