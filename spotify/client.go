package spotify

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

const tokenURL = "https://accounts.spotify.com/api/token"

type spotifyTokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int    `json:"expires_in"`
}

type Client struct {
	httpClient   *http.Client
	clientID     string
	clientSecret string
	tokenDetails spotifyTokenResponse
	expiry       time.Time
}

func NewClient(clientID, clientSecret string) (*Client, error) {
	if clientID == "" || clientSecret == "" {
		return nil, fmt.Errorf("SPOTIFY_CLIENT_ID and SPOTIFY_CLIENT_SECRET must be set")
	}

	return &Client{
		httpClient:   &http.Client{Timeout: 10 * time.Second},
		clientID:     clientID,
		clientSecret: clientSecret,
	}, nil
}

func (c *Client) refreshToken() error {
	data := url.Values{}
	data.Set("grant_type", "client_credentials")
	data.Set("client_id", c.clientID)
	data.Set("client_secret", c.clientSecret)

	resp, err := c.httpClient.PostForm(tokenURL, data)
	if err != nil {
		return fmt.Errorf("Request failed while refreshing token: %w", err)
	}

	defer resp.Body.Close()
	
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("Request failed while reading refresh token response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Spotify API error (status %d): %s", resp.StatusCode, string(body))
	}

	var tokenResponse spotifyTokenResponse
	if err := json.Unmarshal(body, &tokenResponse); err != nil {
		return fmt.Errorf("Failed to parse token response JSON: %w", err)
	}

	c.tokenDetails = tokenResponse
	c.expiry = time.Now().Add(time.Duration(tokenResponse.ExpiresIn) * time.Second)

	return nil

}

func (c *Client) ensureToken() error {
	if c.tokenDetails.AccessToken != "" && time.Now().Before(c.expiry) {
		return nil
	}

	return c.refreshToken()
}