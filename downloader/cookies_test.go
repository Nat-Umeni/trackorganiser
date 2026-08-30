package downloader

import (
	"os"
	"path/filepath"
	"testing"
)

// writeProfile creates a Firefox profile directory under home, optionally with a
// cookie store in it.
func writeProfile(t *testing.T, home, relativeRoot, profile string, withCookies bool) {
	t.Helper()

	dir := filepath.Join(home, relativeRoot, profile)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("setting up %q: %v", dir, err)
	}

	if !withCookies {
		return
	}

	if err := os.WriteFile(filepath.Join(dir, "cookies.sqlite"), []byte("not really sqlite"), 0644); err != nil {
		t.Fatalf("setting up %q: %v", dir, err)
	}
}

func TestFirefoxCookiesAvailable(t *testing.T) {
	tests := []struct {
		name string
		// setUp builds whatever should exist under the fake home.
		setUp func(t *testing.T, home string)
		want  bool
	}{
		{
			// The path a current Firefox on Linux actually uses, and the one a
			// naive check of ~/.mozilla alone would miss.
			name: "xdg path",
			setUp: func(t *testing.T, home string) {
				writeProfile(t, home, ".config/mozilla/firefox", "lmg7pb4h.default-release", true)
			},
			want: true,
		},
		{
			name: "legacy path",
			setUp: func(t *testing.T, home string) {
				writeProfile(t, home, ".mozilla/firefox", "abc123.default", true)
			},
			want: true,
		},
		{
			name: "snap path",
			setUp: func(t *testing.T, home string) {
				writeProfile(t, home, "snap/firefox/common/.mozilla/firefox", "xyz.default", true)
			},
			want: true,
		},
		{
			name: "flatpak path",
			setUp: func(t *testing.T, home string) {
				writeProfile(t, home, ".var/app/org.mozilla.firefox/.mozilla/firefox", "xyz.default", true)
			},
			want: true,
		},
		{
			// Firefox installed but never run. Passing the flag here would make
			// yt-dlp fail every download rather than just the age-gated ones.
			name: "profile directory but no cookie store",
			setUp: func(t *testing.T, home string) {
				writeProfile(t, home, ".config/mozilla/firefox", "lmg7pb4h.default-release", false)
			},
			want: false,
		},
		{
			name:  "empty home",
			setUp: func(t *testing.T, home string) {},
			want:  false,
		},
		{
			name: "some other browser only",
			setUp: func(t *testing.T, home string) {
				if err := os.MkdirAll(filepath.Join(home, ".config", "chromium"), 0755); err != nil {
					t.Fatalf("setting up: %v", err)
				}
			},
			want: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			home := t.TempDir()
			test.setUp(t, home)

			if got := FirefoxCookiesAvailable(home); got != test.want {
				t.Errorf("FirefoxCookiesAvailable() = %v, want %v", got, test.want)
			}
		})
	}
}
