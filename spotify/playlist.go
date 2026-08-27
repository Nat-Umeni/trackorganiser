package spotify

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
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
	var all []Playlist

	nextUrl := "/me/playlists?limit=50"
	for nextUrl != "" {
		resp, err := c.callSpotify("GET", nextUrl, nil)
		if err != nil {
			return nil, fmt.Errorf("request: %w", err)
		}

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			return nil, fmt.Errorf("Spotify API error %d: %s", resp.StatusCode, string(body))
		}

		var playlistsResponse playlistsResponse
		err = json.NewDecoder(resp.Body).Decode(&playlistsResponse)
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("decode %w", err)
		}

		all = append(all, playlistsResponse.Items...)
		nextUrl = playlistsResponse.Next
	}

	sort.Slice(all, func(i, j int) bool {
		return all[i].Name < all[j].Name
	})

	return all, nil
}
