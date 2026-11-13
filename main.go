package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/Nat-Umeni/trackorganiser/config"
	"github.com/Nat-Umeni/trackorganiser/downloader"
	"github.com/Nat-Umeni/trackorganiser/spotify"
	"github.com/Nat-Umeni/trackorganiser/updater"
	"github.com/joho/godotenv"
)

const maxJobs = 16

// version is overridden at link time by build.sh with -ldflags -X. The literal
// default means a plain `go build` still works and reads honestly.
var version = "dev"

// hiddenFlags are accepted but kept out of -h. The callback port only works
// against a redirect URI registered in the Spotify dashboard, so offering it
// would mostly generate confusing failures.
var hiddenFlags = map[string]bool{"port": true}

func init() {
	// A missing .env is the normal case for anyone but me - the client
	// ID comes from the config file instead. Nothing is printed, because
	// announcing it and then failing one line later is what made the old
	// startup read like a bug.
	_ = godotenv.Load()
}

// writeUsage replaces flag's own output so hiddenFlags can be left out. Giving a
// flag an empty usage string does not hide it, it just shows a blank
// description. Takes the set so a test can pass its own.
func writeUsage(set *flag.FlagSet) {
	out := set.Output()
	fmt.Fprintf(out, "Usage of %s:\n", os.Args[0])

	set.VisitAll(func(f *flag.Flag) {
		if hiddenFlags[f.Name] {
			return
		}

		fmt.Fprintf(out, "  -%s\n    \t%s", f.Name, f.Usage)
		if f.DefValue != "" && f.DefValue != "false" {
			fmt.Fprintf(out, " (default %s)", f.DefValue)
		}
		fmt.Fprintln(out)
	})
}

// resolveClientID checks the flag, then the config file, then the environment,
// and saves anything passed by flag. The flag wins so a wrong saved value can be
// fixed by re-running rather than editing JSON by hand.
func resolveClientID(flagValue string) string {
	if trimmed := strings.TrimSpace(flagValue); trimmed != "" {
		if err := config.SaveClientID(trimmed); err != nil {
			// Not fatal: the ID works for this run, it just will not persist.
			fmt.Printf("Couldn't save the client ID for next time: %v\n", err)
		}
		return trimmed
	}

	if saved := config.LoadClientID(); saved != "" {
		return saved
	}

	return strings.TrimSpace(os.Getenv("SPOTIFY_CLIENT_ID"))
}

// clientIDHelp is printed when there is no client ID to be found. It has to name
// the exact command, because the person reading it has been handed a binary and
// an ID and nothing else.
func clientIDHelp() string {
	location := "your user config directory"
	if dir, err := config.ConfigDir(); err == nil {
		location = dir
	}

	// The "./" prefix matters: without it PowerShell refuses to run a binary in
	// the current directory, and so do sh and bash. Copy-pasteable beats tidy
	// for a message whose whole job is unblocking someone.
	command := "." + string(os.PathSeparator) + filepath.Base(os.Args[0])

	return fmt.Sprintf(`This needs a Spotify client ID before it can read your playlists. Ask me for it, then run:

  %s -client-id THE-ID-I-GAVE-YOU

That only needs doing once. It is saved in %s and picked up automatically
from then on.`, command, location)
}

// shouldOfferUpdate decides whether to ask about latest. A version once said no
// to is never asked about again, but the next one after it is - declining
// v0.2.0 shouldn't mean never hearing about v0.3.0.
func shouldOfferUpdate(current, latest, declined string) bool {
	return latest != declined && updater.IsNewer(current, latest)
}

// checkForUpdate offers the newest GitHub release when this binary is older, and
// installs it on a yes. It returns true when the binary was replaced, meaning
// this run should stop - the process is still the old version, and carrying on
// would quietly do a whole run on the code that was just updated away.
//
// Nothing here is fatal. Being offline, GitHub refusing, or a failed download
// all just mean carrying on with the version already here.
func checkForUpdate() bool {
	// A plain `go build` or an untagged build has nothing to compare against,
	// and shouldn't be offered a release over the top of local changes.
	if !updater.IsRelease(version) {
		return false
	}

	latest, err := updater.LatestVersion()
	if err != nil {
		fmt.Printf("Couldn't check for updates: %v\n", err)
		return false
	}

	if !shouldOfferUpdate(version, latest, config.LoadDeclinedVersion()) {
		return false
	}

	answer, err := readLine(fmt.Sprintf("Version %s is out (you have %s). Update now? [Y/n] ", latest, version))
	if err != nil {
		fmt.Printf("Couldn't read your answer, carrying on with %s: %v\n", version, err)
		return false
	}

	if strings.EqualFold(answer, "n") || strings.EqualFold(answer, "no") {
		if err := config.SaveDeclinedVersion(latest); err != nil {
			// Not fatal: it just asks again next time.
			fmt.Printf("Couldn't save that, so it will ask again next time: %v\n", err)
		} else {
			fmt.Printf("OK, it won't ask about %s again.\n", latest)
		}
		return false
	}

	fmt.Printf("Downloading %s...\n", latest)
	if err := updater.Apply(latest); err != nil {
		fmt.Printf("Update failed, carrying on with %s: %v\n", version, err)
		return false
	}

	fmt.Printf("Updated to %s. Run it again to use the new version.\n", latest)
	return true
}

