package spotify

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"math/big"
	"net/url"
	"os/exec"
)

// generateCodeVerifier creates a high‑entropy random string for PKCE.
func generateCodeVerifier() (string, error) {
	const length = 64
	const charset = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-._~"
	verifier := make([]byte, length)
	for i := range verifier {
		num, err := rand.Int(rand.Reader, big.NewInt(int64(len(charset))))
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
		num, err := rand.Int(rand.Reader, big.NewInt(int64(len(charset))))
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

	authURL, err := url.Parse("https://accounts.spotify.com/authorize")
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

	// 3. Open browser
	cmd := exec.Command("rundll32", "url.dll,FileProtocolHandler", authURL.String())
	if err := cmd.Start(); err != nil {
		return "", "", "", fmt.Errorf("open browser: %w", err)
	}

	// 4. Wait for the authorization code
	fmt.Println("Waiting for you to authorize in the browser...")
	code = <-codeChan
	if code == "" {
		return "", "", "", fmt.Errorf("authorization failed: no code received")
	}

	return code, verifier, state, nil
}
