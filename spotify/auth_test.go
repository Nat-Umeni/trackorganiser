package spotify

import (
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

// A realistic auth URL: the "&" separators are the whole reason Windows keeps
// rundll32 rather than `cmd /c start`, which would read them as command chaining.
const testAuthURL = "https://accounts.spotify.com/authorize?client_id=abc123&response_type=code&redirect_uri=http%3A%2F%2F127.0.0.1%3A8080%2Fcallback&scope=playlist-read-private+playlist-read-collaborative"

func TestBrowserOpenCommand(t *testing.T) {
	tests := []struct {
		name     string
		goos     string
		wantName string
		wantArgs []string
	}{
		{
			name:     "linux",
			goos:     "linux",
			wantName: "xdg-open",
			wantArgs: []string{testAuthURL},
		},
		{
			name:     "macos",
			goos:     "darwin",
			wantName: "open",
			wantArgs: []string{testAuthURL},
		},
		{
			// The only platform that puts an argument before the URL.
			name:     "windows",
			goos:     "windows",
			wantName: "rundll32",
			wantArgs: []string{"url.dll,FileProtocolHandler", testAuthURL},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			name, args, err := browserOpenCommand(test.goos, testAuthURL)
			if err != nil {
				t.Fatalf("browserOpenCommand(%q) returned unexpected error: %v", test.goos, err)
			}
			if name != test.wantName {
				t.Errorf("command = %q, want %q", name, test.wantName)
			}
			if !slices.Equal(args, test.wantArgs) {
				t.Errorf("args = %v, want %v", args, test.wantArgs)
			}
		})
	}
}

func TestBrowserOpenCommandPassesTheUrlLast(t *testing.T) {
	// rundll32 puts a flag before the URL and the others don't, so "last
	// argument" is the invariant that holds across all of them.
	for _, goos := range []string{"linux", "darwin", "windows"} {
		t.Run(goos, func(t *testing.T) {
			_, args, err := browserOpenCommand(goos, testAuthURL)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(args) == 0 {
				t.Fatal("got no arguments, want at least the URL")
			}
			if last := args[len(args)-1]; last != testAuthURL {
				t.Errorf("last argument is %q, want the URL", last)
			}
		})
	}
}

func TestBrowserOpenCommandKeepsQuerySeparators(t *testing.T) {
	// If someone swaps Windows to `cmd /c start`, the "&" separators become
	// command chaining and the URL arrives truncated. This is the test that
	// should fail if that happens.
	for _, goos := range []string{"linux", "darwin", "windows"} {
		t.Run(goos, func(t *testing.T) {
			_, args, err := browserOpenCommand(goos, testAuthURL)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			url := args[len(args)-1]
			if strings.Count(url, "&") != strings.Count(testAuthURL, "&") {
				t.Errorf("URL lost its query separators: %q", url)
			}
			if !strings.Contains(url, "code_challenge") && !strings.Contains(url, "scope=") {
				t.Errorf("URL looks truncated: %q", url)
			}
		})
	}
}

func TestBrowserOpenCommandUnsupportedPlatform(t *testing.T) {
	// Never guess a command: a wrong one either does nothing or does something
	// unexpected, and openURL prints the URL instead, which always works.
	for _, goos := range []string{"plan9", "freebsd", ""} {
		t.Run(goos, func(t *testing.T) {
			name, args, err := browserOpenCommand(goos, testAuthURL)
			if err == nil {
				t.Fatalf("got (%q, %v, nil), want an error", name, args)
			}
			if name != "" {
				t.Errorf("got command %q alongside an error, want empty", name)
			}
			if args != nil {
				t.Errorf("got args %v alongside an error, want nil", args)
			}
			if !strings.Contains(err.Error(), goos) && goos != "" {
				t.Errorf("error %q does not name the platform", err)
			}
		})
	}
}

// captureStdout runs fn with os.Stdout redirected, returning what was written.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()

	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("setting up: %v", err)
	}

	previous := os.Stdout
	os.Stdout = writer
	fn()
	os.Stdout = previous
	writer.Close()

	var captured strings.Builder
	if _, err := io.Copy(&captured, reader); err != nil {
		t.Fatalf("reading captured output: %v", err)
	}

	return captured.String()
}

