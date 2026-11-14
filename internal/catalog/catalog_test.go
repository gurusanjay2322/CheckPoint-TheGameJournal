package catalog

import "testing"

func TestTitlesMatch(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"The Witcher 3: Wild Hunt", "The Witcher® 3: Wild Hunt", true},
		{"Counter-Strike 2", "Counter-Strike 2", true},
		{"Portal 2", "Portal", true},
		{"Counter-Strike 2", "Just Chatting", false},
		{"", "Anything", false},
		{"™", "Anything", false},
	}
	for _, c := range cases {
		if got := TitlesMatch(c.a, c.b); got != c.want {
			t.Errorf("TitlesMatch(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}