// ErrDownloadDirDeclined means the user chose not to create the download
// directory. It is a normal outcome rather than a failure, so callers should
// match it with errors.Is and exit quietly.
var ErrDownloadDirDeclined = errors.New("no download directory to save to")

// stdin is created once and reused. A bufio.Scanner buffers ahead, so making a
// new one per prompt can swallow input that has already been read.
var stdin = bufio.NewScanner(os.Stdin)

func main() {
	var jobs int
	var dryRun bool
	var noRetag bool
	var clientIDFlag string
	var showVersion bool
	var callbackPort int
	flag.IntVar(&jobs, "jobs", 4, "how many tracks to download at once (1-16) - higher risks rate limiting")
	flag.BoolVar(&dryRun, "dry-run", false, "list the filename each track would get without downloading anything")
	flag.BoolVar(&noRetag, "no-retag", false, "skip rewriting ID3 tags on tracks already downloaded (much faster: retagging costs each file's full size in I/O)")
	flag.StringVar(&clientIDFlag, "client-id", "", "your Spotify client ID - only needed once, it gets saved")
	flag.BoolVar(&showVersion, "version", false, "print the version and exit")
	flag.IntVar(&callbackPort, "port", spotify.CallbackPort, "")
	flag.Usage = func() { writeUsage(flag.CommandLine) }
	flag.Parse()
	jobs = clampJobs(jobs)
	spotify.CallbackPort = callbackPort

	if showVersion {
		fmt.Println(version)
		return
	}

	// Before everything else, including the client ID, so a fix for whatever
	// is stopping someone reaches them even if they can't get past it.
	updater.CleanupOld()
	if checkForUpdate() {
		return
	}

	clientID := resolveClientID(clientIDFlag)
	if clientID == "" {
		fmt.Println(clientIDHelp())
		os.Exit(1)
	}

	ytdlpPath, err := downloader.FindYtDlp()
	if errors.Is(err, downloader.ErrYtDlpMissing) {
		answer, readErr := readLine("yt-dlp isn't installed, download it? [Y/n]")
		if readErr != nil {
			log.Fatal("failed to determine your input: ", readErr)
		}

		if strings.EqualFold(answer, "n") || strings.EqualFold(answer, "no") {
			fmt.Println("Stopping")
			return
		}

		ytdlpPath, err = downloader.InstallYtDlp()
		if err != nil {
			log.Fatalf("failed to install YtDlp dependency: %v", err)
		}
	}

	// Alert for non cannot find ytdlp errs
	if err != nil {
		log.Fatal("failed to find ytdlp for an unknown reason: ", err)
	}

	ensureFfmpeg()

	reportYtdlpExtras()

	// Set up Spotify and collect playlists
	client, err := spotify.NewClient(clientID)
	if err != nil {
		log.Fatal("Failed to create Spotify client:", err)
	}

	playlists, err := client.FetchPlaylists()
	if err != nil {
		log.Fatal("Error fetching playlists:", err)
	}

	// The owner is shown because only playlists you own or collaborate on can be
	// read - anything else returns 403. Two playlists with near-identical names,
	// one yours and one followed, are otherwise indistinguishable in this list.
	for index, playlist := range playlists {
		fmt.Printf("%d - %s  (%s)\n", index+1, playlist.Name, playlist.PlaylistOwner.DisplayName)
	}
	userInput, playlistInputErr := readLine("\nType the numbers of the playlists you would like to save to disk, separated by commas OR by dashes. IE: 1,3,5 OR 4-7 \n\n")

	if playlistInputErr != nil {
		log.Fatal("Failed to read input from user: ", playlistInputErr)
	}

	selectedPlaylists, err := parsePlaylistSelection(userInput, len(playlists))
	if err != nil {
		log.Fatal("Failed to determine selected playlists: ", err)
	}

	if len(selectedPlaylists) == 0 {
		log.Fatal("No playlists selected")
	}

	fmt.Println("You chose: ")

	// Confirm correct playlist choices
	var playlistsToGet []spotify.Playlist
	for _, selectedPlaylistIndex := range selectedPlaylists {
		playlistsToGet = append(playlistsToGet, playlists[selectedPlaylistIndex])
		fmt.Printf("\n%v", playlists[selectedPlaylistIndex].Name)
	}

	// Grab the download path and verify it's reachable
	downloadPathInput, downloadPathInputErr := readLine(downloadDirPrompt())
	if downloadPathInputErr != nil {
		log.Fatal("Failed to gather input on prefered download path: ", downloadPathInputErr)
	}

	// Remembered for next time, unless this is only a dry run.
	downloadPath, err := chooseDownloadDir(downloadPathInput, !dryRun)
	switch {
	case errors.Is(err, ErrDownloadDirDeclined):
		fmt.Println("Nothing saved.")
		return
	case err != nil:
		log.Fatal(err)
	}

	fmt.Printf("\nYou chose to output to: %s\n\nPulling in tracks from your chosen playlists\n", downloadPath)

	tracksByPlaylist := make(map[string][]spotify.Track)
	for _, currentPlaylist := range playlistsToGet {
		tracks, err := client.FetchPlaylistTracks(currentPlaylist)
		if err != nil {
			fmt.Printf("Skipped %s: %v\n", currentPlaylist.Name, err)
			continue
		}

		cleanPlaylistName := currentPlaylist.FolderName()
		tracksByPlaylist[cleanPlaylistName] = tracks
		fmt.Printf("%s: %d of %d\n", cleanPlaylistName, len(tracks), currentPlaylist.Items.Total)
	}

	// TEMP(selection-log): records which YouTube result each download picked.
	selectionLogPath := ""
	if !dryRun {
		if logFile, path, err := openSelectionLog(); err != nil {
			fmt.Printf("Couldn't open the selection log: %v\n", err)
		} else {
			defer logFile.Close()
			downloader.SelectionLog = logFile
			selectionLogPath = path
			fmt.Printf("Logging which YouTube result each download picks to %s\n", path)
		}
	}

	// Collected across every playlist and printed at the end. An inline warning
	// is lost among hundreds of tracks; a short list afterwards is actionable.
	var mismatches, notFound []string
	skipped := 0
	refreshed := 0

	for playlistName, playlistTracks := range tracksByPlaylist {
		playlistPath := filepath.Join(downloadPath, playlistName)

		dl, downloaderErr := downloader.NewAudioDownloader(playlistPath, ytdlpPath)
		if downloaderErr != nil {
			log.Fatal("Failed to set up downloader on that path: ", downloaderErr)
		}

		var toDownload []spotify.Track

		for index, track := range playlistTracks {
			// Checked above the dry run so a dry run reports what would really
			// happen. The scan is recursive, so a track filed into a genre
			// subfolder by hand still counts as downloaded.
			if dl.Has(track.FileName()) {
				skipped++

				// Already on disk, but its timestamp may predate this being
				// recorded at all, and its tags may be stale. Correcting in
				// place costs no download - and without this, a library only
				// comes into line one deleted file at a time.
				if !dryRun {
					changed, err := dl.RefreshExisting(buildRequest(track), !noRetag)
					if err != nil {
						fmt.Printf("Couldn't refresh %s: %v\n", track.FileName(), err)
					} else if changed {
						refreshed++
					}
				}

				continue
			}

			if dryRun {
				fmt.Printf("\nWould have downloaded track: %d - %s as %s\n\n", index, track.Name, track.FileName())
				continue
			}

			toDownload = append(toDownload, track)
		}

		playlistMismatches, playlistNotFound := downloadTracks(dl, toDownload, jobs)
		mismatches = append(mismatches, playlistMismatches...)
		notFound = append(notFound, playlistNotFound...)
	}

	if skipped > 0 {
		fmt.Printf("\nSkipped %d track(s) already downloaded.\n", skipped)
	}

	if refreshed > 0 {
		fmt.Printf("Corrected %d existing track(s) in place.\n", refreshed)
	}

	if len(mismatches) > 0 {
		fmt.Printf("\n%d track(s) may not be the right version, worth a listen:\n", len(mismatches))
		for _, mismatch := range mismatches {
			fmt.Printf("  %s\n", mismatch)
		}
	}

	if len(notFound) > 0 {
		fmt.Printf("\n%d track(s) not found on YouTube - nothing saved, it will try again next run:\n", len(notFound))
		for _, missing := range notFound {
			fmt.Printf("  %s\n", missing)
		}
	}

	// TEMP(selection-log)
	if selectionLogPath != "" {
		fmt.Printf("\nSelection log: %s\n", selectionLogPath)
	}
}

