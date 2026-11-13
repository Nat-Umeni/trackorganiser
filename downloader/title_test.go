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

		// From a real run: the right upload, rejected, because Spotify stored
		// "é" as one character and YouTube as "e" plus a separate accent mark.
		// They look identical and compare as different words.
		{name: "accent encoded two ways", spotify: "recuérdame", youtube: "recuérdame", want: true},
		{name: "accent dropped by the uploader", spotify: "Café del Mar", youtube: "Energy 52 - Cafe Del Mar", want: true},

		// Mid-word is the case that needs the accent dropped rather than
		// treated as a separator - at the end of a word the two look the same,
		// and a mutation check showed the cases above couldn't tell them apart.
		{name: "accent dropped mid-word", spotify: "recuérdame", youtube: "Yaego - Recuerdame", want: true},
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

func TestLengthStandsIn(t *testing.T) {
	tests := []struct {
		name      string
		spotifyMS int
		actualMS  int
		want      bool
	}{
		// Every real case was under a second out - the same master, re-uploaded.
		{name: "grim saint, measured", spotifyMS: 124183, actualMS: 124000, want: true},
		{name: "fallen, measured", spotifyMS: 435350, actualMS: 436000, want: true},
		{name: "two seconds is the edge", spotifyMS: 100000, actualMS: 102000, want: true},

		// Tighter than the 5s used for flagging: this lets an upload through
		// with no artist credit, so it has to be the same recording.
		{name: "two and a half seconds out", spotifyMS: 100000, actualMS: 102500, want: false},
		{name: "a different edit", spotifyMS: 100000, actualMS: 140000, want: false},

		// No length from Spotify means nothing to stand in with.
		{name: "spotify gave no length", spotifyMS: 0, actualMS: 0, want: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := lengthStandsIn(test.spotifyMS, test.actualMS); got != test.want {
				t.Errorf("lengthStandsIn(%d, %d) = %v, want %v", test.spotifyMS, test.actualMS, got, test.want)
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

		// From a real run: Spotify credited the artist as one name with the
		// featured act inside it, and no upload repeats that whole string.
		{name: "featuring dropped from the artist", artists: []string{"Stanton Warriors feat. Them & Us"},
			title: "Cut Me Up (feat. Them & Us) (Cause & Affect Remix)", channel: "Stanton Warriors - Topic", want: true},
		{name: "ft. dropped from the artist", artists: []string{"Someone ft. Other"}, title: "Song", channel: "Someone - Topic", want: true},
		{name: "featuring spelled out", artists: []string{"Someone featuring Other"}, title: "Song", channel: "Someone - Topic", want: true},
		{name: "featuring dropped, still checked", artists: []string{"Stanton Warriors feat. Them & Us"},
			title: "Unrelated Song", channel: "Somebody Else", want: false},
		{name: "accent in the artist", artists: []string{"Beyoncé"}, title: "Halo", channel: "Beyonce - Topic", want: true},

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
