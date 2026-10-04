package spotify

import (
	"encoding/json"
	"sort"
	"testing"
	"time"
)

func at(day int) time.Time {
	return time.Date(2026, time.August, day, 12, 0, 0, 0, time.UTC)
}

func TestAddedAfter(t *testing.T) {
	tests := []struct {
		name string
		a    Track
		b    Track
		want bool
	}{
		{
			name: "newer is after older",
			a:    Track{AddedAt: at(20)},
			b:    Track{AddedAt: at(10)},
			want: true,
		},
		{
			name: "older is not after newer",
			a:    Track{AddedAt: at(10)},
			b:    Track{AddedAt: at(20)},
			want: false,
		},
		{
			// Neither wins, which is what keeps sort.Slice stable-ish rather
			// than having two tracks claim to precede each other.
			name: "identical timestamps",
			a:    Track{AddedAt: at(10)},
			b:    Track{AddedAt: at(10)},
			want: false,
		},
		{
			// The zero time predates everything, so a track Spotify gave no
			// timestamp for sinks to the bottom instead of winning by accident.
			name: "a real time beats a zero time",
			a:    Track{AddedAt: at(10)},
			b:    Track{},
			want: true,
		},
		{
			name: "a zero time does not beat a real one",
			a:    Track{},
			b:    Track{AddedAt: at(10)},
			want: false,
		},
		{
			name: "two zero times",
			a:    Track{},
			b:    Track{},
			want: false,
		},
		{
			// Spotify sends RFC 3339 with a zone; the comparison must be about
			// the instant, not the wall clock reading.
			name: "different zones, same instant",
			a:    Track{AddedAt: at(10).In(time.FixedZone("somewhere", 3600))},
			b:    Track{AddedAt: at(10)},
			want: false,
		},
		{
			name: "different zones, genuinely later",
			a:    Track{AddedAt: at(10).Add(time.Second).In(time.FixedZone("somewhere", -7200))},
			b:    Track{AddedAt: at(10)},
			want: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := addedAfter(test.a, test.b); got != test.want {
				t.Errorf("addedAfter(%v, %v) = %v, want %v",
					test.a.AddedAt, test.b.AddedAt, got, test.want)
			}
		})
	}
}

// sortTracks applies the same ordering FetchPlaylistTracks does, so the sort can
// be exercised without reaching the API.
func sortTracks(tracks []Track) {
	sort.Slice(tracks, func(i, j int) bool {
		return addedAfter(tracks[i], tracks[j])
	})
}

func TestSortingPutsNewestFirst(t *testing.T) {
	tests := []struct {
		name  string
		input []Track
		want  []string
	}{
		{
			name: "shuffled input",
			input: []Track{
				{Name: "middle", AddedAt: at(15)},
				{Name: "oldest", AddedAt: at(1)},
				{Name: "newest", AddedAt: at(30)},
			},
			want: []string{"newest", "middle", "oldest"},
		},
		{
			name: "already in order",
			input: []Track{
				{Name: "newest", AddedAt: at(30)},
				{Name: "oldest", AddedAt: at(1)},
			},
			want: []string{"newest", "oldest"},
		},
		{
			name: "reversed",
			input: []Track{
				{Name: "oldest", AddedAt: at(1)},
				{Name: "newest", AddedAt: at(30)},
			},
			want: []string{"newest", "oldest"},
		},
		{
			// A track with no timestamp must not float to the top and claim to
			// be the most recently added.
			name: "missing timestamps sink",
			input: []Track{
				{Name: "no date"},
				{Name: "newest", AddedAt: at(30)},
				{Name: "older", AddedAt: at(5)},
			},
			want: []string{"newest", "older", "no date"},
		},
		{
			name:  "single track",
			input: []Track{{Name: "only", AddedAt: at(3)}},
			want:  []string{"only"},
		},
		{
			name:  "empty",
			input: nil,
			want:  nil,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			sortTracks(test.input)

			if len(test.input) != len(test.want) {
				t.Fatalf("got %d tracks, want %d", len(test.input), len(test.want))
			}
			for index, wantName := range test.want {
				if got := test.input[index].Name; got != wantName {
					t.Errorf("position %d = %q, want %q", index, got, wantName)
				}
			}
		})
	}
}

