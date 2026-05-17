package main

import (
    "fmt"
    "log"
    "os"

    "github.com/Nat-Umeni/trackorganiser/spotify"
    "github.com/joho/godotenv"
)

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

    client, err := spotify.NewClient(clientID)
    if err != nil {
        log.Fatal("Failed to create Spotify client:", err)
    }

    playlists, err := client.FetchPlaylists()
    if err != nil {
        log.Fatal("Error fetching playlists:", err)
    }

    for _, p := range playlists {
        fmt.Printf("🎵 %s – %d tracks\n", p.Name, p.Tracks.Total)
    }
}