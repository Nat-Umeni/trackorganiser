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

// ErrSongMismatch means every search came back with a title that doesn't contain
// the song's name - most likely a different song altogether, not just a
// different cut. The best guess is kept so the caller can report it.
var ErrSongMismatch = errors.New("downloaded what looks like a different song")

// videoInfoTemplate is what yt-dlp prints about the video it picked. Tab
// separated, with the title last, so a title containing anything at all still
// parses. "after_move" matters: --print implies --simulate unless a later stage
// is named, and simulating would download nothing.
const videoInfoTemplate = "after_move:%(id)s\t%(channel)s\t%(duration)s\t%(title)s"

// videoInfo is what yt-dlp recorded about the video a search picked. The title
// drives the song and version checks; the rest is there to make a wrong pick
// traceable.
type videoInfo struct {
	ID       string
	Channel  string
	Duration string
	Title    string
}

// readVideoInfo returns what yt-dlp recorded, or a zero value if anything at all
// went wrong. An unreadable record only means the title can't be checked; the
// mp3 is downloaded and tagged either way.
func readVideoInfo(path string) videoInfo {
	if path == "" {
		return videoInfo{}
	}

	contents, err := os.ReadFile(path)
	if err != nil {
		return videoInfo{}
	}

	line := strings.TrimSpace(string(contents))

	// SplitN, so tabs inside the title stay part of it.
	fields := strings.SplitN(line, "\t", 4)
	if len(fields) < 4 {
		return videoInfo{Title: line}
	}

	return videoInfo{ID: fields[0], Channel: fields[1], Duration: fields[2], Title: fields[3]}
}

// candidate is one search's download, before deciding whether to keep it.
type candidate struct {
	path  string
	query string
	info  videoInfo
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
	Query string

	// FallbackQueries are searched in order when Query's result has a title that
	// doesn't contain the song's name. Empty means no second chance.
	FallbackQueries []string

	// Artists are every credited name, for checking that a result is by one of
	// them. Separate from Tags.Artist, which is the same list joined for the
	// tag and can't be split back reliably - a name can contain a comma.
	Artists []string

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
func ytdlpArgs(outputPath, query, jsRuntime, infoPath string, useCookies bool) []string {
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

	// Records what yt-dlp actually picked, so the song and version can be checked
	// against what Spotify asked for.
	//
	// The path is a temp file rather than one derived from the track name
	// because this argument is parsed as an output template - a track called
	// "100% Pure" would otherwise mangle it.
	if infoPath != "" {
		args = append(args, "--print-to-file", videoInfoTemplate, infoPath)
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

// fetch runs one search and downloads its first result to outBase + ".mp3".
func (d *AudioDownloader) fetch(outBase, query string) (candidate, error) {
	// Failing to make the temp file only costs the title checks, so the error
	// is deliberately ignored - infoPath stays empty and the flag is skipped.
	infoPath := ""
	if infoFile, err := os.CreateTemp("", "trackorganiser-info-*"); err == nil {
		infoPath = infoFile.Name()
		infoFile.Close()
		defer os.Remove(infoPath)
	}

	cmd := exec.Command(d.YtdlpPath, ytdlpArgs(outBase, query, d.jsRuntime, infoPath, d.useCookies)...)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return candidate{}, fmt.Errorf("yt-dlp failed: %v\nOutput: %s", err, string(output))
	}

	path := outBase + ".mp3"

	// A search with no results is not an error to yt-dlp: it exits 0 having
	// downloaded nothing. Without this check addTagsToFile is the first thing to
	// notice the missing file, so a normal not-found reads as a code defect.
	if _, err := os.Stat(path); err != nil {
		return candidate{}, fmt.Errorf("no YouTube results for %q", query)
	}

	return candidate{path: path, query: query, info: readVideoInfo(infoPath)}, nil
}

// DownloadBestAudio searches for the track, downloads it, and tags it.
//
// The first search ends in "topic", which finds the label's own upload when the
// artist has a Topic channel - and finds something unrelated when they don't.
// So when the result's title doesn't contain the song's name, the fallback
// searches are tried in turn, and the first whose title does is kept. When the
// first search is right, which is the usual case, nothing extra runs.
func (d *AudioDownloader) DownloadBestAudio(req Request) (err error) {
	base := filepath.Join(d.OutputDir, req.FileName)
	downloadedPath := base + ".mp3"

	// TEMP(selection-log)
	selection := newSelectionRecord(req)
	defer func() { selection.finish(err) }()

	var picked, firstFound *candidate
	pickedIndex, firstFoundIndex := -1, -1
	var firstErr error

	for index, query := range append([]string{req.Query}, req.FallbackQueries...) {
		// Retries land beside the first attempt rather than on top of it, since
		// --no-overwrites would otherwise refuse, and the first attempt is still
		// the best guess if nothing better turns up.
		outBase := base
		if index > 0 {
			outBase = fmt.Sprintf("%s.try%d", base, index+1)
		}

		found, fetchErr := d.fetch(outBase, query)
		if fetchErr != nil {
			selection.attempt(index, query, videoInfo{}, fetchErr, "") // TEMP(selection-log)
			if firstErr == nil {
				firstErr = fetchErr
			}
			if index > 0 {
				// A failed retry may still have left something behind.
				os.Remove(outBase + ".mp3")
			}
			continue
		}

		titleOK := titleMatches(req.Tags.Title, found.info.Title)
		artistOK := artistMatches(req.Artists, found.info.Title, found.info.Channel)
		matches := titleOK && artistOK
		selection.attempt(index, query, found.info, nil, verdictFor(titleOK, artistOK)) // TEMP(selection-log)

		if matches {
			picked, pickedIndex = &found, index
			break
		}

		// Wrong, but the first thing found stays the fallback - the "topic"
		// search was measured to be the better one in general.
		if firstFound == nil {
			firstFound, firstFoundIndex = &found, index
		} else {
			os.Remove(found.path)
		}
	}

	songMismatch := false
	switch {
	case picked == nil && firstFound == nil:
		// Nothing downloaded at all. The first search's error is the one to
		// report - it is the search the program normally runs.
		return firstErr
	case picked == nil:
		picked, pickedIndex = firstFound, firstFoundIndex
		songMismatch = true
	case firstFound != nil:
		os.Remove(firstFound.path)
	}

	selection.keep(pickedIndex, picked.info) // TEMP(selection-log)

	if picked.path != downloadedPath {
		// Rename replaces an existing file on every platform, so a wrong first
		// attempt sitting at the final path is simply overwritten.
		if err := os.Rename(picked.path, downloadedPath); err != nil {
			os.Remove(picked.path)
			return fmt.Errorf("keeping the result of %q: %w", picked.query, err)
		}
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

	// The strongest signal first: a different song is wrong outright, a
	// different remix nearly always is, whereas a length difference is often
	// just a radio edit that is perfectly usable.
	if songMismatch {
		return fmt.Errorf("%w: wanted %q, got %q", ErrSongMismatch, req.Tags.Title, picked.info.Title)
	}

	if youtubeTitle := picked.info.Title; youtubeTitle != "" {
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
