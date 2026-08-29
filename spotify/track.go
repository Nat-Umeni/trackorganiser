package spotify

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type Artist struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Href string `json:"href"`
}

type Album struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Href string `json:"href"`
}

type Track struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Artists    []Artist `json:"artists"`
	Album      Album    `json:"album"`
	DurationMS int      `json:"duration_ms"`
}

func (t Track) JoinArtistNames() string {
	fullArtistStringSlice := make([]string, 0, len(t.Artists))
	for _, artist := range t.Artists {
		fullArtistStringSlice = append(fullArtistStringSlice, strings.TrimSpace(artist.Name))
	}

	return strings.Join(fullArtistStringSlice, ", ")
}

func (t Track) BuildSearchQuery() string {
	if len(t.Artists) == 0 {
		return strings.TrimSpace(t.Name)
	}

	return t.Name + " " + t.Artists[0].Name
}

func (t Track) FileName() string {
	name := sanitizeName(t.Name)

	if name == "" {
		id := t.ID
		if len(id) > 5 {
			id = id[len(id)-5:]
		}

		return "track-" + id
	}

	if len(t.Artists) == 0 {
		return name
	}

	return name + " - " + sanitizeName(t.Artists[0].Name)
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
