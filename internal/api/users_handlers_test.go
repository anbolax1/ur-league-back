package api

import "testing"

func TestMaskGameID(t *testing.T) {
	cases := map[string]string{
		"":           "",
		"1234":       "••••",
		"512345":     "51••45",
		"5123456789": "51••••••89",
	}
	for in, want := range cases {
		if got := maskGameID(in); got != want {
			t.Errorf("ID %q скрыт как %q, нужно %q", in, got, want)
		}
	}
}
