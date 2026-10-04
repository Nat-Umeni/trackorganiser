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
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/Nat-Umeni/trackorganiser/downloader"
	"github.com/Nat-Umeni/trackorganiser/spotify"
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
		if err := spotify.SaveClientID(trimmed); err != nil {
			// Not fatal: the ID works for this run, it just will not persist.
			fmt.Printf("Couldn't save the client ID for next time: %v\n", err)
		}
		return trimmed
	}

	if saved := spotify.LoadClientID(); saved != "" {
		return saved
	}

	return strings.TrimSpace(os.Getenv("SPOTIFY_CLIENT_ID"))
}

// clientIDHelp is printed when there is no client ID to be found. It has to name
// the exact command, because the person reading it has been handed a binary and
// an ID and nothing else.
func clientIDHelp() string {
	location := "your user config directory"
	if dir, err := spotify.ConfigDir(); err == nil {
		location = dir
	}

	return fmt.Sprintf(`This needs a Spotify client ID before it can read your playlists. Ask me for it, then run:

  %s -client-id THE-ID-I-GAVE-YOU

That only needs doing once. It is saved in %s and picked up automatically
from then on.`, filepath.Base(os.Args[0]), location)
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
	downloadPathInput, downloadPathInputErr := readLine("\n\nWhat location would you like to save the downloads to? Default is ~/Downloads. \n")
	if downloadPathInputErr != nil {
		log.Fatal("Failed to gather input on prefered download path: ", downloadPathInputErr)
	}

	downloadPath, err := ensureOutputLocationExists(downloadPathInput)
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

	// Collected across every playlist and printed at the end. An inline warning
	// is lost among hundreds of tracks; a short list afterwards is actionable.
	var mismatches []string
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

		mismatches = append(mismatches, downloadTracks(dl, toDownload, jobs)...)
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
}

// downloadTracks fetches up to jobs tracks at a time and returns a line for each
// one whose length didn't match Spotify. Most of a download is spent waiting on
// the network, which is why running several at once helps. The ceiling is
// YouTube's tolerance for parallel requests from one IP, not the CPU.
func downloadTracks(dl *downloader.AudioDownloader, tracks []spotify.Track, jobs int) []string {
	var mismatches []string

	// A buffered channel used as a counting semaphore: a car park with `jobs`
	// spaces. Sending takes a ticket, receiving hands it back, and a send blocks
	// once every ticket is out. The element type is struct{} because the values
	// are never read - only whether a slot is occupied - and an empty struct
	// takes no memory.
	downloadSlots := make(chan struct{}, jobs)

	// Tracks the goroutines that haven't finished, so the last one can be waited
	// for before returning.
	var pendingDownloads sync.WaitGroup

	// Named after what it guards: mismatches is appended to from several
	// goroutines at once, which is a data race without this.
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

			switch {
			case errors.Is(err, downloader.ErrDurationMismatch),
				errors.Is(err, downloader.ErrVersionMismatch):
				// Not a failure - the file downloaded and was tagged. Keep it and
				// flag it, because an extended mix or a live version is often
				// worth having.

				// Locked around the append only. Holding it across the download
				// above would serialise everything and undo the concurrency.
				mismatchesLock.Lock()
				mismatches = append(mismatches, fmt.Sprintf("%s — %v", track.FileName(), err))
				mismatchesLock.Unlock()
			case err != nil:
				fmt.Printf("Failed: %s — %v\n", track.FileName(), err)
			}
		})
	}

	// Without this the function would return as soon as everything was launched,
	// handing back an empty slice while downloads were still running.
	pendingDownloads.Wait()

	return mismatches
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
	return downloader.Request{
		Query:    track.BuildSearchQuery(),
		FileName: track.FileName(),
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

func ensureOutputLocationExists(downloadPath string) (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("finding your home directory: %w", err)
	}

	if downloadPath == "" {
		downloadPath = filepath.Join(homeDir, "Downloads")
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
