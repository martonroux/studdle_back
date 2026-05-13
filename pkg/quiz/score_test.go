package quiz

import "testing"

func TestNormalizeFillBlank_StripsCaseSpaceAndPunctuation(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"Mitochondria.", "mitochondria"},
		{"  the  Cell ", "the  cell"}, // intentional inner-double-space preserved
		{"Reagan,", "reagan"},
	}
	for _, c := range cases {
		if got := normalizeFillBlank(c.in); got != c.want {
			t.Fatalf("normalize(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
