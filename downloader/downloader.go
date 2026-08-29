package downloader

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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

func NewAudioDownloader(outputDir string, ytdlpPath string) (*AudioDownloader, error) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return nil, fmt.Errorf("ffmpeg not found. Please install it: https://www.gyan.dev/ffmpeg/builds/ffmpeg-release-essentials.zip")
	}

	// Ensure output directory exists
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return nil, fmt.Errorf("create output dir: %w", err)
	}

	return &AudioDownloader{OutputDir: outputDir, YtdlpPath: ytdlpPath}, nil
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