// stubStartCommand replaces the process launcher for one test and reports what
// it was asked to run. Without this, testing openURL would open a real browser.
//
// The result comes back on a channel rather than a slice because the auth flow
// runs in its own goroutine - sharing a slice across that boundary would be a
// data race, and -race would rightly complain.
func stubStartCommand(t *testing.T, err error) <-chan []string {
	t.Helper()

	launched := make(chan []string, 1)
	previous := startCommand
	startCommand = func(name string, args ...string) error {
		select {
		case launched <- append([]string{name}, args...):
		default: // only the first launch is of interest
		}
		return err
	}
	t.Cleanup(func() { startCommand = previous })

	return launched
}

// awaitLaunch waits for the stubbed launcher to be called and returns the URL it
// was given, which is the last argument on every platform.
func awaitLaunch(t *testing.T, launched <-chan []string) string {
	t.Helper()

	select {
	case args := <-launched:
		if len(args) == 0 {
			t.Fatal("launcher was called with no arguments")
		}
		return args[len(args)-1]
	case <-time.After(5 * time.Second):
		t.Fatal("nothing was launched")
		return ""
	}
}

func TestOpenURLLaunchesTheBrowser(t *testing.T) {
	launched := stubStartCommand(t, nil)

	output := captureStdout(t, func() { openURL(testAuthURL) })

	if got := awaitLaunch(t, launched); got != testAuthURL {
		t.Errorf("launched with %q, want the URL", got)
	}
	// Nothing to say when it worked - the browser is the feedback.
	if output != "" {
		t.Errorf("printed %q on success, want nothing", output)
	}
}

func TestOpenURLPrintsWhenLaunchFails(t *testing.T) {
	// A browser that won't start is survivable: the callback server is already
	// listening, so a URL you can paste completes the flow just as well.
	stubStartCommand(t, errors.New("exec: xdg-open: executable file not found"))

	output := captureStdout(t, func() { openURL(testAuthURL) })

	if !strings.Contains(output, testAuthURL) {
		t.Errorf("output %q does not contain the URL to paste", output)
	}
	if !strings.Contains(strings.ToLower(output), "couldn't open") {
		t.Errorf("output %q does not explain what happened", output)
	}
}

func TestGenerateCodeVerifier(t *testing.T) {
	// PKCE requires 43-128 characters from an unreserved set.
	const allowed = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-._~"

	first, err := generateCodeVerifier()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(first) != 64 {
		t.Errorf("length %d, want 64", len(first))
	}
	for _, r := range first {
		if !strings.ContainsRune(allowed, r) {
			t.Errorf("verifier contains %q, which is not PKCE-safe", r)
		}
	}

	second, err := generateCodeVerifier()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if first == second {
		t.Error("two verifiers were identical, which defeats the point of PKCE")
	}
}

func TestGenerateState(t *testing.T) {
	first, err := generateState()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(first) != 16 {
		t.Errorf("length %d, want 16", len(first))
	}

	second, err := generateState()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if first == second {
		t.Error("two states were identical, so it could not detect a replay")
	}
}

func TestGenerateCodeChallenge(t *testing.T) {
	// Computed independently rather than copied from the implementation, so the
	// test would catch the encoding being changed to StdEncoding (which pads with
	// "=" and uses "+/" - both invalid in a URL).
	const verifier = "abc123"
	sum := sha256.Sum256([]byte(verifier))
	want := base64.RawURLEncoding.EncodeToString(sum[:])

	got := generateCodeChallenge(verifier)
	if got != want {
		t.Errorf("generateCodeChallenge(%q) = %q, want %q", verifier, got, want)
	}
	if strings.ContainsAny(got, "+/=") {
		t.Errorf("challenge %q contains characters that are unsafe in a URL", got)
	}
	if generateCodeChallenge("different") == got {
		t.Error("different verifiers produced the same challenge")
	}
}

