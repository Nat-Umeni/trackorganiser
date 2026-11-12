package updater

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fakeRelease serves files the way GitHub lays a release out - /<tag>/<name> -
// and points downloadBaseURL at it. Anything not in files is a 404, which is
// what GitHub gives for an asset a release doesn't have.
func fakeRelease(t *testing.T, tag string, files map[string][]byte) {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name, underTag := strings.CutPrefix(r.URL.Path, "/"+tag+"/")
		body, exists := files[name]
		if !underTag || !exists {
			http.NotFound(w, r)
			return
		}
		w.Write(body)
	}))
	t.Cleanup(server.Close)

	original := downloadBaseURL
	downloadBaseURL = server.URL + "/"
	t.Cleanup(func() { downloadBaseURL = original })
}

// fakeExecutable plants a file standing in for the running binary and points
// executablePath at it, so Apply swaps a temp file rather than the test binary.
func fakeExecutable(t *testing.T, contents string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "trackorganiser")
	if err := os.WriteFile(path, []byte(contents), 0755); err != nil {
		t.Fatalf("planting the fake executable: %v", err)
	}

	original := executablePath
	executablePath = func() (string, error) { return path, nil }
	t.Cleanup(func() { executablePath = original })

	return path
}

// sumsLine is one line of sha256sum output for contents.
func sumsLine(name string, contents []byte) []byte {
	sum := sha256.Sum256(contents)
	return []byte(hex.EncodeToString(sum[:]) + "  " + name + "\n")
}

// thisPlatformsAsset is what Apply will ask for on the machine running the test.
func thisPlatformsAsset(t *testing.T) string {
	t.Helper()

	name, err := assetName(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		t.Skipf("no release is built for %s/%s", runtime.GOOS, runtime.GOARCH)
	}

	return name
}

func assertGone(t *testing.T, path string) {
	t.Helper()

	if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("%s was left behind (stat error: %v)", filepath.Base(path), err)
	}
}