// openSelectionLog creates a fresh log beside the cached yt-dlp, so each run's
// log only holds that run.
//
// TEMP(selection-log): remove once the wrong-song retry is proven.
func openSelectionLog() (*os.File, string, error) {
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return nil, "", err
	}

	dir := filepath.Join(cacheDir, "trackorganiser")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, "", err
	}

	path := filepath.Join(dir, "selection.log")
	file, err := os.Create(path)
	if err != nil {
		return nil, "", err
	}

	return file, path, nil
}

// bucket is where a download's outcome is reported at the end of the run.
type bucket int

const (
	bucketOK bucket = iota

	// A file was saved but may not be the right cut - worth a listen.
	bucketListen

	// Nothing was saved, so there is nothing to listen to. The next run tries
	// again, since the folder is the record of what's downloaded.
	bucketNotFound

	// yt-dlp itself broke - printed as it happens.
	bucketFailed
)

func (b bucket) String() string {
	return [...]string{"ok", "worth a listen", "not found", "failed"}[b]
}

// reportBucket sorts a download's error into where it is reported. Length on
// its own only ever lands in "worth a listen": an extended mix or a radio edit
// is still usable, and duration was measured not to predict correctness.
func reportBucket(err error) bucket {
	switch {
	case err == nil:
		return bucketOK
	case errors.Is(err, downloader.ErrSongMismatch),
		errors.Is(err, downloader.ErrNoResults):
		return bucketNotFound
	case errors.Is(err, downloader.ErrArtistUnconfirmed),
		errors.Is(err, downloader.ErrDurationMismatch),
		errors.Is(err, downloader.ErrVersionMismatch):
		return bucketListen
	default:
		return bucketFailed
	}
}

