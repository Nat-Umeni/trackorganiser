// Package updater checks GitHub for a newer release and swaps the running binary
// for it. Asking whether to is left to main, the same as the yt-dlp and ffmpeg
// installs - this package only does the work.
package updater

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Vars rather than constants so tests can point them at an httptest server and a
// temp file - the same seam as spotifyAuthorizeURL and startCommand.
var (
	releaseAPIURL   = "https://api.github.com/repos/Nat-Umeni/trackorganiser/releases/latest"
	downloadBaseURL = "https://github.com/Nat-Umeni/trackorganiser/releases/download/"
	executablePath  = os.Executable
)

// checksumsFileName is what release.yml publishes beside the binaries.
const checksumsFileName = "checksums.txt"

// maxDownloadBytes caps what a download may read. The binaries are 7-8 MB; the
// cap only exists so a broken response can't fill memory.
const maxDownloadBytes = 100 << 20

// checkClient is short because the check runs on every start - being offline
// should cost a few seconds, not a hang before anything else happens.
var checkClient = &http.Client{Timeout: 5 * time.Second}

// downloadClient is generous because it fetches a whole binary over whatever
// connection the person happens to have.
var downloadClient = &http.Client{Timeout: 2 * time.Minute}

// assetName is the file release.yml publishes for a platform. It has to match
// the names in build.sh exactly, and a test reads build.sh to hold it to that.
//
// Takes goos and goarch as parameters, like ytdlpAssetName, so every platform
// can be asserted from one machine.
func assetName(goos, goarch string) (string, error) {
	switch goos + "/" + goarch {
	case "windows/amd64":
		return "trackorganiser-windows-amd64.exe", nil
	case "windows/arm64":
		return "trackorganiser-windows-arm64.exe", nil
	case "linux/amd64":
		return "trackorganiser-linux-amd64", nil
	case "linux/arm64":
		return "trackorganiser-linux-arm64", nil
	case "darwin/arm64":
		return "trackorganiser-macos-arm64", nil
	}

	return "", fmt.Errorf("no release is built for %s/%s", goos, goarch)
}

// parseVersion reads a release tag like v0.2.0 into its three numbers.
//
// Anything else is rejected on purpose. "dev" is a plain `go build`, and
// git describe gives "v0.1.0-3-gabc123" or "55b099c-dirty" for a local build -
// none of those should ever be offered an update over the top of themselves.
func parseVersion(version string) ([3]int, bool) {
	var parts [3]int

	trimmed, hasPrefix := strings.CutPrefix(version, "v")
	if !hasPrefix {
		return parts, false
	}

	fields := strings.Split(trimmed, ".")
	if len(fields) != len(parts) {
		return parts, false
	}

	for index, field := range fields {
		number, err := strconv.Atoi(field)
		if err != nil || number < 0 {
			return parts, false
		}
		parts[index] = number
	}

	return parts, true
}

// IsRelease reports whether version is a tagged release build. main checks this
// before LatestVersion, so a dev build never touches the network.
func IsRelease(version string) bool {
	_, ok := parseVersion(version)
	return ok
}

// IsNewer reports whether latest is a later release than current. Compared as
// numbers, not strings - as strings, v0.10.0 sorts before v0.9.0.
func IsNewer(current, latest string) bool {
	currentParts, ok := parseVersion(current)
	if !ok {
		return false
	}

	latestParts, ok := parseVersion(latest)
	if !ok {
		return false
	}

	// slices.Compare goes element by element and stops at the first difference,
	// which is exactly major, then minor, then patch.
	return slices.Compare(latestParts[:], currentParts[:]) > 0
}

// LatestVersion asks GitHub for the newest published release's tag. Drafts and
// prereleases are never returned by this endpoint, so tagging something as a
// prerelease is a way to publish it without offering it to anyone.
func LatestVersion() (string, error) {
	request, err := http.NewRequest(http.MethodGet, releaseAPIURL, nil)
	if err != nil {
		return "", err
	}

	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("User-Agent", "trackorganiser")

	response, err := checkClient.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GitHub answered HTTP %s", response.Status)
	}

	var release struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(response.Body).Decode(&release); err != nil {
		return "", fmt.Errorf("reading the release: %w", err)
	}

	if release.TagName == "" {
		return "", errors.New("the latest release has no tag")
	}

	return release.TagName, nil
}

// Apply downloads the given release for this platform, checks it against the
// release's checksums.txt, and swaps it in for the running binary. The new
// version takes effect on the next run.
func Apply(tag string) error {
	asset, err := assetName(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return err
	}

	// The tag's own URL, not releases/latest/download. A release published
	// between the check and this download would otherwise install a version
	// that isn't the one the person said yes to.
	base := downloadBaseURL + tag + "/"

	// Checksums first: it's tiny, and a release without one fails here before
	// spending 8 MB on a binary that would be refused anyway.
	sums, err := download(base + checksumsFileName)
	if err != nil {
		return err
	}

	want, err := checksumFor(sums, asset)
	if err != nil {
		return err
	}

	binary, err := download(base + asset)
	if err != nil {
		return err
	}

	got := sha256.Sum256(binary)
	if hex.EncodeToString(got[:]) != want {
		return fmt.Errorf("%s doesn't match its checksum, so it wasn't installed", asset)
	}

	exe, err := resolveExecutable()
	if err != nil {
		return err
	}

	return replaceExecutable(exe, binary)
}

