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

func TestReadVideoInfo(t *testing.T) {
	dir := t.TempDir()

	write := func(name, contents string) string {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(contents), 0644); err != nil {
			t.Fatalf("setting up: %v", err)
		}
		return path
	}

	// What yt-dlp writes for videoInfoTemplate, measured on the Bluff upload.
	full := write("full", "LwjV4rKV1tU\tUKF\t148\thttps://www.youtube.com/watch?v=LwjV4rKV1tU\tBluff - I Feel So Good\n")

	// A title is free text. Only the first four tabs separate fields.
	tabbed := write("tabbed", "abc\tSomeone\t200\thttps://example.com/abc\tA\tTitle\tWith Tabs\n")

	// An unexpected shape - an older template, or yt-dlp changing - still yields
	// a usable title rather than nothing.
	titleOnly := write("title-only", "Some Track (Remix)\n")

	empty := write("empty", "")

	tests := []struct {
		name string
		path string
		want videoInfo
	}{
		{name: "every field", path: full, want: videoInfo{ID: "LwjV4rKV1tU", Channel: "UKF", Duration: "148",
			URL: "https://www.youtube.com/watch?v=LwjV4rKV1tU", Title: "Bluff - I Feel So Good"}},
		{name: "tabs inside the title", path: tabbed, want: videoInfo{ID: "abc", Channel: "Someone", Duration: "200",
			URL: "https://example.com/abc", Title: "A\tTitle\tWith Tabs"}},
		{name: "title only, trailing newline trimmed", path: titleOnly, want: videoInfo{Title: "Some Track (Remix)"}},
		{name: "empty file", path: empty, want: videoInfo{}},

		// All of these mean "cannot check the title", never "fail the
		// download" - the mp3 is fine and tagged either way.
		{name: "missing file", path: filepath.Join(dir, "nope"), want: videoInfo{}},
		{name: "empty path", path: "", want: videoInfo{}},
		{name: "path is a directory", path: dir, want: videoInfo{}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := readVideoInfo(test.path); got != test.want {
				t.Errorf("readVideoInfo(%q) = %+v, want %+v", test.path, got, test.want)
			}
		})
	}
}

// A search listing prints one of these lines per result, straight to stdout
// rather than a file, so the line parsing has to stand on its own.
func TestParseVideoInfo(t *testing.T) {
	tests := []struct {
		name string
		line string
		want videoInfo
	}{
		// Measured: result 2 of the "Easy" search, the edit Spotify has.
		{name: "every field", line: "nElXJx1PHi4\tbreaksbeat5.0\t192\thttps://www.youtube.com/watch?v=nElXJx1PHi4\tNOKAUT - (Easy) - original mix ",
			want: videoInfo{ID: "nElXJx1PHi4", Channel: "breaksbeat5.0", Duration: "192",
				URL: "https://www.youtube.com/watch?v=nElXJx1PHi4", Title: "NOKAUT - (Easy) - original mix"}},

		// Measured on SoundCloud: no channel, so %(channel,uploader)s gives the
		// uploader - the artist's own account - and the duration is fractional.
		{name: "soundcloud", line: "2314260431\tXav\t346.437\thttps://soundcloud.com/officialxav/anotr-talk-to-you-xav-funky\tANOTR - Talk To You (Xav Funky Remix)",
			want: videoInfo{ID: "2314260431", Channel: "Xav", Duration: "346.437",
				URL: "https://soundcloud.com/officialxav/anotr-talk-to-you-xav-funky", Title: "ANOTR - Talk To You (Xav Funky Remix)"}},

		{name: "tabs inside the title", line: "abc\tSomeone\t200\thttps://example.com/abc\tA\tB",
			want: videoInfo{ID: "abc", Channel: "Someone", Duration: "200", URL: "https://example.com/abc", Title: "A\tB"}},
		{name: "title only", line: "Some Track", want: videoInfo{Title: "Some Track"}},
		{name: "blank", line: "   ", want: videoInfo{}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := parseVideoInfo(test.line); got != test.want {
				t.Errorf("parseVideoInfo(%q) = %+v, want %+v", test.line, got, test.want)
			}
		})
	}
}

// A listing's result is downloaded by its own link. YouTube's and SoundCloud's
// differ, so assuming a YouTube watch link would fetch nothing for SoundCloud.
func TestVideoInfoDownloadTarget(t *testing.T) {
	tests := []struct {
		name string
		info videoInfo
		want string
	}{
		{name: "soundcloud link", info: videoInfo{ID: "2314260431", URL: "https://soundcloud.com/officialxav/anotr-talk-to-you-xav-funky"},
			want: "https://soundcloud.com/officialxav/anotr-talk-to-you-xav-funky"},
		{name: "youtube link", info: videoInfo{ID: "nElXJx1PHi4", URL: "https://www.youtube.com/watch?v=nElXJx1PHi4"},
			want: "https://www.youtube.com/watch?v=nElXJx1PHi4"},

		// yt-dlp prints NA for a missing field. Without a link, a bare ID can
		// only have come from YouTube.
		{name: "no link recorded", info: videoInfo{ID: "abc123", URL: "NA"}, want: "https://www.youtube.com/watch?v=abc123"},
		{name: "empty link", info: videoInfo{ID: "abc123"}, want: "https://www.youtube.com/watch?v=abc123"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := test.info.downloadTarget(); got != test.want {
				t.Errorf("downloadTarget() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestVideoInfoDurationMS(t *testing.T) {
	tests := []struct {
		duration string
		wantMS   int
		wantOK   bool
	}{
		{duration: "192", wantMS: 192000, wantOK: true},
		{duration: "192.5", wantMS: 192500, wantOK: true},

		// yt-dlp prints NA for a field it doesn't have - a live stream, say. No
		// length is not the same as a zero length, which would fail every check.
		{duration: "NA", wantOK: false},
		{duration: "", wantOK: false},
		{duration: "0", wantOK: false},
		{duration: "-3", wantOK: false},
	}

	for _, test := range tests {
		t.Run(test.duration, func(t *testing.T) {
			gotMS, gotOK := videoInfo{Duration: test.duration}.durationMS()
			if gotMS != test.wantMS || gotOK != test.wantOK {
				t.Errorf("durationMS() for %q = %d, %v; want %d, %v",
					test.duration, gotMS, gotOK, test.wantMS, test.wantOK)
			}
		})
	}
}
