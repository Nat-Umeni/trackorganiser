package main

import (
	"fmt"
	"github.com/Nat-Umeni/trackorganiser/spotify"
	"github.com/joho/godotenv"
	"os"
)

func init() {
	if err := godotenv.Load(); err != nil {
		fmt.Println("Failed to load env file properly")
		os.Exit(1)
	}
}

func main() {
	spotifyClient, err := spotify.NewClient(os.Getenv("SPOTIFY_CLIENT_ID"), os.Getenv("SPOTIFY_CLIENT_SECRET"))
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	// dl, err := downloader.NewAudioDownloader("./downloads")
	// if err := dl.DownloadBestAudio("Bodies Drowning Pool"); err != nil {
	//     fmt.Println("Download error: %w", err)
	// 	os.Exit(1)
	// 	return
	// }

	playlists, err := spotifyClient.FetchPlaylists()
	if err != nil {
		fmt.Println("Error fetching playlists:", err)
		return
	}
	for _, p := range playlists {
		fmt.Printf("  %s (%s) – %d tracks\n", p.Name, p.ID, p.Tracks.Total)
	}
}
