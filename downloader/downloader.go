package downloader

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/bogem/id3v2/v2"
)

type AudioDownloader struct {
	OutputDir string
	YtdlpPath string
}

type Tags struct {
	Title  string
	Artist string
	Album  string
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

func (d *AudioDownloader) DownloadBestAudio(query string, filename string, tags Tags) error {
	filepathForDownloader := filepath.Join(d.OutputDir, filename)

	cmd := exec.Command(d.YtdlpPath,
		"-f", "bestaudio",
		"-x",
		"--audio-format", "mp3",
		"--audio-quality", "320k",
		"--output", filepathForDownloader+".%(ext)s",
		"--no-overwrites",
		"ytsearch:"+query,
	)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("yt-dlp failed: %v\nOutput: %s", err, string(output))
	}

	return addTagsToFile(filepathForDownloader+".mp3", tags)
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
