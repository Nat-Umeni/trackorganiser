package downloader

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestVersionQualifier(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "remix qualifier", in: "Starscapes - Rameses B Remix", want: "Rameses B Remix"},
		{name: "qualifier after a feature credit", in: "Cracks Ft. Belle Humble - Flux Pavilion Remix", want: "Flux Pavilion Remix"},
		{name: "no qualifier", in: "Gravity", want: ""},

		// The separator needs spaces on both sides, or every hyphenated artist
		// would look like a version.
		{name: "hyphen without spaces is not a separator", in: "Jay-Z Song", want: ""},
		{name: "hyphenated name plus a real qualifier", in: "Sun-El Musician Song - Radio Edit", want: "Radio Edit"},

		{name: "only the first separator splits", in: "Song - Live - Remastered", want: "Live - Remastered"},
		{name: "empty name", in: "", want: ""},
		{name: "trailing separator", in: "Song - ", want: ""},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := versionQualifier(test.in); got != test.want {
				t.Errorf("versionQualifier(%q) = %q, want %q", test.in, got, test.want)
			}
		})
	}
}

func TestDistinctiveVersionWords(t *testing.T) {
	tests := []struct {
		name      string
		qualifier string
		want      []string
	}{
		{
			// "B" is too short to mean anything, "Remix" appears in every
			// qualifier - only "rameses" identifies this version.
			name:      "remixer name survives",
			qualifier: "Rameses B Remix",
			want:      []string{"rameses"},
		},
		{
			name:      "two word remixer",
			qualifier: "Flux Pavilion Remix",
			want:      []string{"flux", "pavilion"},
		},
		{
			name:      "all generic leaves nothing",
			qualifier: "Radio Edit",
			want:      nil,
		},
		{
			name:      "years are dropped",
			qualifier: "Remastered 2011",
			want:      nil,
		},
		{
			name:      "extended mix is generic",
			qualifier: "Extended Mix",
			want:      nil,
		},
		{
			name:      "punctuation is stripped",
			qualifier: "(Zeds Dead Remix)",
			want:      []string{"zeds", "dead"},
		},
		{
			name:      "empty qualifier",
			qualifier: "",
			want:      nil,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := distinctiveVersionWords(test.qualifier)
			if !slices.Equal(got, test.want) {
				t.Errorf("distinctiveVersionWords(%q) = %v, want %v", test.qualifier, got, test.want)
			}
		})
	}
}

func TestVersionMatches(t *testing.T) {
	tests := []struct {
		name         string
		spotifyName  string
		youtubeTitle string
		want         bool
	}{
		{
			// The real failure this exists for. Spotify wanted the Rameses B
			// remix; YouTube returned a Dark By Design one. Duration rated this
			// less suspicious than three tracks that were perfectly correct.
			name:         "wrong remix is caught",
			spotifyName:  "Starscapes - Rameses B Remix",
			youtubeTitle: "Starscapes (feat. Veela) (Dark By Design Vs DJ Husband Remix Edit)",
			want:         false,
		},
		{
			// The real correct case, which duration flagged at -55%.
			name:         "right remix passes despite being short",
			spotifyName:  "Cracks Ft. Belle Humble - Flux Pavilion Remix",
			youtubeTitle: "Freestylers - Cracks (Ft. Belle Humble) (Flux Pavilion Remix)",
			want:         true,
		},

		{
			// Found on a real run. "Album Edit" names a version type, not a
			// person, and a Topic upload of it is titled with just the song
			// name - so flagging it was noise. The check exists for named
			// remixes.
			name:         "album edit is a type, not a named version",
			spotifyName:  "Reach You - Album Edit",
			youtubeTitle: "Reach You",
			want:         true,
		},
		{
			name:         "other version types pass too",
			spotifyName:  "Song - Sped Up Version",
			youtubeTitle: "Song",
			want:         true,
		},
		{
			// The distinction that matters: a name, not a type.
			name:         "a named version still flags",
			spotifyName:  "Reach You - Rameses B Edit",
			youtubeTitle: "Reach You",
			want:         false,
		},
		{
			name:         "no qualifier always passes",
			spotifyName:  "Gravity",
			youtubeTitle: "Something Else Entirely",
			want:         true,
		},
		{
			name:         "generic qualifier always passes",
			spotifyName:  "Song - Radio Edit",
			youtubeTitle: "Some Upload With No Version Info",
			want:         true,
		},
		{
			name:         "remastered year passes",
			spotifyName:  "Bodies - Remastered 2011",
			youtubeTitle: "Drowning Pool - Bodies",
			want:         true,
		},
		{
			name:         "remixer present passes",
			spotifyName:  "Eyes on Fire - Zeds Dead Remix",
			youtubeTitle: "Blue Foundation - Eyes on Fire (Zeds Dead Remix)",
			want:         true,
		},
		{
			name:         "remixer absent flags",
			spotifyName:  "Eyes on Fire - Zeds Dead Remix",
			youtubeTitle: "Blue Foundation - Eyes on Fire",
			want:         false,
		},
		{
			name:         "case is ignored",
			spotifyName:  "Song - ZEDS DEAD Remix",
			youtubeTitle: "song (zeds dead remix)",
			want:         true,
		},
		{
			// Only some of the distinctive words present is still wrong.
			name:         "partial match flags",
			spotifyName:  "Song - Flux Pavilion Remix",
			youtubeTitle: "Song (Flux Something Else Remix)",
			want:         false,
		},
		{
			name:         "empty youtube title flags when a qualifier exists",
			spotifyName:  "Song - Rameses B Remix",
			youtubeTitle: "",
			want:         false,
		},
		{
			name:         "empty youtube title passes without a qualifier",
			spotifyName:  "Song",
			youtubeTitle: "",
			want:         true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := versionMatches(test.spotifyName, test.youtubeTitle)
			if got != test.want {
				t.Errorf("versionMatches(%q, %q) = %v, want %v",
					test.spotifyName, test.youtubeTitle, got, test.want)
			}
		})
	}
}

func TestReadTitleFile(t *testing.T) {
	dir := t.TempDir()

	written := filepath.Join(dir, "title")
	if err := os.WriteFile(written, []byte("Some Track (Remix)\n"), 0644); err != nil {
		t.Fatalf("setting up: %v", err)
	}

	empty := filepath.Join(dir, "empty")
	if err := os.WriteFile(empty, nil, 0644); err != nil {
		t.Fatalf("setting up: %v", err)
	}

	tests := []struct {
		name string
		path string
		want string
	}{
		{name: "trailing newline is trimmed", path: written, want: "Some Track (Remix)"},
		{name: "empty file", path: empty, want: ""},

		// All of these mean "cannot check the version", never "fail the
		// download" - the mp3 is fine and tagged either way.
		{name: "missing file", path: filepath.Join(dir, "nope"), want: ""},
		{name: "empty path", path: "", want: ""},
		{name: "path is a directory", path: dir, want: ""},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := readTitleFile(test.path); got != test.want {
				t.Errorf("readTitleFile(%q) = %q, want %q", test.path, got, test.want)
			}
		})
	}
}