// CleanupOld removes the previous binary an earlier update left behind. Windows
// can't delete an executable while it is running, so the update itself only
// moves it aside, and this clears it up on the next start.
func CleanupOld() {
	exe, err := resolveExecutable()
	if err != nil {
		return
	}

	// Not existing is the usual case, and any other failure is harmless - the
	// file just sits there until the next attempt.
	_ = os.Remove(exe + ".old")
}

// resolveExecutable finds the real file behind the running program. Symlinks are
// followed so an update replaces the binary itself, not a link pointing at it -
// otherwise a ~/bin link would become a copy, and the original would never
// update again.
func resolveExecutable() (string, error) {
	exe, err := executablePath()
	if err != nil {
		return "", fmt.Errorf("finding this program: %w", err)
	}

	resolved, err := filepath.EvalSymlinks(exe)
	if err != nil {
		return "", fmt.Errorf("finding this program: %w", err)
	}

	return resolved, nil
}

// download fetches url into memory, treating anything but a 200 as a failure.
func download(url string) ([]byte, error) {
	response, err := downloadClient.Get(url)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("downloading %s: HTTP %s", url, response.Status)
	}

	// One byte over the cap, so a body of exactly the cap is still accepted and
	// anything larger is detectable.
	body, err := io.ReadAll(io.LimitReader(response.Body, maxDownloadBytes+1))
	if err != nil {
		return nil, fmt.Errorf("downloading %s: %w", url, err)
	}

	if len(body) > maxDownloadBytes {
		return nil, fmt.Errorf("downloading %s: larger than %d MB, refusing it", url, maxDownloadBytes>>20)
	}

	return body, nil
}

// checksumFor finds name's hash in sha256sum output. Each line is the hash, then
// the file name - separated by two spaces in text mode, or " *" in binary mode,
// so the asterisk is stripped rather than relied on either way.
func checksumFor(sums []byte, name string) (string, error) {
	for _, line := range strings.Split(string(sums), "\n") {
		// Fields also eats a trailing \r, should the file ever pass through
		// something that adds Windows line endings.
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}

		if strings.TrimPrefix(fields[1], "*") == name {
			return strings.ToLower(fields[0]), nil
		}
	}

	return "", fmt.Errorf("%s has no entry for %s", checksumsFileName, name)
}

// replaceExecutable swaps binary in at exe. Every failure leaves the original in
// place, because a half-finished update that leaves nothing runnable is the one
// outcome that can't be fixed by running it again.
func replaceExecutable(exe string, binary []byte) error {
	info, err := os.Stat(exe)
	if err != nil {
		return fmt.Errorf("checking this program: %w", err)
	}

	// Beside the executable rather than in a temp dir, because os.Rename can't
	// move a file between filesystems and /tmp is often a separate one.
	newPath := exe + ".new"
	oldPath := exe + ".old"

	// Covers every early return below. Once the rename succeeds there is nothing
	// at newPath and this does nothing.
	defer os.Remove(newPath)

	if err := os.WriteFile(newPath, binary, info.Mode().Perm()); err != nil {
		return fmt.Errorf("writing the new version: %w", err)
	}

	// WriteFile only applies the mode when it creates the file, so a .new left by
	// an earlier failed attempt would keep whatever it had. Chmod makes it exact.
	if err := os.Chmod(newPath, info.Mode().Perm()); err != nil {
		return fmt.Errorf("making the new version executable: %w", err)
	}

	// A leftover .old from an update whose cleanup never ran would block the move
	// below on Windows.
	_ = os.Remove(oldPath)

	// Windows won't overwrite a running executable, but it will rename one. So
	// the old binary is moved aside instead of replaced - the same steps on every
	// platform, which is what lets a Linux test cover the Windows path.
	if err := os.Rename(exe, oldPath); err != nil {
		return fmt.Errorf("moving the old version aside: %w", err)
	}

	if err := os.Rename(newPath, exe); err != nil {
		// Put the original back, so the next run still has a program to start.
		if restoreErr := os.Rename(oldPath, exe); restoreErr != nil {
			return fmt.Errorf("installing the new version: %w - and putting the old one back failed too (%v), it is at %s", err, restoreErr, oldPath)
		}
		return fmt.Errorf("installing the new version: %w", err)
	}

	// Succeeds on Linux and macOS. Fails on Windows while this process is still
	// running from it, which is what CleanupOld is for.
	_ = os.Remove(oldPath)

	return nil
}
