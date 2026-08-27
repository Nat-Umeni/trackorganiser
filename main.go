package main

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Nat-Umeni/trackorganiser/spotify"
	"github.com/joho/godotenv"
)

// stdin is created once and reused. A bufio.Scanner buffers ahead, so making a
// new one per prompt can swallow input that has already been read.
var stdin = bufio.NewScanner(os.Stdin)

// ErrDownloadDirDeclined means the user chose not to create the download
// directory. It is a normal outcome rather than a failure, so callers should
// match it with errors.Is and exit quietly.
var ErrDownloadDirDeclined = errors.New("no download directory to save to")

func init() {
	if err := godotenv.Load(); err != nil {
		fmt.Println("No .env file, continuing")
	}
}

func main() {
	clientID := os.Getenv("SPOTIFY_CLIENT_ID")
	if clientID == "" {
		log.Fatal("SPOTIFY_CLIENT_ID not set")
	}

	// dl, err := downloader.NewAudioDownloader("./downloads")
	// if err := dl.DownloadBestAudio("Bodies Drowning Pool"); err != nil {
	//     fmt.Println("Download error: %w", err)
	// 	os.Exit(1)
	// 	return
	// }

	// Set up Spotify and collect playlists
	client, err := spotify.NewClient(clientID)
	if err != nil {
		log.Fatal("Failed to create Spotify client:", err)
	}

	playlists, err := client.FetchPlaylists()
	if err != nil {
		log.Fatal("Error fetching playlists:", err)
	}

	for index, playlist := range playlists {
		fmt.Printf("%d - %s\n", index+1, playlist.Name)
	}
	userInput, playlistInputErr := readLine("\nType the numbers of the playlists you would like to save to disk, separated by commas OR by dashes. \n IE: 1,3,5 OR 4-7 \n")

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

	// Confirm correct playlist choices
	fmt.Println("You chose:")
	var playlistsToGet []spotify.Playlist
	for _, selectedPlaylistIndex := range selectedPlaylists {
		playlistsToGet = append(playlistsToGet, playlists[selectedPlaylistIndex])
		fmt.Println(playlists[selectedPlaylistIndex].Name)
	}

	// Grab the download path and verify it's reachable
	downloadPathInput, downloadPathInputErr := readLine("What location would you like to save the downloads to? Default is ~/Downloads.")
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

	fmt.Printf("\nYou chose to output to: %s\n", downloadPath)
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
