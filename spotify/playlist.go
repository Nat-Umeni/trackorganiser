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
	ID            string        `json:"id"`
	Name          string        `json:"name"`
	Description   string        `json:"description"`
	Public        bool          `json:"public"`
	PlaylistOwner PlaylistOwner `json:"owner"`
	Items         ItemsInfo     `json:"items"`
}

// Represents the playlist creator.
type PlaylistOwner struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
}

// ItemsInfo holds the total count and a link to the playlist deets
type ItemsInfo struct {
	Total int    `json:"total"`
	Href  string `json:"href"`
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

type Artist struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Href string `json:"href"`
}

type Track struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Artists    []Artist `json:"artists"`
	DurationMS int      `json:"duration_ms"`
}

type PlaylistTrackItem struct {
	IsLocal bool  `json:"is_local"`
	Item    Track `json:"item"`
}

type playlistTracksResponse struct {
	Items []PlaylistTrackItem `json:"items"`
	Total int                 `json:"total"`
	Limit int                 `json:"limit"`
	Next  string              `json:"next"`
}

func (c *Client) FetchPlaylistTracks(playlist Playlist) ([]Track, error) {
	nextUrl := fmt.Sprintf("/playlists/%s/items?limit=50", playlist.ID)
	var allTracks []Track

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

		var playlistTracksResponse playlistTracksResponse
		err = json.NewDecoder(resp.Body).Decode(&playlistTracksResponse)
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("decode %w", err)
		}

		for _, playlistTrackItem := range playlistTracksResponse.Items {
			if playlistTrackItem.IsLocal || playlistTrackItem.Item.Name == "" {
				continue
			}
			allTracks = append(allTracks, playlistTrackItem.Item)
		}

		nextUrl = playlistTracksResponse.Next

	}

	return allTracks, nil
}
