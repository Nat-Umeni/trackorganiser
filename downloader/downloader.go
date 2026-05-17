package downloader

import (
    "fmt"
    "os"
    "os/exec"
)

type AudioDownloader struct {
    OutputDir string
}

func NewAudioDownloader(outputDir string) *AudioDownloader {
    // Create output directory if it doesn't exist
    if err := os.MkdirAll(outputDir, 0755); err != nil {
        // Maybe return an error instead, but New... usually doesn't fail.
        // For simplicity, we'll create lazily in DownloadBestAudio.
    }
    return &AudioDownloader{OutputDir: outputDir}
}

func (d *AudioDownloader) DownloadBestAudio(query string) error {
    // Check yt-dlp presence
    if _, err := exec.LookPath("yt-dlp"); err != nil {
        return fmt.Errorf("yt-dlp not found. Please install it: https://github.com/yt-dlp/yt-dlp#installation")
    }

	// Ensure output directory exists
    if err := os.MkdirAll(d.OutputDir, 0755); err != nil {
        return fmt.Errorf("create output dir: %w", err)
    }

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