func TestAssetName(t *testing.T) {
	tests := []struct {
		goos    string
		arch    string
		want    string
		wantErr bool
	}{
		{goos: "windows", arch: "amd64", want: "trackorganiser-windows-amd64.exe"},
		{goos: "windows", arch: "arm64", want: "trackorganiser-windows-arm64.exe"},
		{goos: "linux", arch: "amd64", want: "trackorganiser-linux-amd64"},
		{goos: "linux", arch: "arm64", want: "trackorganiser-linux-arm64"},
		{goos: "darwin", arch: "arm64", want: "trackorganiser-macos-arm64"},

		// Intel Macs aren't built (build.sh: "one line to add if anyone asks"),
		// so there is nothing to download - an error, not a 404 later.
		{goos: "darwin", arch: "amd64", wantErr: true},
		{goos: "linux", arch: "386", wantErr: true},
		{goos: "freebsd", arch: "amd64", wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.goos+"/"+test.arch, func(t *testing.T) {
			got, err := assetName(test.goos, test.arch)

			if test.wantErr {
				if err == nil {
					t.Errorf("got %q, want an error", got)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != test.want {
				t.Errorf("got %q, want %q", got, test.want)
			}
		})
	}
}

// The names are written twice - in build.sh, which names the files, and here,
// which downloads them. Renaming one without the other would pass every other
// test and then 404 for everyone on the next release.
func TestAssetNamesMatchBuildScript(t *testing.T) {
	script, err := os.ReadFile("../build.sh")
	if err != nil {
		t.Fatalf("reading build.sh: %v", err)
	}

	platforms := [][2]string{
		{"windows", "amd64"}, {"windows", "arm64"},
		{"linux", "amd64"}, {"linux", "arm64"},
		{"darwin", "arm64"},
	}

	for _, platform := range platforms {
		name, err := assetName(platform[0], platform[1])
		if err != nil {
			t.Errorf("%s/%s: %v", platform[0], platform[1], err)
			continue
		}

		if !strings.Contains(string(script), name) {
			t.Errorf("build.sh never produces %s, so updating on %s/%s would 404", name, platform[0], platform[1])
		}
	}
}

func TestIsNewer(t *testing.T) {
	tests := []struct {
		name    string
		current string
		latest  string
		want    bool
	}{
		{name: "patch bump", current: "v0.1.0", latest: "v0.1.1", want: true},
		{name: "minor bump", current: "v0.1.0", latest: "v0.2.0", want: true},
		{name: "major bump", current: "v0.9.9", latest: "v1.0.0", want: true},
		{name: "same version", current: "v0.2.0", latest: "v0.2.0", want: false},
		{name: "latest is older", current: "v0.3.0", latest: "v0.2.0", want: false},

		// The case a string comparison gets wrong: "1" sorts before "9".
		{name: "numeric not alphabetical", current: "v0.9.0", latest: "v0.10.0", want: true},
		{name: "numeric not alphabetical, reversed", current: "v0.10.0", latest: "v0.9.0", want: false},

		// Local builds never get offered anything.
		{name: "dev build", current: "dev", latest: "v0.2.0", want: false},
		{name: "dirty tree", current: "55b099c-dirty", latest: "v0.2.0", want: false},
		{name: "commits past a tag", current: "v0.1.0-3-gabc123", latest: "v0.2.0", want: false},

		// And a strange tag on GitHub's side is ignored rather than installed.
		{name: "unparseable latest", current: "v0.1.0", latest: "latest", want: false},
		{name: "two-part latest", current: "v0.1.0", latest: "v0.2", want: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := IsNewer(test.current, test.latest); got != test.want {
				t.Errorf("IsNewer(%q, %q) = %v, want %v", test.current, test.latest, got, test.want)
			}
		})
	}
}

func TestIsRelease(t *testing.T) {
	tests := []struct {
		version string
		want    bool
	}{
		{version: "v0.1.0", want: true},
		{version: "v12.34.56", want: true},
		{version: "dev", want: false},
		{version: "0.1.0", want: false}, // release.yml only fires on v* tags
		{version: "v0.1", want: false},
		{version: "v0.1.0.0", want: false},
		{version: "v0.1.0-rc1", want: false},
		{version: "v0.-1.0", want: false},
		{version: "55b099c-dirty", want: false},
		{version: "", want: false},
	}

	for _, test := range tests {
		t.Run(test.version, func(t *testing.T) {
			if got := IsRelease(test.version); got != test.want {
				t.Errorf("IsRelease(%q) = %v, want %v", test.version, got, test.want)
			}
		})
	}
}

func TestLatestVersion(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		body    string
		want    string
		wantErr bool
	}{
		// Trimmed from a real response - the field that matters among dozens.
		{name: "a release", status: http.StatusOK, body: `{"tag_name":"v0.2.0","name":"v0.2.0","draft":false}`, want: "v0.2.0"},

		// What GitHub gives a repo with no releases at all.
		{name: "no releases", status: http.StatusNotFound, body: `{"message":"Not Found"}`, wantErr: true},
		{name: "rate limited", status: http.StatusForbidden, body: `{"message":"API rate limit exceeded"}`, wantErr: true},
		{name: "not json", status: http.StatusOK, body: "<html>", wantErr: true},
		{name: "empty tag", status: http.StatusOK, body: `{"tag_name":""}`, wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(test.status)
				w.Write([]byte(test.body))
			}))
			t.Cleanup(server.Close)

			original := releaseAPIURL
			releaseAPIURL = server.URL
			t.Cleanup(func() { releaseAPIURL = original })

			got, err := LatestVersion()

			if test.wantErr {
				if err == nil {
					t.Errorf("got %q, want an error", got)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != test.want {
				t.Errorf("got %q, want %q", got, test.want)
			}
		})
	}
}