// callCallback plays the part of the browser redirecting back to us.
//
// Keep-alives are disabled deliberately. Every one of these tests binds the same
// port, and a pooled connection from a previous test would carry the request to
// that test's handler instead of the one currently listening - which looks
// exactly like the auth flow hanging.
func callCallback(t *testing.T, query string) {
	t.Helper()

	client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}
	resp, err := client.Get("http://127.0.0.1:8080/callback" + query)
	if err != nil {
		t.Fatalf("calling the callback: %v", err)
	}
	resp.Body.Close()
}

func TestInitiateSpotifyAuthReturnsTheCode(t *testing.T) {
	// Stub the launcher and capture the URL it would have opened, so the flow can
	// be driven without a browser.
	launched := stubStartCommand(t, nil)

	type result struct {
		code     string
		verifier string
		err      error
	}
	done := make(chan result, 1)

	go func() {
		code, verifier, _, err := initiateSpotifyAuth("test-client-id")
		done <- result{code, verifier, err}
	}()

	// Wait for the callback server to come up and the URL to be handed over.
	authURL := awaitLaunch(t, launched)

	// The URL is what Spotify would receive, so it is worth checking properly.
	parsed, err := url.Parse(authURL)
	if err != nil {
		t.Fatalf("auth URL does not parse: %v", err)
	}
	query := parsed.Query()
	for field, want := range map[string]string{
		"client_id":             "test-client-id",
		"response_type":         "code",
		"redirect_uri":          "http://127.0.0.1:8080/callback",
		"code_challenge_method": "S256",
	} {
		if got := query.Get(field); got != want {
			t.Errorf("auth URL %s = %q, want %q", field, got, want)
		}
	}
	if query.Get("code_challenge") == "" {
		t.Error("auth URL has no code_challenge, so PKCE is not in play")
	}
	if query.Get("state") == "" {
		t.Error("auth URL has no state")
	}

	callCallback(t, "?code=test-auth-code&state="+query.Get("state"))

	select {
	case got := <-done:
		if got.err != nil {
			t.Fatalf("unexpected error: %v", got.err)
		}
		if got.code != "test-auth-code" {
			t.Errorf("code = %q, want the one the callback supplied", got.code)
		}
		// The verifier must be the one whose challenge went to Spotify, or the
		// token exchange would be rejected.
		if generateCodeChallenge(got.verifier) != query.Get("code_challenge") {
			t.Error("returned verifier does not match the challenge that was sent")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("initiateSpotifyAuth did not return after the callback")
	}
}

func TestInitiateSpotifyAuthRejectsAnEmptyCode(t *testing.T) {
	launched := stubStartCommand(t, nil)

	done := make(chan error, 1)
	go func() {
		_, _, _, err := initiateSpotifyAuth("test-client-id")
		done <- err
	}()

	awaitLaunch(t, launched)

	// The callback forwards whatever it got, including nothing - a user who
	// denies access lands here.
	callCallback(t, "")

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("got no error for an empty code, want one")
		}
		if !strings.Contains(err.Error(), "no code") {
			t.Errorf("error %q does not explain that no code arrived", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("initiateSpotifyAuth did not return")
	}
}

func TestInitiateSpotifyAuthFailsWhenThePortIsTaken(t *testing.T) {
	// Only one auth flow can own 127.0.0.1:8080, and the redirect_uri registered
	// with Spotify pins the port - so a clash has to surface, not hang.
	blocker, err := net.Listen("tcp", "127.0.0.1:8080")
	if err != nil {
		t.Skipf("could not occupy the port to test the clash: %v", err)
	}
	defer blocker.Close()

	stubStartCommand(t, nil)

	_, _, _, err = initiateSpotifyAuth("test-client-id")
	if err == nil {
		t.Fatal("got no error with the port already in use, want one")
	}
	if !strings.Contains(err.Error(), "callback server") {
		t.Errorf("error %q does not point at the callback server", err)
	}
}

// failingReader stands in for a broken OS entropy source, which is the only way
// crypto/rand actually errors.
type failingReader struct{}

func (failingReader) Read([]byte) (int, error) {
	return 0, errors.New("entropy source unavailable")
}

// stubRandomReader makes the PKCE generators fail for one test.
func stubRandomReader(t *testing.T) {
	t.Helper()

	previous := randomReader
	randomReader = failingReader{}
	t.Cleanup(func() { randomReader = previous })
}

func TestGenerateCodeVerifierWhenRandomnessFails(t *testing.T) {
	stubRandomReader(t)

	got, err := generateCodeVerifier()
	if err == nil {
		t.Fatalf("got %q, want an error", got)
	}
	if got != "" {
		t.Errorf("got %q alongside an error, want empty", got)
	}
	if !strings.Contains(err.Error(), "random") {
		t.Errorf("error %q does not explain what failed", err)
	}
}

func TestGenerateStateWhenRandomnessFails(t *testing.T) {
	stubRandomReader(t)

	got, err := generateState()
	if err == nil {
		t.Fatalf("got %q, want an error", got)
	}
	if got != "" {
		t.Errorf("got %q alongside an error, want empty", got)
	}
}

func TestInitiateSpotifyAuthWhenRandomnessFails(t *testing.T) {
	// Without a verifier there is no PKCE challenge, so there is no point
	// opening a browser or binding a port - it has to give up here.
	stubRandomReader(t)

	_, _, _, err := initiateSpotifyAuth("test-client-id")
	if err == nil {
		t.Fatal("got no error when randomness failed, want one")
	}
	if !strings.Contains(err.Error(), "verifier") {
		t.Errorf("error %q does not say which step failed", err)
	}
}

func TestInitiateSpotifyAuthWithAnUnparseableEndpoint(t *testing.T) {
	previous := spotifyAuthorizeURL
	spotifyAuthorizeURL = ":"
	t.Cleanup(func() { spotifyAuthorizeURL = previous })

	_, _, _, err := initiateSpotifyAuth("test-client-id")
	if err == nil {
		t.Fatal("got no error for an unparseable endpoint, want one")
	}
	if !strings.Contains(err.Error(), "auth URL") {
		t.Errorf("error %q does not point at the URL", err)
	}
}

func TestStartCommandRunsARealProcess(t *testing.T) {
	// Every other test stubs startCommand, so the real launcher would otherwise
	// never run. /bin/true exits 0 immediately and touches nothing.
	if runtime.GOOS == "windows" {
		t.Skip("/bin/true is not a thing on Windows")
	}

	if err := startCommand("/bin/true"); err != nil {
		t.Errorf("startCommand(/bin/true) = %v, want nil", err)
	}

	if err := startCommand("/definitely/not/a/real/binary"); err == nil {
		t.Error("launching a missing binary returned no error, want one")
	}
}

// exhaustibleReader supplies a fixed byte for a set number of reads, then fails.
//
// The byte is constant so the count is deterministic: crypto/rand.Int rejects
// and retries when a draw lands above its maximum, and a value of 1 never does.
// That makes "reads" exactly one per character generated.
type exhaustibleReader struct {
	reads     int
	failAfter int
}

func (r *exhaustibleReader) Read(p []byte) (int, error) {
	if r.reads >= r.failAfter {
		return 0, errors.New("entropy source exhausted")
	}
	r.reads++

	for i := range p {
		p[i] = 1
	}

	return len(p), nil
}

func TestInitiateSpotifyAuthWhenStateGenerationFails(t *testing.T) {
	// The verifier is generated first, so making randomness fail outright never
	// reaches the state. Let it survive exactly long enough for the verifier and
	// no longer.
	counter := &exhaustibleReader{failAfter: 1 << 30}
	previous := randomReader
	randomReader = counter
	if _, err := generateCodeVerifier(); err != nil {
		randomReader = previous
		t.Fatalf("measuring the verifier's reads: %v", err)
	}
	verifierReads := counter.reads

	randomReader = &exhaustibleReader{failAfter: verifierReads}
	t.Cleanup(func() { randomReader = previous })

	_, _, _, err := initiateSpotifyAuth("test-client-id")
	if err == nil {
		t.Fatal("got no error when state generation failed, want one")
	}
	if !strings.Contains(err.Error(), "state") {
		t.Errorf("error %q does not say which step failed", err)
	}
}
