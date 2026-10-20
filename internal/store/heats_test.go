package store

import "testing"

func TestMatchRounds(t *testing.T) {
	cases := []struct {
		format string
		rounds int
		want   int
	}{
		{FormatMatch, 0, 2}, // поле не прислали - обычный матч на два раунда
		{FormatMatch, 2, 2},
		{FormatMatch, 3, 3},
		{FormatMatch, 5, 2},
		{FormatShow, 0, 3},
		{FormatShow, 2, 3}, // шоу-матч всегда на три раунда
	}
	for _, c := range cases {
		if got := MatchRounds(c.format, c.rounds); got != c.want {
			t.Errorf("MatchRounds(%q, %d) = %d, want %d", c.format, c.rounds, got, c.want)
		}
	}
}

// Сторона A - меньший MMR, при равном - меньше матчей в прошлых сезонах, при полном равенстве - жребий.
func TestAGoesFirst(t *testing.T) {
	heads := func() bool { return true }
	tails := func() bool { return false }
	cases := []struct {
		name string
		a, b sideInfo
		coin func() bool
		want bool
	}{
		{"меньше MMR ходит первым", sideInfo{mmr: 990, prevGames: 50}, sideInfo{mmr: 1010}, tails, true},
		{"больше MMR уступает", sideInfo{mmr: 1010}, sideInfo{mmr: 990, prevGames: 50}, heads, false},
		{"при равном MMR - кто меньше играл раньше", sideInfo{mmr: 1000, prevGames: 3}, sideInfo{mmr: 1000, prevGames: 12}, tails, true},
		{"опытный при равном MMR уступает", sideInfo{mmr: 1000, prevGames: 12}, sideInfo{mmr: 1000, prevGames: 3}, heads, false},
		{"два новичка - орёл", sideInfo{mmr: 1000}, sideInfo{mmr: 1000}, heads, true},
		{"два новичка - решка", sideInfo{mmr: 1000}, sideInfo{mmr: 1000}, tails, false},
	}
	for _, c := range cases {
		if got := aGoesFirst(c.a, c.b, c.coin); got != c.want {
			t.Errorf("%s: aGoesFirst = %v, want %v", c.name, got, c.want)
		}
	}
}
