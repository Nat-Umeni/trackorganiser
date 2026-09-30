package downloader

import (
	"fmt"
	"io"
	"net/http"
	"sync"
)

type coverArtCache struct {
	coverArtsLocker sync.Mutex
	coverArts       map[string][]byte
}

func newCoverArtCache() *coverArtCache {
	return &coverArtCache{
		coverArts: make(map[string][]byte),
	}
}

func (c *coverArtCache) lookUp(url string) ([]byte, error) {
	c.coverArtsLocker.Lock()
	existing, found := c.coverArts[url]
	c.coverArtsLocker.Unlock()

	if found {
		return existing, nil
	}

	coverArtFromFetch, err := getCoverURLFetch(url)
	if err != nil {
		c.coverArtsLocker.Lock()
		c.coverArts[url] = nil
		c.coverArtsLocker.Unlock()

		return nil, err
	}

	c.coverArtsLocker.Lock()
	c.coverArts[url] = coverArtFromFetch
	c.coverArtsLocker.Unlock()

	return coverArtFromFetch, nil
}

func getCoverURLFetch(url string) ([]byte, error) {
	resp, err := http.Get(url)
	if err != nil {
		return nil, err
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("downloading %s: HTTP %s", url, resp.Status)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read cover url response: %w", err)
	}

	return body, nil
}
