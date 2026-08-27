package spotify

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

type spotifyTokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token,omitempty"`
}

type Client struct {
	httpClient        *http.Client
	clientID          string
	savedRefreshToken string
	tokenDetails      spotifyTokenResponse
	expiry            time.Time
}

const tokenCacheFile = "spotify_token.json"

type tokenCache struct {
	RefreshToken string `json:"refresh_token"`
}

func NewClient(clientID string) (*Client, error) {
	if clientID == "" {
		return nil, fmt.Errorf("SPOTIFY_CLIENT_ID must be set")
	}

	c := &Client{
		httpClient: &http.Client{Timeout: 10 * time.Second},
		clientID:   clientID,
	}

	// Try to load an existing refresh token
	if err := c.loadToken(); err == nil {
		// fmt.Println("Loaded token:", c.savedRefreshToken[:10]+"...")
		// Verify the token is still valid by attempting to refresh
		if refreshErr := c.refreshToken(); refreshErr == nil {
			// fmt.Println("Token valid, no browser needed.")
			return c, nil // <-- success, we're done
		} else {
			// fmt.Println("Stored token invalid:", refreshErr)
			// Token revoked or invalid – delete it and continue to full auth
			c.deleteTokenCache()
			c.savedRefreshToken = ""
		}
	} else {
		fmt.Println("No token found, will authenticate via browser.")
	}

	// Full browser auth flow
	code, verifier, _, err := initiateSpotifyAuth(clientID)
	if err != nil {
		return nil, fmt.Errorf("authorization flow: %w", err)
	}

	_, refreshToken, err := exchangeCodeForTokens(clientID, code, verifier)
	if err != nil {
		return nil, fmt.Errorf("token exchange: %w", err)
	}

	if err := c.saveToken(refreshToken); err != nil {
		return nil, fmt.Errorf("save token: %w", err)
	}
	// fmt.Println("New token saved:", refreshToken[:15]+"...")

	c.savedRefreshToken = refreshToken
	// Get an initial access token
	if err := c.refreshToken(); err != nil {
		return nil, fmt.Errorf("initial access token: %w", err)
	}

	return c, nil
}

func (c *Client) refreshToken() error {
	if c.savedRefreshToken == "" {
		return fmt.Errorf("no refresh token set; please authenticate first")
	}

	data := url.Values{}
	data.Set("grant_type", "refresh_token")
	data.Set("refresh_token", c.savedRefreshToken)
	data.Set("client_id", c.clientID)

	resp, err := c.httpClient.PostForm("https://accounts.spotify.com/api/token", data)
	if err != nil {
		return fmt.Errorf("token refresh request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read token response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Spotify token error (status %d): %s", resp.StatusCode, string(body))
	}

	var tokenResponse spotifyTokenResponse
	if err := json.Unmarshal(body, &tokenResponse); err != nil {
		return fmt.Errorf("parse token JSON: %w", err)
	}

	// Update access token and expiry
	c.tokenDetails = tokenResponse
	c.expiry = time.Now().Add(time.Duration(tokenResponse.ExpiresIn) * time.Second)

	// If Spotify rotated the refresh token, update the stored one
	if tokenResponse.RefreshToken != "" {
		c.savedRefreshToken = tokenResponse.RefreshToken
		if err := c.saveToken(tokenResponse.RefreshToken); err != nil {
			return fmt.Errorf("save rotated refresh token: %w", err)
		}
	}

	return nil
}

func (c *Client) ensureToken() error {
	if c.tokenDetails.AccessToken != "" && time.Now().Before(c.expiry) {
		return nil
	}

	return c.refreshToken()
}

// loadToken reads the refresh token from disk.
func (c *Client) loadToken() error {
	data, err := os.ReadFile(tokenCacheFile)
	if err != nil {
		return err
	}
	var cache tokenCache
	if err := json.Unmarshal(data, &cache); err != nil {
		return err
	}
	c.savedRefreshToken = cache.RefreshToken
	return nil
}

// saveToken writes the refresh token to disk.
func (c *Client) saveToken(token string) error {
	cache := tokenCache{RefreshToken: token}
	data, err := json.Marshal(cache)
	if err != nil {
		return err
	}
	return os.WriteFile(tokenCacheFile, data, 0600)
}

func (c *Client) callSpotify(method string, endpoint string, body interface{}) (*http.Response, error) {
	var requestBody io.Reader

	if err := c.ensureToken(); err != nil {
		return nil, fmt.Errorf("ensure token: %w", err)
	}

	fullURL := "https://api.spotify.com/v1/" + strings.TrimLeft(endpoint, "/")
	if body != nil {
		jsonData, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal body: %w", err)
		}

		requestBody = bytes.NewBuffer(jsonData)
	}

	request, err := http.NewRequest(method, fullURL, requestBody)
	if err != nil {
		return nil, fmt.Errorf("create request %w", err)
	}

	request.Header.Set("Authorization", "Bearer "+c.tokenDetails.AccessToken)
	request.Header.Set("Content-Type", "application/json")

	return c.httpClient.Do(request)
}

func (c *Client) deleteTokenCache() {
	os.Remove(tokenCacheFile)
}
