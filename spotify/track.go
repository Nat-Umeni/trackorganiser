package spotify

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

type Artist struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Href string `json:"href"`
}

type Album struct {
	ID     string  `json:"id"`
	Name   string  `json:"name"`
	Href   string  `json:"href"`
	Images []Image `json:"images"`
}

func (a Album) GetAlbumCoverArtURL() string {
	if len(a.Images) == 0 {
		return ""
	}

	best := a.Images[0]

	for _, currentAlbum := range a.Images[1:] {
		if gapFrom300(currentAlbum.Width) < gapFrom300(best.Width) {
			best = currentAlbum
		}
	}

	return best.URL
}

func gapFrom300(widthAmount int) int {
	gap := widthAmount - 300
	if gap < 0 {
		gap = -gap
	}

	return gap
}

type Image struct {
	URL    string `json:"url"`
	Height int    `json:"height"`
	Width  int    `json:"width"`
}

type Track struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Artists    []Artist `json:"artists"`
	Album      Album    `json:"album"`
	DurationMS int      `json:"duration_ms"`

	// AddedAt is when the track joined the playlist. Spotify puts it on the
	// wrapper rather than the song - one song added to two playlists has two
	// different values - so it never decodes into here directly.
	// FetchPlaylistTracks copies it across when it unwraps, because carrying it
	// on Track is far easier to work with than passing the wrapper around.
	AddedAt time.Time
}

func (t Track) JoinArtistNames() string {
	fullArtistStringSlice := make([]string, 0, len(t.Artists))
	for _, artist := range t.Artists {
		fullArtistStringSlice = append(fullArtistStringSlice, strings.TrimSpace(artist.Name))
	}

	return strings.Join(fullArtistStringSlice, ", ")
}

// BuildSearchQuery is the string handed to yt-dlp's ytsearch:. It uses only the
// first artist - extra names narrow YouTube's match rather than widening it - and
// appends "topic" to bias towards the auto-generated "<Artist> - Topic" channels,
// which host what the label delivered and so match Spotify's master closely.
// Music video uploads tend to carry intros that throw the duration out.
func (t Track) BuildSearchQuery() string {
	if len(t.Artists) == 0 {
		return strings.TrimSpace(t.Name) + " topic"
	}

	return strings.TrimSpace(t.Name) + " " + strings.TrimSpace(t.Artists[0].Name) + " topic"
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
	IsLocal bool      `json:"is_local"`
	Item    Track     `json:"item"`
	AddedAt time.Time `json:"added_at"`
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

			// Spotify sends a bare "Forbidden" with no explanation. It means the
			// playlist is only followed, not owned or collaborated on - reading
			// its items needs one of those. Copying it into your own account is
			// the workaround.
			if resp.StatusCode == http.StatusForbidden {
				return nil, fmt.Errorf(
					"owned by %s, and you can only read playlists you own or collaborate on - make your own copy of it to download it",
					playlist.PlaylistOwner.DisplayName,
				)
			}

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

			// Item is a value, so this is a copy - the response struct is left
			// alone.
			track := playlistTrackItem.Item
			track.AddedAt = playlistTrackItem.AddedAt
			allTracks = append(allTracks, track)
		}

		nextUrl = playlistTracksResponse.Next

	}

	// After the loop, not inside it, or each page would be sorted on its own.
	sort.Slice(allTracks, func(i, j int) bool {
		return addedAfter(allTracks[i], allTracks[j])
	})

	return dedupeByFileName(allTracks), nil
}

// dedupeByFileName drops later entries that would land on the same file as an
// earlier one, keeping the first - which, after sorting, is the most recently
// added.
//
// A track really can appear twice in one playlist with two different added_at
// values, and both map to a single filename. Left in, the pair fights over that
// file's timestamp on every run, and with several downloads in flight both
// could be fetched to the same path at once.
func dedupeByFileName(tracks []Track) []Track {
	seen := make(map[string]bool, len(tracks))
	deduped := make([]Track, 0, len(tracks))

	for _, track := range tracks {
		// Lowercased to match how the downloader recognises existing files:
		// macOS and Windows treat two casings as one file.
		key := strings.ToLower(track.FileName())
		if seen[key] {
			continue
		}

		seen[key] = true
		deduped = append(deduped, track)
	}

	return deduped
}

// addedAfter reports whether a joined the playlist more recently than b, which
// is the "date added, newest first" order Spotify shows by default.
//
// It is a named function rather than an inline closure so it can be tested:
// FetchPlaylistTracks itself cannot be, while callSpotify hardcodes the API base
// URL.
func addedAfter(a, b Track) bool {
	// A track with no timestamp sorts last rather than winning by accident -
	// the zero time predates everything.
	return a.AddedAt.After(b.AddedAt)
}