func TestSortingKeepsEveryTrack(t *testing.T) {
	// Equal timestamps are common - a batch added in one go shares a second -
	// and a comparison that reported "a before b" *and* "b before a" could make
	// sort.Slice drop or duplicate entries.
	tracks := make([]Track, 0, 20)
	for i := range 20 {
		tracks = append(tracks, Track{ID: string(rune('a' + i)), AddedAt: at(10)})
	}

	sortTracks(tracks)

	seen := make(map[string]bool, len(tracks))
	for _, track := range tracks {
		if seen[track.ID] {
			t.Fatalf("track %q appears twice after sorting", track.ID)
		}
		seen[track.ID] = true
	}
	if len(seen) != 20 {
		t.Errorf("got %d distinct tracks after sorting, want 20", len(seen))
	}
}

func TestPlaylistTrackItemDecodesAddedAt(t *testing.T) {
	// The exact shape Spotify sends: added_at is a sibling of is_local and item,
	// not a field on the song.
	const payload = `{
		"added_at": "2026-08-30T22:21:04Z",
		"is_local": false,
		"item": { "id": "abc", "name": "Some Track", "duration_ms": 180000 }
	}`

	var item PlaylistTrackItem
	if err := json.Unmarshal([]byte(payload), &item); err != nil {
		t.Fatalf("decoding: %v", err)
	}

	want := time.Date(2026, time.August, 30, 22, 21, 4, 0, time.UTC)
	if !item.AddedAt.Equal(want) {
		t.Errorf("AddedAt = %v, want %v", item.AddedAt, want)
	}
	if item.Item.Name != "Some Track" {
		t.Errorf("Item.Name = %q, want the song", item.Item.Name)
	}
	// It decodes onto the wrapper, never onto the song - FetchPlaylistTracks
	// copies it across.
	if !item.Item.AddedAt.IsZero() {
		t.Errorf("Item.AddedAt = %v, want zero", item.Item.AddedAt)
	}
}

func TestDedupeByFileName(t *testing.T) {
	// A track really can appear twice in one playlist with different added_at
	// values - "Bounce" in Mix Potential was added in May and again in
	// September. Both land on one filename, so left in they fight over that
	// file's timestamp on every run.
	tests := []struct {
		name  string
		input []Track
		want  []string
	}{
		{
			// After sorting, the first occurrence is the newest, and that is
			// the one to keep.
			name: "same name and artist collapses to the newest",
			input: []Track{
				{Name: "Bounce", Artists: []Artist{{Name: "bullet tooth"}}, AddedAt: at(29)},
				{Name: "Bounce", Artists: []Artist{{Name: "bullet tooth"}}, AddedAt: at(1)},
			},
			want: []string{"Bounce - bullet tooth"},
		},
		{
			// Same song title, different artist, so different files - both stay.
			name: "same title by different artists both survive",
			input: []Track{
				{Name: "Gravity", Artists: []Artist{{Name: "Xilent"}}},
				{Name: "Gravity", Artists: []Artist{{Name: "Someone Else"}}},
			},
			want: []string{"Gravity - Xilent", "Gravity - Someone Else"},
		},
		{
			// The downloader matches existing files case-insensitively, so this
			// has to as well or the two would still collide on disk.
			name: "case differences collapse",
			input: []Track{
				{Name: "BOO", Artists: []Artist{{Name: "Someone"}}, AddedAt: at(20)},
				{Name: "boo", Artists: []Artist{{Name: "someone"}}, AddedAt: at(10)},
			},
			want: []string{"BOO - Someone"},
		},
		{
			name: "nothing to drop",
			input: []Track{
				{Name: "First", Artists: []Artist{{Name: "A"}}},
				{Name: "Second", Artists: []Artist{{Name: "B"}}},
			},
			want: []string{"First - A", "Second - B"},
		},
		{
			name:  "empty",
			input: nil,
			want:  nil,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := dedupeByFileName(test.input)

			if len(got) != len(test.want) {
				t.Fatalf("got %d track(s), want %d", len(got), len(test.want))
			}
			for index, wantName := range test.want {
				if name := got[index].FileName(); name != wantName {
					t.Errorf("position %d = %q, want %q", index, name, wantName)
				}
			}
		})
	}
}

