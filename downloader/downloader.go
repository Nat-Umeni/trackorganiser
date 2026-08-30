package downloader

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/bogem/id3v2/v2"
)

// ErrDurationMismatch means the file downloaded fine but is not the length
// Spotify advertised - usually a music video with an intro, an extended mix, or
// the wrong song. The file is kept; callers should report it, not delete it.
var ErrDurationMismatch = errors.New("downloaded audio length does not match Spotify")

type AudioDownloader struct {
	OutputDir string
	YtdlpPath string

	// existing holds the lowercased base names of every mp3 already under
	// OutputDir, at any depth. Playlist folders get genre subfolders made by
	// hand, so a track that has been filed away must still count as downloaded.
	existing map[string]bool
}

type Tags struct {
	Title  string
	Artist string
	Album  string
}

// Request is everything needed to fetch and label one track. It is a struct
// rather than a parameter list because the list was already at three and each
// new detail about a track would add another.
type Request struct {
	Query      string
	FileName   string
	Tags       Tags
	DurationMS int
}

// ffmpegInstallHint names the command that installs ffmpeg on the given
// platform. It takes goos as a parameter rather than reading runtime.GOOS so it
// can be tested from any machine, the same as ytdlpAssetName.
//
// Unlike yt-dlp there is no self-bootstrap: the builds are archives rather than
// single binaries, there is no macOS build, and ffmpeg does not go stale the way
// yt-dlp does. A one-time install with the right command is enough.
func ffmpegInstallHint(goos string) string {
	switch goos {
	case "linux":
		return "install it with your package manager, e.g. `sudo pacman -S ffmpeg` or `sudo apt install ffmpeg`"
	case "darwin":
		return "install it with `brew install ffmpeg`"
	case "windows":
		return "install it with `winget install ffmpeg`, or download from https://www.gyan.dev/ffmpeg/builds/"
	default:
		return "see https://ffmpeg.org/download.html"
	}
}

func NewAudioDownloader(outputDir string, ytdlpPath string) (*AudioDownloader, error) {
	// ffprobe is checked separately: it ships with ffmpeg but is its own binary,
	// and without it every track silently skips duration verification, since
	// probeDurationMS failing is deliberately not fatal.
	for _, binary := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(binary); err != nil {
			return nil, fmt.Errorf("%s not found - %s", binary, ffmpegInstallHint(runtime.GOOS))
		}
	}

	// Ensure output directory exists
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return nil, fmt.Errorf("create output dir: %w", err)
	}

	return &AudioDownloader{
		OutputDir: outputDir,
		YtdlpPath: ytdlpPath,
		existing:  findExistingTracks(outputDir),
	}, nil
}

// findExistingTracks lists every mp3 under root, at any depth, keyed by
// lowercased base name. Lowercasing matters because macOS and Windows treat
// "Touch.mp3" and "touch.mp3" as the same file.
//
// Errors are swallowed deliberately: a directory that cannot be read means
// "assume nothing is here", which costs a re-download. Failing the whole run
// because one subfolder was unreadable would be a far worse trade.
func findExistingTracks(root string) map[string]bool {
	existing := make(map[string]bool)

	filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return nil
		}

		if strings.EqualFold(filepath.Ext(path), ".mp3") {
			existing[strings.ToLower(filepath.Base(path))] = true
		}

		return nil
	})

	return existing
}

// Has reports whether a track is already downloaded, anywhere under OutputDir.
// It takes the bare name as spotify.Track.FileName returns it and adds the
// extension itself, so callers never need to know the output format.
func (d *AudioDownloader) Has(fileName string) bool {
	return d.existing[strings.ToLower(fileName+".mp3")]
}

func (d *AudioDownloader) DownloadBestAudio(req Request) error {
	filepathForDownloader := filepath.Join(d.OutputDir, req.FileName)

	cmd := exec.Command(d.YtdlpPath,
		"-f", "bestaudio",
		"-x",
		"--audio-format", "mp3",
		"--audio-quality", "320k",
		"--output", filepathForDownloader+".%(ext)s",
		"--no-overwrites",
		"ytsearch:"+req.Query,
	)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("yt-dlp failed: %v\nOutput: %s", err, string(output))
	}

	downloadedPath := filepathForDownloader + ".mp3"

	// A search with no results is not an error to yt-dlp: it exits 0 having
	// downloaded nothing. Without this check addTagsToFile is the first thing to
	// notice the missing file, so a normal not-found reads as a code defect.
	if _, err := os.Stat(downloadedPath); err != nil {
		return fmt.Errorf("no YouTube results for %q", req.Query)
	}

	if err := addTagsToFile(downloadedPath, req.Tags); err != nil {
		return err
	}

	// ffprobe failing is not a reason to fail the download - the mp3 is fine and
	// tagged, we just can't verify it. ffprobe ships with ffmpeg but is a
	// separate binary, so it can genuinely be absent.
	actualMS, err := probeDurationMS(downloadedPath)
	if err != nil {
		return nil
	}

	if !durationsMatch(req.DurationMS, actualMS) {
		return fmt.Errorf(
			"%w: expected %s, got %s",
			ErrDurationMismatch,
			time.Duration(req.DurationMS)*time.Millisecond,
			time.Duration(actualMS)*time.Millisecond,
		)
	}

	return nil
}

func addTagsToFile(path string, tags Tags) error {
	tag, err := id3v2.Open(path, id3v2.Options{Parse: true})
	if err != nil {
		return fmt.Errorf("Error while opening mp3 file %q: %w ", tags.Title, err)
	}
	defer tag.Close()

	tag.SetAlbum(tags.Album)
	tag.SetArtist(tags.Artist)
	tag.SetTitle(tags.Title)

	if err = tag.Save(); err != nil {
		return fmt.Errorf("tagging %q: %w", tags.Title, err)
	}

	return nil
}