// downloadTracks fetches up to jobs tracks at a time. It returns a line for
// each track saved but worth a listen, and one for each it couldn't find at
// all. Most of a download is spent waiting on the network, which is why
// running several at once helps. The ceiling is YouTube's tolerance for
// parallel requests from one IP, not the CPU.
func downloadTracks(dl *downloader.AudioDownloader, tracks []spotify.Track, jobs int) (mismatches, notFound []string) {

	// A buffered channel used as a counting semaphore: a car park with `jobs`
	// spaces. Sending takes a ticket, receiving hands it back, and a send blocks
	// once every ticket is out. The element type is struct{} because the values
	// are never read - only whether a slot is occupied - and an empty struct
	// takes no memory.
	downloadSlots := make(chan struct{}, jobs)

	// Tracks the goroutines that haven't finished, so the last one can be waited
	// for before returning.
	var pendingDownloads sync.WaitGroup

	// Named after what it guards: mismatches and notFound are appended to from
	// several goroutines at once, which is a data race without this.
	var mismatchesLock sync.Mutex

	// Counted on completion rather than on start. With several running at once
	// the order is unpredictable, but the count is monotonic, so it tells you how
	// far through the run you are.
	var completed atomic.Int64

	for _, track := range tracks {
		// The barrier, and it belongs out here rather than inside the goroutine.
		// Inside, the loop would launch every goroutine immediately and they
		// would queue up internally - the limit would do nothing.
		downloadSlots <- struct{}{}
		pendingDownloads.Go(func() {
			// Deferred so the slot comes back however this exits. Releasing at
			// the top instead would free the ticket before doing any work, and
			// every track would start at once.
			defer func() { <-downloadSlots }()

			err := dl.DownloadBestAudio(buildRequest(track))

			fmt.Printf("%d/%d - %s\n", completed.Add(1), len(tracks), track.FileName())

			line := fmt.Sprintf("%s — %v", track.FileName(), err)

			// Locked around the appends only. Holding it across the download
			// above would serialise everything and undo the concurrency.
			switch reportBucket(err) {
			case bucketListen:
				// Not a failure - the file downloaded and was tagged. Keep it and
				// flag it, because an extended mix or a live version is often
				// worth having.
				mismatchesLock.Lock()
				mismatches = append(mismatches, line)
				mismatchesLock.Unlock()
			case bucketNotFound:
				mismatchesLock.Lock()
				notFound = append(notFound, line)
				mismatchesLock.Unlock()
			case bucketFailed:
				fmt.Printf("Failed: %s\n", line)
			}
		})
	}

	// Without this the function would return as soon as everything was launched,
	// handing back empty slices while downloads were still running.
	pendingDownloads.Wait()

	return mismatches, notFound
}

