package app

import "testing"

func TestNormalizeCoverURL(t *testing.T) {
	tests := map[string]string{
		"//i1.hdslb.com/bfs/archive/cover.jpg":       "https://i1.hdslb.com/bfs/archive/cover.jpg",
		"https://i1.hdslb.com/bfs/archive/cover.jpg": "https://i1.hdslb.com/bfs/archive/cover.jpg",
		"http://i1.hdslb.com/bfs/archive/cover.jpg":  "http://i1.hdslb.com/bfs/archive/cover.jpg",
		"": "",
	}
	for input, want := range tests {
		if got := normalizeCoverURL(input); got != want {
			t.Errorf("normalizeCoverURL(%q) = %q, want %q", input, got, want)
		}
	}
}