func TestDedupeKeepsTheNewestDate(t *testing.T) {
	// Which of the two survives matters: it decides the file's timestamp, and
	// the whole point is newest-first.
	tracks := dedupeByFileName([]Track{
		{Name: "Bounce", Artists: []Artist{{Name: "bullet tooth"}}, AddedAt: at(29)},
		{Name: "Bounce", Artists: []Artist{{Name: "bullet tooth"}}, AddedAt: at(1)},
	})

	if len(tracks) != 1 {
		t.Fatalf("got %d tracks, want 1", len(tracks))
	}
	if !tracks[0].AddedAt.Equal(at(29)) {
		t.Errorf("kept the entry added %v, want the newest at %v", tracks[0].AddedAt, at(29))
	}
}

func TestGapFrom300(t *testing.T) {
	tests := []struct {
		name  string
		width int
		want  int
	}{
		{name: "exact", width: 300, want: 0},
		{name: "above", width: 640, want: 340},
		{name: "below", width: 64, want: 236},

		// The gap ignores direction, so these two are equally good candidates.
		{name: "50 over", width: 350, want: 50},
		{name: "50 under", width: 250, want: 50},

		{name: "zero width", width: 0, want: 300},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := gapFrom300(test.width); got != test.want {
				t.Errorf("gapFrom300(%d) = %d, want %d", test.width, got, test.want)
			}
		})
	}
}