func clampJobs(n int) int {
	if n < 1 {
		return 1
	}

	if n > maxJobs {
		return maxJobs
	}

	return n
}

func buildRequest(track spotify.Track) downloader.Request {
	artists := make([]string, 0, len(track.Artists))
	for _, artist := range track.Artists {
		artists = append(artists, artist.Name)
	}

	return downloader.Request{
		Query:           track.BuildSearchQuery(),
		FallbackQueries: track.BuildFallbackQueries(),
		Artists:         artists,
		FileName:        track.FileName(),
		Tags: downloader.Tags{
			Artist:      track.JoinArtistNames(),
			Title:       track.Name,
			Album:       track.Album.Name,
			CoverArtURL: track.Album.GetAlbumCoverArtURL(),
		},
		DurationMS: track.DurationMS,
		AddedAt:    track.AddedAt,
	}
}

func readLine(prompt string) (string, error) {
	fmt.Print(prompt)

	// Scan reads one line and reports false at the end of input, which covers
	// both a read error and the user pressing Ctrl+D.
	if !stdin.Scan() {
		if err := stdin.Err(); err != nil {
			return "", err
		}
		return "", fmt.Errorf("no input given")
	}

	return strings.TrimSpace(stdin.Text()), nil
}

func parsePlaylistSelection(input string, max int) ([]int, error) {
	parts := strings.Split(input, ",")
	var selected []int
	seen := make(map[int]bool)

	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		// A part is either a single number ("3") or a range ("4-7"). Treating a
		// single number as the range 3-3 means one loop handles both.
		firstText, lastText := part, part
		if before, after, isRange := strings.Cut(part, "-"); isRange {
			firstText, lastText = before, after
		}

		first, err := parseIndex(firstText, max)
		if err != nil {
			return nil, err
		}
		last, err := parseIndex(lastText, max)
		if err != nil {
			return nil, err
		}
		if first > last {
			return nil, fmt.Errorf("range %q counts backwards", part)
		}

		for index := first; index <= last; index++ {
			if seen[index] {
				continue
			}
			seen[index] = true
			selected = append(selected, index)
		}
	}
	return selected, nil
}

// parseIndex turns one number as displayed to the user (1-based) into an index
// into the playlists slice (0-based).
func parseIndex(text string, max int) (int, error) {
	num, err := strconv.Atoi(strings.TrimSpace(text))
	if err != nil {
		return 0, fmt.Errorf("invalid number: %q", text)
	}
	if num < 1 || num > max {
		return 0, fmt.Errorf("number %d out of range (1–%d)", num, max)
	}
	return num - 1, nil
}

// downloadDirPrompt asks where to save, naming the actual default - otherwise
// pressing enter would put the downloads somewhere the prompt never mentioned.
func downloadDirPrompt() string {
	defaultDir, err := defaultDownloadDir()
	if err != nil {
		defaultDir = "~/Downloads"
	}

	return fmt.Sprintf("\n\nWhat location would you like to save the downloads to? Default is %s\n", defaultDir)
}

// chooseDownloadDir resolves the folder to download to and, when remember is
// set, saves it as next run's default. What's saved is the resolved absolute
// path, so the default is exact however it was typed.
//
// Only a folder that was actually chosen is saved: a declined "create it?"
// returns before saving, so last run's choice stands. The caller passes
// remember=false on a dry run, which is for seeing what would happen, not for
// changing anything.
func chooseDownloadDir(input string, remember bool) (string, error) {
	downloadPath, err := ensureOutputLocationExists(input)
	if err != nil {
		return "", err
	}

	if remember && downloadPath != config.LoadDownloadDir() {
		if err := config.SaveDownloadDir(downloadPath); err != nil {
			// Not fatal: the folder works for this run, it just won't be the
			// default next time - the same as a client ID that won't save.
			fmt.Printf("Couldn't remember that folder for next time: %v\n", err)
		}
	}

	return downloadPath, nil
}

