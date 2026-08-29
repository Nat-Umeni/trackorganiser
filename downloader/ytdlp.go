package downloader

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

var ErrYtDlpMissing = errors.New("yt-dlp not found")

const packageUrl = "https://github.com/yt-dlp/yt-dlp/releases/latest/download/"

func InstallYtDlp() (string, error) {
	assetName, err := ytdlpAssetName(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return "", err
	}

	cachePath, err := ytdlpCachePath()
	if err != nil {
		return "", err
	}

	if err := os.MkdirAll(filepath.Dir(cachePath), 0755); err != nil {
		return "", err
	}

	resp, err := http.Get(packageUrl + assetName)
	if err != nil {
		return "", err
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("downloading %s: HTTP %s", packageUrl+assetName, resp.Status)
	}

	tempPath := cachePath + ".tmp"

	filePointer, err := os.Create(tempPath)
	if err != nil {
		return "", err
	}

	defer os.Remove(tempPath)
	defer filePointer.Close()

	_, copyErr := io.Copy(filePointer, resp.Body)
	if copyErr != nil {
		return "", copyErr
	}

	if err := filePointer.Close(); err != nil {
		return "", err
	}

	if err := os.Chmod(tempPath, 0755); err != nil {
		return "", err
	}

	if err := os.Rename(tempPath, cachePath); err != nil {
		return "", err
	}

	return cachePath, nil
}

func FindYtDlp() (string, error) {
	if path, err := exec.LookPath("yt-dlp"); err == nil {
		return path, nil
	}

	cachePath, cachePathErr := ytdlpCachePath()
	if cachePathErr != nil {
		return "", fmt.Errorf("failed to find yt-dlp in cache path during check: %w", cachePathErr)
	}

	_, osStatErr := os.Stat(cachePath)
	if osStatErr != nil {
		return "", ErrYtDlpMissing
	}

	return cachePath, nil
}

func ytdlpCachePath() (string, error) {
	assetName, err := ytdlpAssetName(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return "", err
	}

	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}

	return filepath.Join(cacheDir, "trackorganiser", assetName), nil
}

func ytdlpAssetName(goos, arch string) (string, error) {
	switch goos {
	case "darwin":
		return "yt-dlp_macos", nil

	case "linux":
		switch arch {
		case "arm64":
			return "yt-dlp_linux_aarch64", nil
		case "amd64":
			return "yt-dlp_linux", nil
		}

		return "", fmt.Errorf("unsupported linux architecture %s", arch)

	case "windows":
		return "yt-dlp.exe", nil

	default:
		return "", fmt.Errorf("unsupported platform %s/%s, install yt-dlp manually: https://github.com/yt-dlp/yt-dlp#installation", goos, arch)
	}
}
