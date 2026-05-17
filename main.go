package main

import (
	"fmt"
	"os"
	"github.com/Nat-Umeni/trackorganiser/spotify"
	"github.com/Nat-Umeni/trackorganiser/downloader"
	"github.com/joho/godotenv"
)

func init() {
	if err := godotenv.Load(); err != nil {
		fmt.Println("Failed to load env file properly")
		os.Exit(1)
	}
}

func main() {
	_, err := spotify.NewClient(os.Getenv("SPOTIFY_CLIENT_ID"), os.Getenv("SPOTIFY_CLIENT_SECRET"))
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

    dl := downloader.NewAudioDownloader("./downloads")
    if err := dl.DownloadBestAudio("Daft Punk - Something About Us"); err != nil {
        fmt.Println("Download error: %w", err)
		os.Exit(1)
		return
    }
}