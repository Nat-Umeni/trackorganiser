package downloader

import (
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// featuringMarkers end the song's own name in a normalized Spotify title. They
// are padded with spaces so they only match whole words - "ft" must not cut
// "Left Behind" in half.
var featuringMarkers = []string{" feat ", " ft ", " featuring "}

// normalizeTitle reduces a title to lowercase words, so punctuation and spacing
// differences between Spotify and a YouTube upload don't count as a mismatch.
//
// Apostrophes are dropped rather than turned into spaces, because "Don't" and
// "Dont" both appear and should match. Everything else that isn't a letter or a
// digit separates words. Letters are any script's, so a Japanese title matches
// itself.
//
// Accents are dropped too. The same "é" can be stored as one character or as
// "e" plus a separate accent mark, and Spotify and YouTube don't agree - a real
// "recuérdame" was rejected for it. NFD splits every accented letter into the
// letter and its mark, and the mark is then skipped, which also lets "Café"
// match an upload titled "Cafe".
func normalizeTitle(title string) string {
	var builder strings.Builder

	for _, r := range norm.NFD.String(strings.ToLower(title)) {
		switch {
		case unicode.Is(unicode.Mn, r):
			// An accent mark, split off its letter by NFD.
		case r == '\'' || r == '’' || r == '`':
			// Dropped, joining "don't" into "dont".
		case r == '&':
			builder.WriteString(" and ")
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			builder.WriteRune(r)
		default:
			builder.WriteRune(' ')
		}
	}

	return strings.Join(strings.Fields(builder.String()), " ")
}

// stripBracketed removes anything inside () or [], where Spotify puts featured
// artists and YouTube puts "(Official Video)". Nesting is tracked so "(feat. X
// (Y))" goes in one piece.
func stripBracketed(text string) string {
	var builder strings.Builder
	depth := 0

	for _, r := range text {
		switch {
		case r == '(' || r == '[':
			depth++
		case (r == ')' || r == ']') && depth > 0:
			depth--
		case depth == 0:
			builder.WriteRune(r)
		}
	}

	return builder.String()
}

// songCore is the part of a Spotify track name that any upload of the song will
// contain: the name before any " - Remix" qualifier, without bracketed extras
// or a featuring credit, normalized.
//
//	"Starscapes - Rameses B Remix"    ->  "starscapes"
//	"Don't Stop (feat. Someone)"      ->  "dont stop"
//	"Bounce ft. Someone Else"         ->  "bounce"
func songCore(spotifyName string) string {
	// The same separator rule as versionQualifier: spaces on both sides, so
	// "Jay-Z" is left alone.
	name, _, _ := strings.Cut(spotifyName, " - ")

	return cutFeaturing(normalizeTitle(stripBracketed(name)))
}

// cutFeaturing ends an already normalized name at its featuring credit, if it
// has one - "bounce ft someone" becomes "bounce".
func cutFeaturing(normalized string) string {
	padded := " " + normalized + " "
	for _, marker := range featuringMarkers {
		if before, _, found := strings.Cut(padded, marker); found {
			padded = before + " "
		}
	}

	return strings.TrimSpace(padded)
}

// titleMatches reports whether a YouTube title looks like the song Spotify
// named. It answers what the version check can't: a plainly titled track coming
// back as a different song altogether - "I Feel So Good" by Bluff arriving as
// "Snow on The Bluff".
//
// Whole words only, so "Gravity" is not found inside "Antigravity". Passes when
// there is nothing to compare - an empty title means yt-dlp's record of it
// couldn't be read, the same rule versionMatches follows.
func titleMatches(spotifyName, youtubeTitle string) bool {
	core := songCore(spotifyName)
	if core == "" || strings.TrimSpace(youtubeTitle) == "" {
		return true
	}

	return strings.Contains(" "+normalizeTitle(youtubeTitle)+" ", " "+core+" ")
}

// standInToleranceMS is how close a result's length must be to Spotify's for
// the length to stand in for a missing artist credit. Far tighter than the 5s
// used to flag a download: it lets an upload through with nobody credited, so
// it has to be the same recording. Every real case was under a second out.
const standInToleranceMS = 2000

// lengthStandsIn reports whether a result is close enough to Spotify's length
// to be accepted without its artist appearing anywhere.
//
// Labels and archives upload under their own name - GRIM SAINT's tracks under
// "DiEM ARCHiVE", others under the auto-generated "Release - Topic" - so the
// artist check rejects the right song. Same title plus the same length to the
// second is the same master. The artist check exists for "123", where an
// unrelated song shared a short title; it was a minute longer.
func lengthStandsIn(spotifyMS, actualMS int) bool {
	if spotifyMS <= 0 {
		return false
	}

	difference := spotifyMS - actualMS
	if difference < 0 {
		difference = -difference
	}

	return difference <= standInToleranceMS
}

// artistMatches reports whether any of the track's artists appears in the
// video's title or channel - "Dominic B - Topic" carries it in the channel,
// "Nokaut – Easy [RAR492]" in the title.
//
// It catches what the title check can't when the song's name is short and
// common: "123" by A.M.R (DE) passed the title check with an unrelated song
// titled "123 ta me ye...". Measured over 39 real downloads, every right
// result named an artist somewhere and the only two that didn't were the two
// wrong songs.
//
// Any artist counts, so a collaboration filed under the second artist's Topic
// channel passes. Bracketed parts of a name are dropped first, because "(DE)"
// in "A.M.R (DE)" is Spotify disambiguating, not something an upload repeats.
func artistMatches(artists []string, youtubeTitle, channel string) bool {
	// Nothing recorded about the video means nothing to check against - the
	// same rule as titleMatches.
	if strings.TrimSpace(youtubeTitle) == "" && strings.TrimSpace(channel) == "" {
		return true
	}

	haystack := " " + normalizeTitle(youtubeTitle+" "+channel) + " "

	checked := false
	for _, artist := range artists {
		// The featured act is dropped from a credit like "Stanton Warriors feat.
		// Them & Us", because no upload repeats that whole string.
		name := cutFeaturing(normalizeTitle(stripBracketed(artist)))
		if name == "" {
			continue
		}

		checked = true
		if strings.Contains(haystack, " "+name+" ") {
			return true
		}
	}

	// No usable artist names at all is a pass, not a failure.
	return !checked
}
