package main

import (
	"encoding/json"
	"fmt"
	"github.com/joho/godotenv"
	"io"
	"net/http"
	"net/url"
	"os"
)

const tokenURL = "https://accounts.spotify.com/api/token"
var auth *SpotifyTokenResponse

type SpotifyTokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int    `json:"expires_in"`
}

// type AllowedRequestMethods

func init() {
	if err := godotenv.Load(); err != nil {
		fmt.Println("Failed to load env file properly")
		os.Exit(1)
	}

	auth, err := getSpotifyAccessToken()
	if err != nil {
		fmt.Printf("\nFailed to get token: \n%v", err)
		os.Exit(1)
	}

	fmt.Printf("Access Token: %s\n", auth.AccessToken)
	fmt.Printf("Token Type:   %s\n", auth.TokenType)
	fmt.Printf("Expires In:   %d seconds\n", auth.ExpiresIn)
}

func main() {
	
}

func getSpotifyAccessToken() (*SpotifyTokenResponse, error) {
	clientID := os.Getenv("SPOTIFY_CLIENT_ID")
	clientSecret := os.Getenv("SPOTIFY_CLIENT_SECRET")

	if clientID == "" || clientSecret == "" {
		return nil, fmt.Errorf("SPOTIFY_CLIENT_ID and SPOTIFY_CLIENT_SECRET must be set")
	}

	data := url.Values{}
	data.Set("grant_type", "client_credentials")
	data.Set("client_id", clientID)
	data.Set("client_secret", clientSecret)

	resp, err := http.PostForm(tokenURL, data)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Spotify API error (status %d): %s", resp.StatusCode, string(body))
	}

	var tokenResp SpotifyTokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, fmt.Errorf("parsing JSON: %w", err)
	}

	return &tokenResp, nil
}

// func callSpotify(endpoint string, method )
