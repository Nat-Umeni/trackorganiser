package downloader

import "testing"

func TestTitleMatches(t *testing.T) {
	tests := []struct {
		name    string
		spotify string
		youtube string
		want    bool
	}{
		// The case that prompted this, from a real run on 2026-11-13: the first
		// search for Bluff's track returned an unrelated song.
		{name: "bluff, wrong song", spotify: "I Feel So Good", youtube: "Snow on The Bluff", want: false},
		{name: "bluff, label upload", spotify: "I Feel So Good", youtube: "Bluff - I Feel So Good", want: true},
		{name: "bluff, topic upload", spotify: "I Feel So Good", youtube: "I Feel So Good", want: true},

		// Uploads decorate the title; none of that should count against it.
		{name: "official video suffix", spotify: "Gravity", youtube: "Gravity (Official Music Video)", want: true},
		{name: "case", spotify: "WITNESS", youtube: "Logic - Witness", want: true},
		{name: "punctuation", spotify: "Mr. Brightside", youtube: "The Killers - Mr Brightside", want: true},
		{name: "apostrophe dropped", spotify: "Don't Stop", youtube: "Dont Stop [HD]", want: true},
		{name: "curly apostrophe", spotify: "Gangsta’s Paradise", youtube: "Gangsta's Paradise", want: true},
		{name: "ampersand", spotify: "Rock & Roll", youtube: "Rock and Roll (Remastered)", want: true},

		// Spotify's own additions are not in every upload's title.
		{name: "version qualifier stripped", spotify: "Starscapes - Rameses B Remix", youtube: "Starscapes (Rameses B Remix)", want: true},
		{name: "featuring in brackets", spotify: "Bounce (feat. Someone)", youtube: "Bounce", want: true},
		{name: "featuring without brackets", spotify: "Bounce ft. Someone", youtube: "Artist - Bounce", want: true},
		{name: "featuring spelled out", spotify: "Bounce featuring Someone", youtube: "Bounce", want: true},

		// Whole words only - substring matching would pass the wrong song.
		{name: "word inside a longer word", spotify: "Gravity", youtube: "Antigravity", want: false},
		{name: "ft inside a word survives", spotify: "Left Behind", youtube: "Left Behind (Live)", want: true},
		{name: "different song", spotify: "Torn Apart", youtube: "Tear Me Apart", want: false},

		// A known limit, pinned so it is a decision rather than a surprise: a
		// short, common name is found inside unrelated titles. This passed in a
		// real run; artistMatches is what catches it.
		{name: "short name inside an unrelated title", spotify: "123", youtube: "123 ta me ye da stargo thor akh akh mar de kram", want: true},

		// Nothing to compare, so nothing to flag.
		{name: "empty title", spotify: "Gravity", youtube: "", want: true},
		{name: "core empty after stripping", spotify: "(Intro)", youtube: "Something Else", want: true},

		// Any script, not just Latin.
		{name: "japanese", spotify: "夜に駆ける", youtube: "YOASOBI「夜に駆ける」 Official Music Video", want: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := titleMatches(test.spotify, test.youtube); got != test.want {
				t.Errorf("titleMatches(%q, %q) = %v, want %v (core %q)",
					test.spotify, test.youtube, got, test.want, songCore(test.spotify))
			}
		})
	}
}

func TestArtistMatches(t *testing.T) {
	tests := []struct {
		name    string
		artists []string
		title   string
		channel string
		want    bool
	}{
		// Where the right results from a real run carried the artist.
		{name: "topic channel", artists: []string{"Dominic B"}, title: "Find Me", channel: "Dominic B - Topic", want: true},
		{name: "artist in the title", artists: []string{"Nokaut"}, title: "Nokaut – Easy [RAR492]", channel: "Raveart Records", want: true},
		{name: "case and punctuation", artists: []string{"NOTION"}, title: "Redlight - Get Out My Head (Notion Bootleg)", channel: "Kessleric", want: true},

		// A collaboration may sit under any of its artists' channels.
		{name: "second artist only", artists: []string{"Neumonic", "Mary Droppinz"}, title: "What?", channel: "Mary Droppinz - Topic", want: true},

		// "(DE)" is Spotify telling two artists apart; uploads don't repeat it.
		{name: "bracketed part of the artist dropped", artists: []string{"A.M.R (DE)"}, title: "A.M.R - 123", channel: "Some Label", want: true},

		// The two wrong songs from the same run. The title check let "123"
		// through, and this is what catches it.
		{name: "buson, autocorrected to bison", artists: []string{"Buson"}, title: "Street Fighter 6 M. Bison's Theme - Undying Desire", channel: "Street Fighter", want: false},
		{name: "123, unrelated song", artists: []string{"A.M.R (DE)"}, title: "123 ta me ye da stargo thor akh akh mar de kram", channel: "Devil's Tone", want: false},

		// Whole words only.
		{name: "artist inside a longer word", artists: []string{"Bluff"}, title: "Snow on The Bluffs", channel: "evening elephants", want: false},

		// A known limit, pinned: an artist whose name is an ordinary word is
		// found in unrelated titles. The title check is what stops "Snow on The
		// Bluff" - the two checks cover for each other.
		{name: "artist name as an ordinary word", artists: []string{"Bluff"}, title: "Snow on The Bluff", channel: "evening elephants", want: true},

		// Nothing to compare, so nothing to flag.
		{name: "no artists", artists: nil, title: "Anything", channel: "Anyone", want: true},
		{name: "artist with nothing usable", artists: []string{"(???)"}, title: "Anything", channel: "Anyone", want: true},
		{name: "nothing recorded about the video", artists: []string{"Buson"}, title: "", channel: "", want: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := artistMatches(test.artists, test.title, test.channel); got != test.want {
				t.Errorf("artistMatches(%q, %q, %q) = %v, want %v",
					test.artists, test.title, test.channel, got, test.want)
			}
		})
	}
}
