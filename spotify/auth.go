package spotify

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"math/big"
	"net/url"
	"os/exec"
	"runtime"
)

// randomReader is where the PKCE randomness comes from. It is a variable so
// tests can substitute a failing reader - the error paths below are otherwise
// unreachable, since crypto/rand only fails if the OS entropy source is broken.
var randomReader io.Reader = rand.Reader

// spotifyAuthorizeURL is a variable for the same reason callSpotify's base URL
// wants to be one: a hardcoded constant cannot be pointed somewhere else in a
// test.
var spotifyAuthorizeURL = "https://accounts.spotify.com/authorize"

// generateCodeVerifier creates a high‑entropy random string for PKCE.
func generateCodeVerifier() (string, error) {
	const length = 64
	const charset = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-._~"
	verifier := make([]byte, length)
	for i := range verifier {
		num, err := rand.Int(randomReader, big.NewInt(int64(len(charset))))
		if err != nil {
			return "", fmt.Errorf("random generation failed: %w", err)
		}
		verifier[i] = charset[num.Int64()]
	}
	return string(verifier), nil
}

func generateState() (string, error) {
	const length = 16
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	result := make([]byte, length)
	for i := range result {
		num, err := rand.Int(randomReader, big.NewInt(int64(len(charset))))
		if err != nil {
			return "", err
		}
		result[i] = charset[num.Int64()]
	}
	return string(result), nil
}

// generateCodeChallenge creates the SHA256 hash of the verifier and base64url‑encodes it.
func generateCodeChallenge(verifier string) string {
	hash := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(hash[:])
}

func initiateSpotifyAuth(clientID string) (code, codeVerifier, state string, err error) {
	// 1. Handle auth requirements for spotify
	verifier, err := generateCodeVerifier()
	if err != nil {
		return "", "", "", fmt.Errorf("generate verifier: %w", err)
	}

	stateVal, err := generateState()
	if err != nil {
		return "", "", "", fmt.Errorf("generate state: %w", err)
	}

	codeChallenge := generateCodeChallenge(verifier)

	authURL, err := url.Parse(spotifyAuthorizeURL)
	if err != nil {
		return "", "", "", fmt.Errorf("parse auth URL: %w", err)
	}

	query := authURL.Query()
	query.Set("client_id", clientID)
	query.Set("response_type", "code")
	query.Set("redirect_uri", "http://127.0.0.1:8080/callback")
	query.Set("state", stateVal)
	query.Set("scope", "playlist-read-private playlist-read-collaborative user-library-read")
	query.Set("code_challenge_method", "S256")
	query.Set("code_challenge", codeChallenge)
	authURL.RawQuery = query.Encode()

	// 2. Start the callback server
	codeChan, shutdown, err := startCallbackServer("127.0.0.1:8080")
	if err != nil {
		return "", "", "", fmt.Errorf("start callback server: %w", err)
	}
	defer shutdown() // clean up no matter what

	// 3. Open browser. Failing to launch one is not fatal: the callback server
	// above is already listening and the wait below still works, so printing the
	// URL to paste by hand keeps the flow usable over SSH or on a headless box.
	openURL(authURL.String())

	// 4. Wait for the authorization code
	fmt.Println("Waiting for you to authorize in the browser...")
	code = <-codeChan
	if code == "" {
		return "", "", "", fmt.Errorf("authorization failed: no code received")
	}

	return code, verifier, state, nil
}

// browserOpenCommand names the command that opens a URL in the user's default
// browser on the given platform, with its full argument list.
//
// goos is a parameter rather than runtime.GOOS so every platform is assertable
// from one machine, the same as ytdlpAssetName and ffmpegInstallHint.
//
// Windows keeps rundll32 rather than the more common `cmd /c start`: the auth URL
// is full of "&" query separators, which cmd treats as command chaining unless
// carefully quoted. rundll32 takes the URL as one plain argument and sidesteps
// the problem.
func browserOpenCommand(goos, rawURL string) (string, []string, error) {
	switch goos {
	case "linux":
		return "xdg-open", []string{rawURL}, nil
	case "darwin":
		return "open", []string{rawURL}, nil
	case "windows":
		return "rundll32", []string{"url.dll,FileProtocolHandler", rawURL}, nil
	default:
		return "", nil, fmt.Errorf("no known way to open a browser on %s", goos)
	}
}

// startCommand launches a process without waiting for it. It is a variable so
// tests can swap it out - otherwise testing openURL would genuinely open a
// browser on the machine running the tests.
//
// Start rather than Run: the browser outlives us, and waiting for it to exit
// would hang forever.
var startCommand = func(name string, args ...string) error {
	return exec.Command(name, args...).Start()
}

// openURL tries to launch a browser, and falls back to printing the URL. It
// deliberately returns nothing: there is no failure here worth aborting for,
// because a URL you can read is a URL you can paste.
func openURL(rawURL string) {
	name, args, err := browserOpenCommand(runtime.GOOS, rawURL)
	if err == nil && startCommand(name, args...) == nil {
		return
	}

	fmt.Printf("\nCouldn't open a browser automatically. Open this to authorise:\n\n  %s\n\n", rawURL)
}
