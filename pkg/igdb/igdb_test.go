package igdb

import "testing"

func TestQuoteString(t *testing.T) {
	cases := map[string]string{
		`zelda`:                   `"zelda"`,
		`"; fields *; where id>0`: `"\"; fields *; where id>0"`,
		`back\slash`:              `"back\\slash"`,
		`\"`:                      `"\\\""`,
	}
	for in, want := range cases {
		if got := QuoteString(in); got != want {
			t.Errorf("QuoteString(%q) = %s, want %s", in, got, want)
		}
	}
}