// defaultDownloadDir is what pressing enter at the folder prompt means: the
// folder chosen last run, or ~/Downloads the first time.
func defaultDownloadDir() (string, error) {
	if saved := config.LoadDownloadDir(); saved != "" {
		return saved, nil
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("finding your home directory: %w", err)
	}

	return filepath.Join(homeDir, "Downloads"), nil
}

func ensureOutputLocationExists(downloadPath string) (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("finding your home directory: %w", err)
	}

	// A saved folder goes through everything below like a typed one - so one
	// on a drive that isn't mounted gets the "create it?" prompt, not a
	// confusing failure later.
	if downloadPath == "" {
		downloadPath, err = defaultDownloadDir()
		if err != nil {
			return "", err
		}
	}

	// The shell expands a leading tilde, but a path typed at our own prompt
	// arrives with the tilde intact, and filepath.Abs would treat it as an
	// ordinary directory name. "~user" is left alone because Go cannot look up
	// another user's home directory portably.
	if downloadPath == "~" || strings.HasPrefix(downloadPath, "~/") {
		downloadPath = filepath.Join(homeDir, downloadPath[1:])
	}

	safeDownloadPath, err := filepath.Abs(downloadPath)
	if err != nil {
		return "", fmt.Errorf("resolving %q to an absolute path: %w", downloadPath, err)
	}

	// Stat failing does not only mean "missing" - permission problems and dead
	// mounts land here too, and creating a directory would not fix those.
	info, err := os.Stat(safeDownloadPath)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		answer, answerErr := readLine(fmt.Sprintf("%s doesn't exist. Create it? [y/N] ", safeDownloadPath))
		if answerErr != nil {
			return "", fmt.Errorf("reading your answer: %w", answerErr)
		}
		if !strings.EqualFold(answer, "y") && !strings.EqualFold(answer, "yes") {
			return "", ErrDownloadDirDeclined
		}
		if err := os.MkdirAll(safeDownloadPath, 0755); err != nil {
			return "", fmt.Errorf("creating %s: %w", safeDownloadPath, err)
		}
	case err != nil:
		return "", fmt.Errorf("checking the download path: %w", err)
	case !info.IsDir():
		return "", fmt.Errorf("the download path is not a directory: %s", safeDownloadPath)
	}

	return safeDownloadPath, nil
}

// ensureFfmpeg makes ffmpeg and ffprobe runnable, offering to download them the
// way yt-dlp is offered.
//
// It has to run before NewAudioDownloader, which is what checks for both and
// refuses to start without them.
//
// Nothing here is fatal. A declined or failed download leaves that check to
// report the problem with the install command for the platform, which is the
// right message anyway - and on macOS there is no download to offer at all.
func ensureFfmpeg() {
	dir, err := downloader.FindFfmpeg()

	if errors.Is(err, downloader.ErrFfmpegMissing) {
		answer, readErr := readLine(fmt.Sprintf(
			"ffmpeg isn't installed, download it? It's %s. [Y/n]",
			downloader.FfmpegDownloadSize(runtime.GOOS)))
		if readErr != nil {
			fmt.Printf("Couldn't read your answer, carrying on without it: %v\n", readErr)
			return
		}

		if strings.EqualFold(answer, "n") || strings.EqualFold(answer, "no") {
			return
		}

		fmt.Println("Downloading ffmpeg, this takes a minute...")
		dir, err = downloader.InstallFfmpeg()
		if err != nil {
			fmt.Printf("Couldn't install ffmpeg: %v\n", err)
			return
		}

		fmt.Println("ffmpeg installed.")
	} else if err != nil {
		fmt.Printf("Couldn't check for ffmpeg: %v\n", err)
		return
	}

	// Empty when ffmpeg was already on PATH, and AddToPath ignores that.
	downloader.AddToPath(dir)
}

// reportYtdlpExtras says what optional yt-dlp support was found, so a run that
// silently has neither is visible rather than mysterious. Both are survivable
// when missing, which is exactly why they need announcing - the symptoms
// otherwise look like unrelated download failures.
func reportYtdlpExtras() {
	if jsRuntime := downloader.FindJSRuntime(); jsRuntime != "" {
		fmt.Printf("Using %s for YouTube extraction.\n", jsRuntime)
	} else {
		fmt.Println("No JavaScript runtime found (deno, node, quickjs or bun) - some tracks will wrongly report as unavailable.")
	}

	home, _ := os.UserHomeDir()
	if downloader.FirefoxCookiesAvailable(home) {
		fmt.Println("Using Firefox cookies for age-restricted tracks.")
	} else {
		fmt.Println("No Firefox cookies found - age-restricted tracks will fail.")
	}
}
