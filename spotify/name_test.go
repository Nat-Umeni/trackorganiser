package spotify

import (
	"slices"
	"strings"
	"testing"
)

func TestPlaylistFolderName(t *testing.T) {
	tests := []struct {
		name     string
		playlist Playlist
		want     string
	}{
		{
			name:     "ordinary name is untouched",
			playlist: Playlist{Name: "Soul Food"},
			want:     "Soul Food",
		},
		{
			// The one that would silently create a nested directory.
			name:     "slash becomes a dash",
			playlist: Playlist{Name: "Jazzhop/lowfi"},
			want:     "Jazzhop-lowfi",
		},
		{
			name:     "pipe and colon",
			playlist: Playlist{Name: "¥ØU$UK€ ¥UK1MAT$U | BOILER ROOM: TOKYO"},
			want:     "¥ØU$UK€ ¥UK1MAT$U - BOILER ROOM- TOKYO",
		},
		{
			// Real playlist used to test this end to end.
			name:     "slash and trailing space",
			playlist: Playlist{Name: "Test/Playlist "},
			want:     "Test-Playlist",
		},
		{
			name:     "emoji survives",
			playlist: Playlist{Name: "🥱"},
			want:     "🥱",
		},
		{
			name:     "trailing dots are stripped",
			playlist: Playlist{Name: "for rainbow..."},
			want:     "for rainbow",
		},
		{
			name:     "nothing usable falls back to the id",
			playlist: Playlist{Name: "...", ID: "2qVrdnFi2puNoZjfztVC8u"},
			want:     "playlist-tVC8u",
		},
		{
			name:     "whitespace only falls back to the id",
			playlist: Playlist{Name: "   ", ID: "2qVrdnFi2puNoZjfztVC8u"},
			want:     "playlist-tVC8u",
		},
		{
			name:     "short id is not sliced",
			playlist: Playlist{Name: "", ID: "abc"},
			want:     "playlist-abc",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := test.playlist.FolderName(); got != test.want {
				t.Errorf("FolderName() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestTrackFileName(t *testing.T) {
	tests := []struct {
		name  string
		track Track
		want  string
	}{
		{
			name:  "song then artist",
			track: Track{Name: "Touch", Artists: []Artist{{Name: "KATSEYE"}}},
			want:  "Touch - KATSEYE",
		},
		{
			// The full credit list goes in the ID3 tag; the filename stays short.
			name: "only the first artist is used",
			track: Track{
				Name:    "PERMANENT SCARS",
				Artists: []Artist{{Name: "TUPLE"}, {Name: "BLOODGROUND"}},
			},
			want: "PERMANENT SCARS - TUPLE",
		},
		{
			name:  "no artists leaves just the song",
			track: Track{Name: "1945"},
			want:  "1945",
		},
		{
			// AC/DC is the reason the artist half is sanitised too.
			name:  "slash in the artist name",
			track: Track{Name: "Back In Black", Artists: []Artist{{Name: "AC/DC"}}},
			want:  "Back In Black - AC-DC",
		},
		{
			name:  "slash in the song name",
			track: Track{Name: "Yes/No", Artists: []Artist{{Name: "Bicep"}}},
			want:  "Yes-No - Bicep",
		},
		{
			name:  "surrounding whitespace is trimmed",
			track: Track{Name: " ETA ", Artists: []Artist{{Name: " NewJeans "}}},
			want:  "ETA - NewJeans",
		},
		{
			name: "unusable song name falls back to the id",
			track: Track{
				Name:    "...",
				ID:      "6UelLqGlWMcVH1E5c4H7lY",
				Artists: []Artist{{Name: "KATSEYE"}},
			},
			want: "track-4H7lY",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := test.track.FileName(); got != test.want {
				t.Errorf("FileName() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestTrackBuildSearchQuery(t *testing.T) {
	tests := []struct {
		name  string
		track Track
		want  string
	}{
		{
			// "topic" biases towards the auto-generated "<Artist> - Topic"
			// channels, which carry the label's master rather than a video.
			name:  "song, first artist, topic",
			track: Track{Name: "ETA", Artists: []Artist{{Name: "NewJeans"}}},
			want:  `ETA "NewJeans" topic`,
		},
		{
			// The case that prompted quoting, from a real run: unquoted, YouTube
			// searched for "Bison" and returned a Street Fighter theme.
			name:  "artist is quoted so it isn't autocorrected",
			track: Track{Name: "The Man", Artists: []Artist{{Name: "Buson"}}},
			want:  `The Man "Buson" topic`,
		},
		{
			name: "extra artists are left out",
			track: Track{
				Name:    "PERMANENT SCARS",
				Artists: []Artist{{Name: "TUPLE"}, {Name: "BLOODGROUND"}},
			},
			want: `PERMANENT SCARS "TUPLE" topic`,
		},
		{
			name:  "no artists",
			track: Track{Name: "1945"},
			want:  "1945 topic",
		},
		{
			name:  "whitespace is trimmed",
			track: Track{Name: " ETA ", Artists: []Artist{{Name: " NewJeans "}}},
			want:  `ETA "NewJeans" topic`,
		},
		{
			// Search strings are not paths, so illegal characters stay put.
			name:  "not sanitised",
			track: Track{Name: "Back In Black", Artists: []Artist{{Name: "AC/DC"}}},
			want:  `Back In Black "AC/DC" topic`,
		},
		{
			// A quote inside the name would end the quoted phrase early.
			name:  "quotes inside the artist are dropped",
			track: Track{Name: "Song", Artists: []Artist{{Name: `The "Real" One`}}},
			want:  `Song "The Real One" topic`,
		},
		{
			// Quoting nothing would search for an empty phrase.
			name:  "artist that is only quotes",
			track: Track{Name: "Song", Artists: []Artist{{Name: ` "" `}}},
			want:  "Song topic",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := test.track.BuildSearchQuery(); got != test.want {
				t.Errorf("BuildSearchQuery() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestTrackBuildFallbackQueries(t *testing.T) {
	tests := []struct {
		name  string
		track Track
		want  []string
	}{
		{
			// The case that prompted the fallbacks: the same search as the first
			// one, minus "topic", put the right upload first.
			name:  "one artist",
			track: Track{Name: "I Feel So Good", Artists: []Artist{{Name: "Bluff"}}},
			want:  []string{`I Feel So Good "Bluff"`},
		},
		{
			// From a real run: "Neumonic" was autocorrected to "Mnemonic", and
			// the second artist's search is what found "What?".
			name: "second artist gets its own search",
			track: Track{
				Name:    "What?",
				Artists: []Artist{{Name: "Neumonic"}, {Name: "Mary Droppinz"}, {Name: "Third"}},
			},
			want: []string{`What? "Neumonic"`, `What? "Mary Droppinz"`},
		},
		{
			name:  "no artists",
			track: Track{Name: "1945"},
			want:  []string{"1945"},
		},
		{
			name:  "whitespace is trimmed",
			track: Track{Name: " ETA ", Artists: []Artist{{Name: " NewJeans "}}},
			want:  []string{`ETA "NewJeans"`},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := test.track.BuildFallbackQueries()
			if !slices.Equal(got, test.want) {
				t.Errorf("BuildFallbackQueries() = %q, want %q", got, test.want)
			}

			// "topic" is what sent the first search astray, so a fallback that
			// still says it would repeat the same mistake.
			for _, query := range got {
				if strings.Contains(query, "topic") {
					t.Errorf("fallback %q still says topic", query)
				}
			}
		})
	}
}
