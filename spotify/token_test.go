package spotify

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Nat-Umeni/trackorganiser/config"
)

// fakeConfigHome points os.UserConfigDir at a temp directory.
//
// XDG_CONFIG_HOME has to be set, not just HOME: on Linux os.UserConfigDir checks
// it first, so a machine with it set would otherwise have these tests writing
// into my real config.
func fakeConfigHome(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir) // linux
	t.Setenv("HOME", dir)            // macos reaches it through HOME
	t.Setenv("AppData", dir)         // windows
	t.Setenv("USERPROFILE", dir)     // windows fallback

	return dir
}

func TestTokenCachePathSitsBesideTheConfig(t *testing.T) {
	fakeConfigHome(t)

	path, err := tokenCachePath()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	dir, err := config.ConfigDir()
	if err != nil {
		t.Fatalf("resolving the directory: %v", err)
	}

	// The whole point of the move: an absolute path in the user's config
	// directory rather than a bare filename resolved against whatever the
	// working directory happens to be.
	if filepath.Dir(path) != dir {
		t.Errorf("token path is %q, want it inside %q", path, dir)
	}
	if !filepath.IsAbs(path) {
		t.Errorf("token path %q is relative - that is the bug this replaced", path)
	}
	if filepath.Base(path) != tokenCacheFileName {
		t.Errorf("token file is named %q, want %q", filepath.Base(path), tokenCacheFileName)
	}
}

func TestTokenRoundTrip(t *testing.T) {
	fakeConfigHome(t)

	c := &Client{}

	// Nothing saved yet, so loading has to fail rather than return a blank
	// token that would then be sent to Spotify.
	if err := c.loadToken(); err == nil {
		t.Error("loadToken() succeeded with no file, want an error")
	}

	if err := c.saveToken("a-refresh-token"); err != nil {
		t.Fatalf("saving: %v", err)
	}

	loaded := &Client{}
	if err := loaded.loadToken(); err != nil {
		t.Fatalf("loading: %v", err)
	}
	if loaded.savedRefreshToken != "a-refresh-token" {
		t.Errorf("savedRefreshToken = %q, want the saved value", loaded.savedRefreshToken)
	}
}

func TestSaveTokenWritesRestrictivePermissions(t *testing.T) {
	fakeConfigHome(t)

	c := &Client{}
	if err := c.saveToken("a-refresh-token"); err != nil {
		t.Fatalf("saving: %v", err)
	}

	path, _ := tokenCachePath()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("the file was not written: %v", err)
	}

	// A refresh token grants access to the account until revoked, so this is
	// the one mode in the project that genuinely matters.
	if mode := info.Mode().Perm(); mode != 0600 {
		t.Errorf("mode = %04o, want 0600", mode)
	}
}

func TestDeleteTokenCache(t *testing.T) {
	fakeConfigHome(t)

	c := &Client{}
	if err := c.saveToken("a-refresh-token"); err != nil {
		t.Fatalf("setting up: %v", err)
	}

	path, _ := tokenCachePath()
	c.deleteTokenCache()

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("the token file still exists after deleting, Stat gave: %v", err)
	}

	// Deleting what is already gone is the outcome this wants, so a second call
	// must not panic.
	c.deleteTokenCache()
}
