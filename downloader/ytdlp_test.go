package downloader

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// ytdlpAssetName takes goos and arch as parameters rather than reading runtime
// directly, which is what lets every platform be asserted from one machine.
func TestYtdlpAssetName(t *testing.T) {
	tests := []struct {
		name    string
		goos    string
		arch    string
		want    string
		wantErr bool
	}{
		{name: "linux amd64", goos: "linux", arch: "amd64", want: "yt-dlp_linux"},
		{name: "linux arm64", goos: "linux", arch: "arm64", want: "yt-dlp_linux_aarch64"},

		// macOS ships a universal binary, so the arch is irrelevant there.
		{name: "darwin amd64", goos: "darwin", arch: "amd64", want: "yt-dlp_macos"},
		{name: "darwin arm64", goos: "darwin", arch: "arm64", want: "yt-dlp_macos"},

		// Windows on ARM runs the x64 exe under emulation.
		{name: "windows amd64", goos: "windows", arch: "amd64", want: "yt-dlp.exe"},
		{name: "windows arm64", goos: "windows", arch: "arm64", want: "yt-dlp.exe"},

		// Never guess an asset name - a wrong one 404s, and a 404 page written to
		// disk and chmod'd is a "binary" that fails in baffling ways later.
		{name: "unsupported linux arch", goos: "linux", arch: "riscv64", wantErr: true},
		{name: "unsupported os", goos: "freebsd", arch: "amd64", wantErr: true},
		{name: "empty platform", goos: "", arch: "", wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := ytdlpAssetName(test.goos, test.arch)

			if test.wantErr {
				if err == nil {
					t.Fatalf("ytdlpAssetName(%q, %q) = %q, want an error", test.goos, test.arch, got)
				}
				if got != "" {
					t.Errorf("ytdlpAssetName(%q, %q) returned %q alongside an error, want empty",
						test.goos, test.arch, got)
				}
				return
			}

			if err != nil {
				t.Fatalf("ytdlpAssetName(%q, %q) returned unexpected error: %v", test.goos, test.arch, err)
			}
			if got != test.want {
				t.Errorf("ytdlpAssetName(%q, %q) = %q, want %q", test.goos, test.arch, got, test.want)
			}
		})
	}
}

// fakeRuntimes creates empty executables named after JS runtimes on a PATH of
// their own, so FindJSRuntime can be tested without any of them installed.
func fakeRuntimes(t *testing.T, names ...string) {
	t.Helper()

	dir := t.TempDir()
	for _, name := range names {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0755); err != nil {
			t.Fatalf("setting up %q: %v", name, err)
		}
	}

	t.Setenv("PATH", dir)
}

func TestFindJSRuntime(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("PATH lookup expects unix executable bits")
	}

	tests := []struct {
		name      string
		installed []string
		want      string
	}{
		// yt-dlp's own documented priority is deno, node, quickjs, bun - the
		// pick should match what it would have chosen itself.
		{name: "deno wins over node", installed: []string{"deno", "node"}, want: "deno"},
		{name: "node wins over quickjs", installed: []string{"node", "quickjs"}, want: "node"},
		{name: "quickjs wins over bun", installed: []string{"quickjs", "bun"}, want: "quickjs"},
		{name: "all four installed", installed: []string{"bun", "quickjs", "node", "deno"}, want: "deno"},

		{name: "only node", installed: []string{"node"}, want: "node"},
		{name: "only bun", installed: []string{"bun"}, want: "bun"},

		// Not an error: yt-dlp still runs, just with fewer formats.
		{name: "none installed", installed: nil, want: ""},
		{name: "something unrelated", installed: []string{"python3", "curl"}, want: ""},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fakeRuntimes(t, test.installed...)

			if got := FindJSRuntime(); got != test.want {
				t.Errorf("FindJSRuntime() = %q, want %q", got, test.want)
			}
		})
	}
}
