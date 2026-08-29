package downloader

import "testing"

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
