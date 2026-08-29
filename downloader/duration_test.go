package downloader

import "testing"

func TestDurationsMatch(t *testing.T) {
	tests := []struct {
		name       string
		expectedMS int
		actualMS   int
		want       bool
	}{
		{name: "exact match", expectedMS: 151373, actualMS: 151373, want: true},
		{name: "a few ms out", expectedMS: 151373, actualMS: 151370, want: true},
		{name: "well inside tolerance", expectedMS: 151373, actualMS: 154000, want: true},
		{name: "exactly at tolerance", expectedMS: 100000, actualMS: 105000, want: true},
		{name: "exactly at tolerance, shorter", expectedMS: 100000, actualMS: 95000, want: true},
		{name: "just outside tolerance", expectedMS: 100000, actualMS: 105001, want: false},

		// The real case that prompted this: a music video with an intro.
		{name: "music video with intro", expectedMS: 151373, actualMS: 216630, want: false},
		{name: "radio edit against album version", expectedMS: 240000, actualMS: 180000, want: false},
		{name: "ten hour loop", expectedMS: 180000, actualMS: 36000000, want: false},

		// Spotify not supplying a duration is not a mismatch - flagging every
		// track on the run would make the summary useless.
		{name: "no expected duration", expectedMS: 0, actualMS: 151373, want: true},
		{name: "negative expected duration", expectedMS: -1, actualMS: 151373, want: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := durationsMatch(test.expectedMS, test.actualMS)
			if got != test.want {
				t.Errorf("durationsMatch(%d, %d) = %v, want %v",
					test.expectedMS, test.actualMS, got, test.want)
			}
		})
	}
}
