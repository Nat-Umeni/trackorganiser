package spotify

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// Playlist is a single playlist in the user's library.
type Playlist struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Description string     `json:"description"`
	Public      bool       `json:"public"`
	Owner       Owner      `json:"owner"`
	Tracks      TracksInfo `json:"tracks"`
}

// Owner represents the playlist creator.
type Owner struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
}

// TracksInfo holds just the total count
type TracksInfo struct {
	Total int `json:"total"`
}

// playlistsResponse is the API wrapper
type playlistsResponse struct {
	Items []Playlist `json:"items"`
	Total int        `json:"total"`
	Limit int        `json:"limit"`
	Next  string     `json:"next"`
}

func (c *Client) FetchPlaylists() ([]Playlist, error) {
	if err := c.ensureToken(); err != nil {
		return nil, fmt.Errorf("auth: %w", err)
	}

	resp, err := c.callSpotify("GET", "/me/playlists", nil)
	if err != nil {
		return nil, fmt.Errorf("request: %w", err)
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("Spotify API error %d: %s", resp.StatusCode, string(body))
	}

	var playlistsResponse playlistsResponse
	if err := json.NewDecoder(resp.Body).Decode(&playlistsResponse); err != nil {
		return nil, fmt.Errorf("decode %w", err)
	}

	return playlistsResponse.Items, nil
}