func TestChecksumFor(t *testing.T) {
	sums := []byte(
		"aaaa  trackorganiser-linux-amd64\n" +
			"BBBB *trackorganiser-windows-amd64.exe\r\n" +
			"cccc  trackorganiser-linux-arm64\n" +
			"\n" +
			"not a checksum line at all\n")

	tests := []struct {
		name    string
		asset   string
		want    string
		wantErr bool
	}{
		{name: "text mode, two spaces", asset: "trackorganiser-linux-amd64", want: "aaaa"},

		// sha256sum -b writes " *" before the name. Lowercased because that is
		// how Apply's own hash is written.
		{name: "binary mode asterisk and CRLF", asset: "trackorganiser-windows-amd64.exe", want: "bbbb"},

		// The arm64 line follows the amd64 one - matching on a prefix would
		// return the wrong hash here.
		{name: "exact name, not prefix", asset: "trackorganiser-linux-arm64", want: "cccc"},

		{name: "missing", asset: "trackorganiser-macos-arm64", wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := checksumFor(sums, test.asset)

			if test.wantErr {
				if err == nil {
					t.Errorf("got %q, want an error", got)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != test.want {
				t.Errorf("got %q, want %q", got, test.want)
			}
		})
	}
}

func TestApplyReplacesTheExecutable(t *testing.T) {
	asset := thisPlatformsAsset(t)
	exe := fakeExecutable(t, "old version")

	newBinary := []byte("new version")
	fakeRelease(t, "v9.9.9", map[string][]byte{
		asset:             newBinary,
		checksumsFileName: sumsLine(asset, newBinary),
	})

	if err := Apply("v9.9.9"); err != nil {
		t.Fatalf("Apply failed: %v", err)
	}

	got, err := os.ReadFile(exe)
	if err != nil {
		t.Fatalf("reading the swapped file: %v", err)
	}
	if string(got) != "new version" {
		t.Errorf("executable holds %q, want the new version", got)
	}

	// Without the exec bit the next run fails with "permission denied", which
	// would read as the update breaking the program.
	if runtime.GOOS != "windows" {
		info, err := os.Stat(exe)
		if err != nil {
			t.Fatalf("stat: %v", err)
		}
		if info.Mode().Perm() != 0755 {
			t.Errorf("mode is %v, want 0755 carried over from the old binary", info.Mode().Perm())
		}
	}

	assertGone(t, exe+".new")
	assertGone(t, exe+".old")
}

// Every way the download can go wrong must leave the original exactly as it
// was, because a run that leaves nothing runnable can't be fixed by running it
// again.
func TestApplyFailuresLeaveTheOriginal(t *testing.T) {
	asset := thisPlatformsAsset(t)
	newBinary := []byte("new version")

	tests := []struct {
		name     string
		files    map[string][]byte
		contains string
	}{
		{
			name: "checksum mismatch",
			files: map[string][]byte{
				asset:             newBinary,
				checksumsFileName: sumsLine(asset, []byte("something else entirely")),
			},
			contains: "checksum",
		},
		{
			// v0.1.0 is exactly this: published before release.yml wrote one.
			// No checksum means no install, not an unverified one.
			name:     "release has no checksums file",
			files:    map[string][]byte{asset: newBinary},
			contains: checksumsFileName,
		},
		{
			name:     "checksums file doesn't list this platform",
			files:    map[string][]byte{asset: newBinary, checksumsFileName: sumsLine("some-other-file", newBinary)},
			contains: "no entry",
		},
		{
			name:     "asset missing from the release",
			files:    map[string][]byte{checksumsFileName: sumsLine(asset, newBinary)},
			contains: "404",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			exe := fakeExecutable(t, "old version")
			fakeRelease(t, "v9.9.9", test.files)

			err := Apply("v9.9.9")
			if err == nil {
				t.Fatal("Apply succeeded, want an error")
			}
			if !strings.Contains(err.Error(), test.contains) {
				t.Errorf("error %q doesn't mention %q", err, test.contains)
			}

			got, readErr := os.ReadFile(exe)
			if readErr != nil {
				t.Fatalf("the original is gone: %v", readErr)
			}
			if string(got) != "old version" {
				t.Errorf("original now holds %q, want it untouched", got)
			}

			assertGone(t, exe+".new")
			assertGone(t, exe+".old")
		})
	}
}

// A link in ~/bin pointing at the real binary is a normal way to install a CLI.
// Replacing the link with a file would leave the real binary on the old version
// forever, so the update has to land on the file the link points at.
func TestApplyFollowsASymlink(t *testing.T) {
	asset := thisPlatformsAsset(t)
	target := fakeExecutable(t, "old version")

	link := filepath.Join(t.TempDir(), "trackorganiser-link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("can't create a symlink here: %v", err)
	}
	executablePath = func() (string, error) { return link, nil }

	newBinary := []byte("new version")
	fakeRelease(t, "v9.9.9", map[string][]byte{
		asset:             newBinary,
		checksumsFileName: sumsLine(asset, newBinary),
	})

	if err := Apply("v9.9.9"); err != nil {
		t.Fatalf("Apply failed: %v", err)
	}

	info, err := os.Lstat(link)
	if err != nil {
		t.Fatalf("lstat: %v", err)
	}
	if info.Mode()&fs.ModeSymlink == 0 {
		t.Error("the link was replaced by a file - the real binary will never update again")
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("reading the target: %v", err)
	}
	if string(got) != "new version" {
		t.Errorf("target holds %q, want the new version", got)
	}
}

func TestCleanupOldRemovesALeftover(t *testing.T) {
	exe := fakeExecutable(t, "current version")

	// What Windows leaves behind: the update moved the running binary aside
	// but couldn't delete it until that process exited.
	if err := os.WriteFile(exe+".old", []byte("previous version"), 0755); err != nil {
		t.Fatalf("planting the leftover: %v", err)
	}

	CleanupOld()

	assertGone(t, exe+".old")

	if _, err := os.Stat(exe); err != nil {
		t.Errorf("CleanupOld touched the current binary: %v", err)
	}
}

func TestCleanupOldWithNothingToClean(t *testing.T) {
	exe := fakeExecutable(t, "current version")

	// The usual case. Just must not panic or touch anything.
	CleanupOld()

	if _, err := os.Stat(exe); err != nil {
		t.Errorf("CleanupOld touched the current binary: %v", err)
	}
}
