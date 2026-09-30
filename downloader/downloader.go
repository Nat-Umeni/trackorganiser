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

// ErrVersionMismatch means yt-dlp returned a different cut of the track - a
// different remix, say. Unlike a length difference this is nearly always wrong,
// but the file is still kept for the caller to judge.
var ErrVersionMismatch = errors.New("downloaded a different version")

// readTitleFile returns what yt-dlp recorded, or an empty string if anything at
// all went wrong. An unreadable title only means the version cannot be checked;
// the mp3 is downloaded and tagged either way.
func readTitleFile(path string) string {
	if path == "" {
		return ""
	}

	contents, err := os.ReadFile(path)
	if err != nil {
		return ""
	}

	return strings.TrimSpace(string(contents))
}

type AudioDownloader struct {
	OutputDir string
	YtdlpPath string

	// existing maps the lowercased base name of every mp3 already under
	// OutputDir to its full path, at any depth. Playlist folders get genre
	// subfolders made by hand, so a track that has been filed away must still
	// count as downloaded - and the path is what lets it be corrected in place
	// rather than only recognised.
	existing map[string]string

	// Both probed once at construction. Empty and false respectively are valid -
	// yt-dlp runs without either, just with fewer formats and no age-gated
	// tracks.
	jsRuntime  string
	useCookies bool
	cache      *coverArtCache
}

type Tags struct {
	Title       string
	Artist      string
	Album       string
	CoverArtURL string
}

// Request is everything needed to fetch and label one track. It is a struct
// rather than a parameter list because the list was already at three and each
// new detail about a track would add another.
type Request struct {
	Query      string
	FileName   string
	Tags       Tags
	DurationMS int

	// AddedAt is when the track joined the Spotify playlist. It becomes the
	// file's modification time, which is the only place a folder can carry the
	// playlist's order - filenames sort alphabetically and a directory has no
	// inherent order of its own.
	AddedAt time.Time
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

	// A missing home directory only means no cookies, which is survivable.
	home, _ := os.UserHomeDir()

	return &AudioDownloader{
		OutputDir:  outputDir,
		YtdlpPath:  ytdlpPath,
		existing:   findExistingTracks(outputDir),
		jsRuntime:  FindJSRuntime(),
		useCookies: FirefoxCookiesAvailable(home),
		cache:      newCoverArtCache(),
	}, nil
}

// ytdlpArgs assembles the command line for one download. Split out from
// DownloadBestAudio so the conditional flags can be asserted without running
// anything.
func ytdlpArgs(outputPath, query, jsRuntime, titlePath string, useCookies bool) []string {
	args := []string{
		// The "/" is a fallback: best audio-only stream, or failing that the
		// best anything. Some videos only offer formats behind a PO token, and
		// without the fallback yt-dlp reports "Requested format is not
		// available" rather than taking a combined stream. -x strips the video
		// either way, so the only cost is wasted bytes on those few tracks.
		"-f", "bestaudio/best",
		"-x",
		"--audio-format", "mp3",
		"--audio-quality", "320k",
		"--output", outputPath + ".%(ext)s",
		"--no-overwrites",
	}

	// Without a runtime, yt-dlp reports available videos as "This video is not
	// available" and silently skips formats.
	if jsRuntime != "" {
		args = append(args, "--js-runtimes", jsRuntime)
	}

	// Age-restricted tracks need this, and it only works alongside a JS runtime.
	if useCookies {
		args = append(args, "--cookies-from-browser", "firefox")
	}

	// Records what yt-dlp actually picked, so the version can be checked against
	// what Spotify asked for. "after_move" matters: --print implies --simulate
	// unless a later stage is named, and simulating would download nothing.
	//
	// The path is a temp file rather than one derived from the track name
	// because this argument is parsed as an output template - a track called
	// "100% Pure" would otherwise mangle it.
	if titlePath != "" {
		args = append(args, "--print-to-file", "after_move:%(title)s", titlePath)
	}

	return append(args, "ytsearch:"+query)
}

// findExistingTracks lists every mp3 under root, at any depth, keyed by
// lowercased base name. Lowercasing matters because macOS and Windows treat
// "Touch.mp3" and "touch.mp3" as the same file.
//
// Errors are swallowed deliberately: a directory that cannot be read means
// "assume nothing is here", which costs a re-download. Failing the whole run
// because one subfolder was unreadable would be a far worse trade.
func findExistingTracks(root string) map[string]string {
	existing := make(map[string]string)

	filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return nil
		}

		if strings.EqualFold(filepath.Ext(path), ".mp3") {
			existing[strings.ToLower(filepath.Base(path))] = path
		}

		return nil
	})

	return existing
}

// Has reports whether a track is already downloaded, anywhere under OutputDir.
// It takes the bare name as spotify.Track.FileName returns it and adds the
// extension itself, so callers never need to know the output format.
func (d *AudioDownloader) Has(fileName string) bool {
	return d.PathOf(fileName) != ""
}

