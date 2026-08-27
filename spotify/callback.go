package spotify

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
)

// startCallbackServer starts a temporary HTTP server on the given address.
// It returns a channel that will receive the "code" value, and a function to shut the server down.
func startCallbackServer(address string) (chan string, func(), error) {
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return nil, nil, fmt.Errorf("cannot listen on %s: %w", address, err)
	}

	codeChan := make(chan string, 1) // a pipe for one single code

	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		code := r.URL.Query().Get("code") // like $_GET['code'] in PHP
		if code == "" {
			http.Error(w, "missing code", http.StatusBadRequest)
			fmt.Fprint(w, "Error: No code received.")
		} else {
			fmt.Fprint(w, "Success! You can close this window.")
		}
		codeChan <- code // send whatever we got (even if empty)
	})

	// Run the server in the background
	go http.Serve(listener, mux)

	// This function shuts down the server
	shutdown := func() {
		listener.Close()
	}

	return codeChan, shutdown, nil
}

// exchangeCodeForTokens swaps the authorization code + verifier for an access and refresh token.
func exchangeCodeForTokens(clientID, code, verifier string) (accessToken, refreshToken string, err error) {
	data := url.Values{}
	data.Set("client_id", clientID)
	data.Set("grant_type", "authorization_code")
	data.Set("code", code)
	data.Set("redirect_uri", "http://127.0.0.1:8080/callback")
	data.Set("code_verifier", verifier)

	resp, err := http.PostForm("https://accounts.spotify.com/api/token", data)
	if err != nil {
		return "", "", fmt.Errorf("token exchange request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", "", fmt.Errorf("read token response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("token endpoint error %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", "", fmt.Errorf("parse token JSON: %w", err)
	}

	// fmt.Println("DEBUG exchange: refresh_token =", result.RefreshToken[:15]+"...")

	return result.AccessToken, result.RefreshToken, nil
}