func TestGetAlbumCoverArtURL(t *testing.T) {
	// The three sizes Spotify actually returns, widest first.
	spotifySizes := []Image{
		{URL: "640px", Width: 640, Height: 640},
		{URL: "300px", Width: 300, Height: 300},
		{URL: "64px", Width: 64, Height: 64},
	}

	tests := []struct {
		name   string
		images []Image
		want   string
	}{
		{
			name:   "picks 300 from Spotify's real three sizes",
			images: spotifySizes,
			want:   "300px",
		},
		{
			// Choosing by width rather than by position is the whole point: an
			// index would grab the 64px thumbnail here.
			name: "still picks 300 if the order changes",
			images: []Image{
				{URL: "640px", Width: 640},
				{URL: "64px", Width: 64},
				{URL: "300px", Width: 300},
			},
			want: "300px",
		},
		{
			// No art at all is real - local files and delisted tracks have none.
			// Indexing before checking the length would panic here.
			name:   "no images returns empty",
			images: nil,
			want:   "",
		},
		{
			name:   "a single image is used whatever its size",
			images: []Image{{URL: "64px", Width: 64}},
			want:   "64px",
		},
		{
			// Nothing reaches 300, so the largest available wins rather than
			// nothing being returned.
			name: "all smaller than 300 takes the largest",
			images: []Image{
				{URL: "64px", Width: 64},
				{URL: "100px", Width: 100},
			},
			want: "100px",
		},
		{
			// Everything overshoots, so take the closest one above.
			name: "all larger than 300 takes the smallest",
			images: []Image{
				{URL: "1000px", Width: 1000},
				{URL: "640px", Width: 640},
			},
			want: "640px",
		},
		{
			name:   "exactly 300 beats a near miss",
			images: []Image{{URL: "301px", Width: 301}, {URL: "300px", Width: 300}},
			want:   "300px",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			album := Album{Images: test.images}
			if got := album.GetAlbumCoverArtURL(); got != test.want {
				t.Errorf("GetAlbumCoverArtURL() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestAlbumDecodesImages(t *testing.T) {
	// The shape Spotify sends, lifted from a real response.
	const payload = `{
		"id": "20yM92448vcBFg4H9BS8tp",
		"name": "An Album",
		"images": [
			{"height": 640, "url": "https://i.scdn.co/image/b273", "width": 640},
			{"height": 300, "url": "https://i.scdn.co/image/1e02", "width": 300},
			{"height": 64,  "url": "https://i.scdn.co/image/4851", "width": 64}
		]
	}`

	var album Album
	if err := json.Unmarshal([]byte(payload), &album); err != nil {
		t.Fatalf("decoding: %v", err)
	}

	if len(album.Images) != 3 {
		t.Fatalf("decoded %d images, want 3 - are the struct fields exported?", len(album.Images))
	}
	if album.Images[1].Width != 300 {
		t.Errorf("second image width = %d, want 300", album.Images[1].Width)
	}
	if got := album.GetAlbumCoverArtURL(); got != "https://i.scdn.co/image/1e02" {
		t.Errorf("GetAlbumCoverArtURL() = %q, want the 300px URL", got)
	}
}

func TestJoinArtistNames(t *testing.T) {
	// This is what goes in the ID3 artist tag, as opposed to FileName which
	// keeps only the first artist. A collaboration should credit everyone where
	// software actually reads it.
	tests := []struct {
		name    string
		artists []Artist
		want    string
	}{
		{
			name:    "one artist",
			artists: []Artist{{Name: "Logic"}},
			want:    "Logic",
		},
		{
			name:    "two artists",
			artists: []Artist{{Name: "Koven"}, {Name: "Alex Hobson"}},
			want:    "Koven, Alex Hobson",
		},
		{
			name:    "several artists",
			artists: []Artist{{Name: "A"}, {Name: "B"}, {Name: "C"}, {Name: "D"}},
			want:    "A, B, C, D",
		},
		{
			// Spotify names carry stray whitespace often enough that this is
			// worth pinning rather than assuming.
			name:    "whitespace is trimmed per name",
			artists: []Artist{{Name: "  Logic  "}, {Name: "\tAsake\n"}},
			want:    "Logic, Asake",
		},
		{
			// A local file or a delisted track can have none. An empty tag is
			// right here; panicking is not.
			name:    "no artists",
			artists: nil,
			want:    "",
		},
		{
			name:    "an empty name among real ones",
			artists: []Artist{{Name: "Logic"}, {Name: ""}},
			want:    "Logic, ",
		},
		{
			// Illegal filename characters are not sanitised here - that is
			// FileName's job. The tag can hold anything.
			name:    "slashes survive, unlike in a filename",
			artists: []Artist{{Name: "AC/DC"}},
			want:    "AC/DC",
		},
		{
			name:    "unicode",
			artists: []Artist{{Name: "¥ØU$UK€ ¥OKOTA"}, {Name: "IÖN"}},
			want:    "¥ØU$UK€ ¥OKOTA, IÖN",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			track := Track{Name: "Some Track", Artists: test.artists}
			if got := track.JoinArtistNames(); got != test.want {
				t.Errorf("JoinArtistNames() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestJoinArtistNamesDiffersFromFileName(t *testing.T) {
	// The deliberate split recorded in the project notes: filenames stay
	// readable with one artist, the tag keeps the full credit. A change that
	// collapsed them into one would pass every other test.
	track := Track{
		Name:    "Gravity",
		Artists: []Artist{{Name: "Koven"}, {Name: "Alex Hobson"}},
	}

	if got := track.FileName(); got != "Gravity - Koven" {
		t.Errorf("FileName() = %q, want only the first artist", got)
	}
	if got := track.JoinArtistNames(); got != "Koven, Alex Hobson" {
		t.Errorf("JoinArtistNames() = %q, want every artist", got)
	}
}
