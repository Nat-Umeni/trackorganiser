package downloader

import "strings"

// genericVersionWords name a *type* of version rather than a specific one.
//
// The distinction is the whole point of this check: it should fire when Spotify
// asks for a named remix and something else arrives, and stay quiet otherwise.
// "Rameses B Remix" names a person, so a title without "Rameses" is the wrong
// track. "Album Edit" or "Radio Edit" names no one - a Topic upload of it is
// just titled with the song name, and flagging that would be noise.
//
// "Remix" itself has to be here: it appears in both "Rameses B Remix" and "Dark
// By Design Remix Edit", so matching on it would pass the wrong track.
var genericVersionWords = map[string]bool{
	"remix": true, "edit": true, "edited": true, "mix": true, "version": true,
	"radio": true, "extended": true, "original": true, "instrumental": true,
	"live": true, "acoustic": true, "remaster": true, "remastered": true,
	"club": true, "bootleg": true, "vip": true, "dub": true,
	"album": true, "single": true, "deluxe": true, "clean": true, "explicit": true,
	"sped": true, "slowed": true, "reverb": true, "mono": true, "stereo": true,
	"feat": true, "featuring": true, "with": true, "and": true, "the": true,
}

// versionQualifier returns the part of a Spotify track name that says which
// version it is. Spotify puts this after " - ":
//
//	"Starscapes - Rameses B Remix"  ->  "Rameses B Remix"
//	"Gravity"                       ->  ""
//
// The separator needs spaces on both sides so it doesn't fire on names like
// "Jay-Z" or "Sun-El".
func versionQualifier(spotifyName string) string {
	_, qualifier, found := strings.Cut(spotifyName, " - ")
	if !found {
		return ""
	}

	return strings.TrimSpace(qualifier)
}

// distinctiveVersionWords reduces a qualifier to the words that identify it -
// usually a remixer's name. Everything generic, very short, or a bare year is
// dropped, because those match anything.
func distinctiveVersionWords(qualifier string) []string {
	var words []string

	for _, word := range strings.Fields(strings.ToLower(qualifier)) {
		word = strings.Trim(word, "()[]{},.!?\"'’`-")

		switch {
		case len(word) < 3:
			// Initials and stray letters: the "B" of "Rameses B Remix".
		case genericVersionWords[word]:
		case isAllDigits(word):
			// A year, as in "Remastered 2011".
		default:
			words = append(words, word)
		}
	}

	return words
}

func isAllDigits(word string) bool {
	for _, r := range word {
		if r < '0' || r > '9' {
			return false
		}
	}

	return word != ""
}

// versionMatches reports whether the video yt-dlp downloaded looks like the
// version Spotify named. It answers the failure the duration check cannot:
// "Starscapes - Rameses B Remix" coming back as a Dark By Design remix, which
// duration alone rates less suspicious than three tracks that were perfectly
// correct.
//
// Tracks with no qualifier always pass - there is nothing to compare.
func versionMatches(spotifyName, youtubeTitle string) bool {
	words := distinctiveVersionWords(versionQualifier(spotifyName))
	if len(words) == 0 {
		return true
	}

	title := strings.ToLower(youtubeTitle)
	for _, word := range words {
		if !strings.Contains(title, word) {
			return false
		}
	}

	return true
}