// PathOf returns where an already-downloaded track actually lives, which is not
// derivable from OutputDir - it may have been filed into a genre subfolder by
// hand. Empty if we don't have it.
func (d *AudioDownloader) PathOf(fileName string) string {
	return d.existing[strings.ToLower(fileName+".mp3")]
}

// RefreshExisting brings a track we already have back in line with Spotify,
// without re-downloading it. Returns whether anything actually changed.
//
// The order is deliberate and is why this is one method rather than two:
// retagging rewrites the whole file, so a timestamp set before it would be
// thrown away.
//
// Tags are only rewritten when asked, because that rewrite costs the file's
// whole size in I/O. The timestamp is always corrected, because it is an inode
// update and effectively free - but only when it is actually wrong, so a second
// run over an already-correct library touches nothing and backup tools have no
// reason to re-transfer gigabytes.
func (d *AudioDownloader) RefreshExisting(req Request, retag bool) (bool, error) {
	path := d.PathOf(req.FileName)
	if path == "" {
		return false, nil
	}

	changed := false

	// Retagging first, always. It rewrites the whole file, so a timestamp set
	// before it would be thrown away.
	if retag {
		if err := d.addTagsToFile(path, req.Tags); err != nil {
			return false, err
		}
		changed = true
	}

	if req.AddedAt.IsZero() {
		return changed, nil
	}

	info, err := os.Stat(path)
	if err != nil {
		return changed, fmt.Errorf("checking %s: %w", filepath.Base(path), err)
	}

	// Already correct, so touch nothing - that is what keeps a second run over
	// an unchanged library free.
	if info.ModTime().Equal(req.AddedAt) {
		return changed, nil
	}

	if err := os.Chtimes(path, req.AddedAt, req.AddedAt); err != nil {
		return changed, fmt.Errorf("stamping %s: %w", filepath.Base(path), err)
	}

	return true, nil
}

func (d *AudioDownloader) DownloadBestAudio(req Request) error {
	filepathForDownloader := filepath.Join(d.OutputDir, req.FileName)

	// Failing to make the temp file only costs the version check, so the error
	// is deliberately ignored - titlePath stays empty and the flag is skipped.
	titlePath := ""
	if titleFile, err := os.CreateTemp("", "trackorganiser-title-*"); err == nil {
		titlePath = titleFile.Name()
		titleFile.Close()
		defer os.Remove(titlePath)
	}

	cmd := exec.Command(
		d.YtdlpPath,
		ytdlpArgs(filepathForDownloader, req.Query, d.jsRuntime, titlePath, d.useCookies)...,
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

	if err := d.addTagsToFile(downloadedPath, req.Tags); err != nil {
		return err
	}

	// Stamped after tagging, because tagging rewrites the file and would undo
	// it. The error is ignored on purpose: a wrong timestamp is cosmetic, and
	// failing a download that otherwise worked over it would be absurd. A zero
	// time means nothing supplied one, and stamping that would date the file to
	// year 1.
	if !req.AddedAt.IsZero() {
		os.Chtimes(downloadedPath, req.AddedAt, req.AddedAt)
	}

	// Checked before the duration, because it is the more meaningful signal: a
	// wrong remix is wrong, whereas a length difference is often just a radio
	// edit that is perfectly usable.
	if youtubeTitle := readTitleFile(titlePath); youtubeTitle != "" {
		if !versionMatches(req.Tags.Title, youtubeTitle) {
			return fmt.Errorf("%w: wanted %q, got %q", ErrVersionMismatch, req.Tags.Title, youtubeTitle)
		}
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

func (d *AudioDownloader) addTagsToFile(path string, tags Tags) error {
	tag, err := id3v2.Open(path, id3v2.Options{Parse: true})
	if err != nil {
		return fmt.Errorf("Error while opening mp3 file %q: %w ", tags.Title, err)
	}
	defer tag.Close()

	tag.SetAlbum(tags.Album)
	tag.SetArtist(tags.Artist)
	tag.SetTitle(tags.Title)

	alreadyHasArt := len(tag.GetFrames(tag.CommonID("Attached picture"))) > 0
	if tags.CoverArtURL != "" && !alreadyHasArt {
		rawIMGData, err := d.cache.lookUp(tags.CoverArtURL)
		if err == nil {
			tag.AddAttachedPicture(id3v2.PictureFrame{
				Encoding:    id3v2.EncodingUTF8,
				MimeType:    "image/jpeg",
				PictureType: id3v2.PTFrontCover,
				Picture:     rawIMGData,
			})
		}
	}

	if err = tag.Save(); err != nil {
		return fmt.Errorf("tagging %q: %w", tags.Title, err)
	}

	return nil
}
