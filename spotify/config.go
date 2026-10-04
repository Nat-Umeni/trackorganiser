package spotify

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// configFileName sits inside ConfigDir alongside the token cache.
const configFileName = "config.json"

// config is what persists between runs. It is JSON rather than a bare string
// because the token cache already is, and a default download directory is the
// obvious next thing to want in here.
type config struct {
	ClientID string `json:"client_id"`
}

// ConfigDir returns the directory holding the client ID and the refresh token,
// creating it if needed. %AppData% on Windows, ~/Library/Application Support on
// macOS, ~/.config on Linux.
//
// 0700 rather than 0755 because a refresh token lives in here.
func ConfigDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("find config directory: %w", err)
	}

	dir := filepath.Join(base, "trackorganiser")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", fmt.Errorf("create config directory: %w", err)
	}

	return dir, nil
}

// loadConfig reads the stored settings. A missing file is not an error - it is
// the normal state on a first run - so it comes back as a zero config, the same
// way FirefoxCookiesAvailable treats a missing profile as a plain false.
func loadConfig() config {
	dir, err := ConfigDir()
	if err != nil {
		return config{}
	}

	contents, err := os.ReadFile(filepath.Join(dir, configFileName))
	if err != nil {
		return config{}
	}

	var stored config
	if err := json.Unmarshal(contents, &stored); err != nil {
		// A corrupt file is treated as an absent one. The cost is re-entering
		// the client ID; refusing to start would be worse.
		return config{}
	}

	return stored
}

// LoadClientID returns the saved Spotify client ID, or an empty string if there
// isn't one yet.
func LoadClientID() string {
	return loadConfig().ClientID
}

// SaveClientID stores the client ID so it only ever has to be supplied once.
//
// The value is trimmed because it arrives by copy-paste from whoever handed it
// over, and a trailing space produces an opaque Spotify rejection rather than a
// clear one.
func SaveClientID(clientID string) error {
	dir, err := ConfigDir()
	if err != nil {
		return err
	}

	// Read-modify-write rather than overwrite, so adding a second setting later
	// cannot quietly discard this one.
	stored := loadConfig()
	stored.ClientID = strings.TrimSpace(clientID)

	contents, err := json.MarshalIndent(stored, "", "  ")
	if err != nil {
		return fmt.Errorf("encode config: %w", err)
	}

	path := filepath.Join(dir, configFileName)
	if err := os.WriteFile(path, contents, 0600); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}

	return nil
}
