package downloader

import (
    "fmt"
    "os"
    "os/exec"
)

type AudioDownloader struct {
    OutputDir string
}

func NewAudioDownloader(outputDir string) (*AudioDownloader, error) {
    // Check yt-dlp presence
    if _, err := exec.LookPath("yt-dlp"); err != nil {
        return nil, fmt.Errorf("yt-dlp not found. Please install it: https://github.com/yt-dlp/yt-dlp#installation")
    }

    if _, err := exec.LookPath("ffmpeg"); err != nil {
        return nil,fmt.Errorf("ffmpeg not found. Please install it: https://www.gyan.dev/ffmpeg/builds/ffmpeg-release-essentials.zip")
    }

	// Ensure output directory exists
    if err := os.MkdirAll(outputDir, 0755); err != nil {
        return nil,fmt.Errorf("create output dir: %w", err)
    }

    return &AudioDownloader{OutputDir: outputDir}, nil
}

func (d *AudioDownloader) DownloadBestAudio(query string) error {
    cmd := exec.Command(
        "yt-dlp",
        "-x", "--audio-format", "mp3", "--audio-quality", "192",
        "--output", d.OutputDir+"/%(title)s.%(ext)s",
        "ytsearch:"+query,
    )

    output, err := cmd.CombinedOutput()
    if err != nil {
        return fmt.Errorf("yt-dlp failed: %v\nOutput: %s", err, string(output))
    }
    return nil
}