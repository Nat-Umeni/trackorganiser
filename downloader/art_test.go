package downloader

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
)

// fakeCDN stands in for Spotify's image host, counting requests so the cache can
// be proven to work. Nothing here touches the network.
func fakeCDN(t *testing.T, body []byte, status int) (url string, requests *atomic.Int64) {
	t.Helper()

	var count atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count.Add(1)
		w.WriteHeader(status)
		w.Write(body)
	}))
	t.Cleanup(server.Close)

	return server.URL, &count
}

func TestCoverArtCacheFetches(t *testing.T) {
	want := []byte("pretend this is a jpeg")
	url, requests := fakeCDN(t, want, http.StatusOK)

	cache := newCoverArtCache()

	got, err := cache.lookUp(url)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(got) != string(want) {
		t.Errorf("got %q, want %q", got, want)
	}
	if requests.Load() != 1 {
		t.Errorf("made %d requests, want 1", requests.Load())
	}
}

func TestCoverArtCacheServesTheSecondCallFromMemory(t *testing.T) {
	// The whole reason the cache exists: a playlist has several tracks per
	// album, and each would otherwise fetch the same cover again.
	want := []byte("pretend this is a jpeg")
	url, requests := fakeCDN(t, want, http.StatusOK)

	cache := newCoverArtCache()

	for range 5 {
		got, err := cache.lookUp(url)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if string(got) != string(want) {
			t.Errorf("got %q, want %q", got, want)
		}
	}

	if requests.Load() != 1 {
		t.Errorf("made %d requests for 5 lookups, want 1", requests.Load())
	}
}

func TestCoverArtCacheRejectsANonOKResponse(t *testing.T) {
	// A 404 page written into an mp3 as a cover is the worst outcome here, so
	// the status has to be checked before the body is read.
	url, _ := fakeCDN(t, []byte("<html>not found</html>"), http.StatusNotFound)

	cache := newCoverArtCache()

	got, err := cache.lookUp(url)
	if err == nil {
		t.Fatalf("got %q with no error, want an error", got)
	}
	if got != nil {
		t.Errorf("got %q alongside an error, want nothing", got)
	}
}

func TestCoverArtCacheDoesNotRetryAFailure(t *testing.T) {
	// One dead album can have 40 tracks. Without caching the failure, each of
	// them would hit the same broken URL again.
	url, requests := fakeCDN(t, nil, http.StatusNotFound)

	cache := newCoverArtCache()

	if _, err := cache.lookUp(url); err == nil {
		t.Fatal("first lookup returned no error, want one")
	}

	// The second call finds the cached empty entry, so no error and no bytes -
	// callers check the bytes before writing a frame.
	got, err := cache.lookUp(url)
	if err != nil {
		t.Errorf("second lookup returned %v, want the cached empty entry", err)
	}
	if len(got) != 0 {
		t.Errorf("second lookup returned %d bytes, want none", len(got))
	}

	if requests.Load() != 1 {
		t.Errorf("made %d requests, want 1 - the failure was not cached", requests.Load())
	}
}

func TestCoverArtCacheUnderConcurrency(t *testing.T) {
	// This is what -jobs 16 actually does. Run under -race: a map written from
	// several goroutines without the mutex is one of the few things Go panics on
	// outright rather than corrupting quietly.
	want := []byte("pretend this is a jpeg")
	url, _ := fakeCDN(t, want, http.StatusOK)

	cache := newCoverArtCache()

	var waiting sync.WaitGroup
	for range 16 {
		waiting.Go(func() {
			if got, err := cache.lookUp(url); err != nil || string(got) != string(want) {
				t.Errorf("lookUp() = (%q, %v), want the image and no error", got, err)
			}
		})
	}
	waiting.Wait()
}

func TestCoverArtCacheWithDifferentURLs(t *testing.T) {
	// Distinct albums must not collide: the URL is the key, so two different
	// covers have to come back differently.
	var servers []string
	for index := range 3 {
		url, _ := fakeCDN(t, []byte(fmt.Sprintf("image %d", index)), http.StatusOK)
		servers = append(servers, url)
	}

	cache := newCoverArtCache()

	for index, url := range servers {
		got, err := cache.lookUp(url)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if want := fmt.Sprintf("image %d", index); string(got) != want {
			t.Errorf("got %q, want %q", got, want)
		}
	}
}

func TestCoverArtCacheOnAnUnreachableHost(t *testing.T) {
	// Not an HTTP error but a transport one - http.Get itself fails, and the
	// response is nil, so it must not be dereferenced.
	cache := newCoverArtCache()

	got, err := cache.lookUp("http://127.0.0.1:1/nothing-listening")
	if err == nil {
		t.Fatalf("got %q with no error, want an error", got)
	}
}
