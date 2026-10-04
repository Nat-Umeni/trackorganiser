package spotify

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
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

func TestConfigDirCreatesItself(t *testing.T) {
	base := fakeConfigHome(t)

	dir, err := ConfigDir()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("the directory was not created: %v", err)
	}
	if !info.IsDir() {
		t.Fatal("got a file where a directory was expected")
	}

	// 0700, because a refresh token lives in here.
	if mode := info.Mode().Perm(); mode != 0700 {
		t.Errorf("mode = %04o, want 0700", mode)
	}

	if !strings.HasPrefix(dir, base) {
		t.Errorf("ConfigDir() = %q, want it under %q", dir, base)
	}
	if filepath.Base(dir) != "trackorganiser" {
		t.Errorf("ConfigDir() = %q, want it to end in trackorganiser", dir)
	}
}

func TestConfigDirIsIdempotent(t *testing.T) {
	fakeConfigHome(t)

	first, err := ConfigDir()
	if err != nil {
		t.Fatalf("first call: %v", err)
	}

	// Called on every load and save, so an existing directory must not be an
	// error.
	second, err := ConfigDir()
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if first != second {
		t.Errorf("got %q then %q, want the same path", first, second)
	}
}

func TestLoadClientIDWithNothingSaved(t *testing.T) {
	fakeConfigHome(t)

	// A first run has no file, and that is normal rather than an error - the
	// caller distinguishes "not set yet" by the empty string.
	if got := LoadClientID(); got != "" {
		t.Errorf("LoadClientID() = %q, want empty", got)
	}
}

func TestSaveThenLoadClientID(t *testing.T) {
	fakeConfigHome(t)

	if err := SaveClientID("6030a2e4b9094bb295d9793f1d67bbf2"); err != nil {
		t.Fatalf("saving: %v", err)
	}

	if got := LoadClientID(); got != "6030a2e4b9094bb295d9793f1d67bbf2" {
		t.Errorf("LoadClientID() = %q, want the saved value", got)
	}
}

func TestSaveClientIDTrims(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "trailing space", input: "abc123 ", want: "abc123"},
		{name: "leading space", input: " abc123", want: "abc123"},
		{name: "both ends", input: "  abc123  ", want: "abc123"},
		{name: "newline from a paste", input: "abc123\n", want: "abc123"},
		{name: "tab", input: "\tabc123\t", want: "abc123"},
		{name: "windows line ending", input: "abc123\r\n", want: "abc123"},
		{name: "nothing to trim", input: "abc123", want: "abc123"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fakeConfigHome(t)

			// This matters more than it looks: the value arrives by copy-paste
			// from whoever handed it over, and Spotify rejects a padded ID with
			// an error that says nothing about whitespace.
			if err := SaveClientID(test.input); err != nil {
				t.Fatalf("saving: %v", err)
			}

			if got := LoadClientID(); got != test.want {
				t.Errorf("saved %q, loaded %q, want %q", test.input, got, test.want)
			}
		})
	}
}

func TestSaveClientIDOverwrites(t *testing.T) {
	fakeConfigHome(t)

	if err := SaveClientID("first-one"); err != nil {
		t.Fatalf("saving: %v", err)
	}
	if err := SaveClientID("second-one"); err != nil {
		t.Fatalf("saving again: %v", err)
	}

	// Re-running with a corrected ID has to actually correct it, or a wrong
	// value could only be fixed by hand-editing the file.
	if got := LoadClientID(); got != "second-one" {
		t.Errorf("LoadClientID() = %q, want the second value", got)
	}
}

func TestSaveClientIDWritesRestrictivePermissions(t *testing.T) {
	fakeConfigHome(t)

	if err := SaveClientID("abc123"); err != nil {
		t.Fatalf("saving: %v", err)
	}

	dir, err := ConfigDir()
	if err != nil {
		t.Fatalf("resolving the directory: %v", err)
	}

	info, err := os.Stat(filepath.Join(dir, configFileName))
	if err != nil {
		t.Fatalf("the file was not written: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0600 {
		t.Errorf("mode = %04o, want 0600", mode)
	}
}

func TestLoadConfigSurvivesACorruptFile(t *testing.T) {
	fakeConfigHome(t)

	dir, err := ConfigDir()
	if err != nil {
		t.Fatalf("resolving the directory: %v", err)
	}

	// A half-written file should cost a re-entered client ID, not a program
	// that refuses to start.
	if err := os.WriteFile(filepath.Join(dir, configFileName), []byte("{not json"), 0600); err != nil {
		t.Fatalf("setting up: %v", err)
	}

	if got := LoadClientID(); got != "" {
		t.Errorf("LoadClientID() = %q, want empty for a corrupt file", got)
	}

	// And it must be recoverable by saving over the top.
	if err := SaveClientID("recovered"); err != nil {
		t.Fatalf("saving over a corrupt file: %v", err)
	}
	if got := LoadClientID(); got != "recovered" {
		t.Errorf("LoadClientID() = %q, want the new value", got)
	}
}

func TestConfigFileIsReadableJSON(t *testing.T) {
	// The file is a thing a person might open when something is wrong, so it
	// should be indented rather than one long line.
	fakeConfigHome(t)

	if err := SaveClientID("abc123"); err != nil {
		t.Fatalf("saving: %v", err)
	}

	dir, _ := ConfigDir()
	contents, err := os.ReadFile(filepath.Join(dir, configFileName))
	if err != nil {
		t.Fatalf("reading back: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(contents, &decoded); err != nil {
		t.Fatalf("the file is not valid JSON: %v", err)
	}
	if decoded["client_id"] != "abc123" {
		t.Errorf("client_id = %v, want abc123", decoded["client_id"])
	}
	if !strings.Contains(string(contents), "\n") {
		t.Error("the file is a single line, want it indented for a human to read")
	}
}

func TestTokenCachePathSitsBesideTheConfig(t *testing.T) {
	fakeConfigHome(t)

	path, err := tokenCachePath()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	dir, err := ConfigDir()
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
