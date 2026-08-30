package downloader

import (
	"os"
	"path/filepath"
)

// firefoxProfileRoots are the directories Firefox keeps its profiles in,
// relative to the user's home. The XDG path is the newer one and is what a
// current Firefox on Linux actually uses - checking only the traditional
// ~/.mozilla path would miss it entirely.
var firefoxProfileRoots = []string{
	filepath.Join(".mozilla", "firefox"),
	filepath.Join(".config", "mozilla", "firefox"),
	filepath.Join("snap", "firefox", "common", ".mozilla", "firefox"),
	filepath.Join(".var", "app", "org.mozilla.firefox", ".mozilla", "firefox"),
}

// FirefoxCookiesAvailable reports whether there is a Firefox cookie store to
// read. Firefox is assumed because this is a single-user tool on a Firefox
// machine; browser detection was considered and rejected as brittle.
//
// The check exists because yt-dlp treats an unreadable cookie store as a hard
// error, so passing --cookies-from-browser blindly would fail every single
// download on a machine without Firefox. Better to quietly go without and lose
// only the age-restricted tracks.
//
// home is a parameter rather than read from the environment so it can be tested
// against a temporary directory.
func FirefoxCookiesAvailable(home string) bool {
	for _, root := range firefoxProfileRoots {
		// Profile directories are named per install (lmg7pb4h.default-release
		// and the like), so the level below the root has to be globbed rather
		// than guessed.
		matches, err := filepath.Glob(filepath.Join(home, root, "*", "cookies.sqlite"))
		if err != nil {
			continue
		}

		for _, match := range matches {
			if info, err := os.Stat(match); err == nil && !info.IsDir() {
				return true
			}
		}
	}

	return false
}
