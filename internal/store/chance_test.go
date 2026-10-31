package store

import (
	"math"
	"testing"
	"time"

	"github.com/ur-league/backend/internal/models"
)

func TestWinTieLead(t *testing.T) {
	// A: 0 или 3 поровну, B: всегда 1. При равном старте A выигрывает в половине случаев.
	a := []float64{0.5, 0, 0, 0.5}
	b := []float64{0, 1}
	if w, tie := winTie(a, b, 0); math.Abs(w-0.5) > 1e-12 || tie != 0 {
		t.Fatalf("без форы: %v %v", w, tie)
	}
	// Фора 1: при 0 у A счёт 1:1 - ничья.
	if w, tie := winTie(a, b, 1); math.Abs(w-0.5) > 1e-12 || math.Abs(tie-0.5) > 1e-12 {
		t.Fatalf("фора 1: %v %v", w, tie)
	}
	// A отстаёт на 5: не догонит.
	if w, tie := winTie(a, b, -5); w != 0 || tie != 0 {
		t.Fatalf("отставание 5: %v %v", w, tie)
	}
}

func TestRaidPMFKnocksSoFar(t *testing.T) {
	p := raidParams{Knocks: 1.5, Adj: [3]float64{1, 0, 0}}
	pmf := raidPMF(nil, p.Knocks, 2, p.Adj)
	for i := 0; i < 6; i++ {
		if pmf[i] != 0 {
			t.Fatalf("два килла уже есть: очков меньше 6 быть не может, у %d вероятность %v", i, pmf[i])
		}
	}
	sum := 0.0
	for _, x := range pmf {
		sum += x
	}
	if math.Abs(sum-1) > 1e-9 {
		t.Fatalf("сумма %v", sum)
	}
}

// Все заходы сыграны, счёт равный: ничья наверняка, а шанс стороны - её шанс выиграть дуэль до первого килла.
func TestChanceTiedGoesToDuel(t *testing.T) {
	st := matchSetup{RatingP: 0.5, Duel: 0.7}
	c := splitChance(sideRaids{base: 9}, sideRaids{base: 9}, st, 1)
	if c.Draw != 1 || c.A <= 0.5 || math.Abs(c.A+c.B-1) > 1e-12 {
		t.Fatalf("дуэль при равном счёте: %+v", c)
	}
	// У кого киллов больше, тот и дуэль выигрывает чаще.
	m := newChanceModel()
	tally(m.knocks, "hunter", 30, 10)
	if s := m.setup([2]string{"hunter", "calm"}, nil); s.Duel <= 0.5 {
		t.Fatalf("дуэль у того, кто чаще убивает: %v", s.Duel)
	}
}

// Дуэль в пульте: заходы сыграны все, хоть сторона B ещё в кадре, ничья наверняка, шанс - кто выиграет дуэль.
func TestLiveChanceDuel(t *testing.T) {
	parts := [2]models.Participant{{ID: "pa", Seed: 1}, {ID: "pb", Seed: 2}}
	setup := matchSetup{RatingP: 0.5, Duel: 0.3, Rounds: []roundSetup{{Kit: kitOwn}, {Kit: kitOwn}}}
	st := models.MatchState{Stage: "round", CurrentRound: 2, Scores: []models.RoundScore{
		{RoundNumber: 1, ParticipantID: "pa", Points: 4}, {RoundNumber: 1, ParticipantID: "pb", Points: 1},
		{RoundNumber: 2, ParticipantID: "pa", Points: 3}, {RoundNumber: 2, ParticipantID: "pb", Points: 6},
	}}
	st.Log = []models.MatchLogEntry{{Kind: DuelLogKind}}
	c := liveChance(st, parts, setup, "pb", nil)
	if c.Draw != 1 || c.A >= 0.5 || math.Abs(c.A+c.B-1) > 1e-12 {
		t.Fatalf("дуэль: %+v", c)
	}
}

// Игрок, который в прошлых матчах убивал больше, перед следующим матчем получает шанс выше половины;
// карта захода учитывается: на карте, где в среднем убивают больше, ожидание киллов у обоих выше.
func TestChanceLearnsFromHistory(t *testing.T) {
	raid := func(m string, kills int) chanceRaidRec {
		return chanceRaidRec{Map: m, Knocks: kills, Points: 3 * kills, Tasks: []chanceTaskRec{{Kind: taskProtocol}, {Kind: taskGeneral}, {Kind: taskOnMap}}}
	}
	var recs []chanceMatchRec
	for i := 0; i < 12; i++ {
		recs = append(recs, chanceMatchRec{
			ID: "h" + string(rune('a'+i)), Users: [2]string{"hunter", "calm"}, Winner: 0, Games: 1, Detailed: true,
			Rounds: []chanceRoundRec{
				{Kit: kitOwn, Played: true, Raids: [2]chanceRaidRec{raid("erangel", 6), raid("erangel", 1)}},
				{Kit: kitOwn, Played: true, Raids: [2]chanceRaidRec{raid("miramar", 5), raid("miramar", 1)}},
			},
		})
	}
	_, model := chanceHistory(recs)
	rounds := []chanceRoundRec{
		{Kit: kitOwn, Played: true, Raids: [2]chanceRaidRec{{Map: "erangel"}, {Map: "erangel"}}},
		{Kit: kitOwn, Played: true, Raids: [2]chanceRaidRec{{Map: "miramar"}, {Map: "miramar"}}},
	}
	pre := model.setup([2]string{"hunter", "calm"}, rounds).prematch()
	if pre.A <= 0.7 || math.Abs(pre.A+pre.B-1) > 1e-9 {
		t.Fatalf("сильный игрок перед матчем: %+v", pre)
	}
	rev := model.setup([2]string{"calm", "hunter"}, rounds).prematch()
	if math.Abs(pre.A-rev.B) > 1e-3 {
		t.Fatalf("порядок сторон не должен менять шанс игрока: %+v и %+v", pre, rev)
	}
	// Новички без истории: шансы пополам.
	even := newChanceModel().setup([2]string{"x", "y"}, rounds).prematch()
	if math.Abs(even.A-0.5) > 1e-6 {
		t.Fatalf("новички: %+v", even)
	}
}

// Пульт сам начинает раунд заходом A, ведущий сразу переключает на B: первым шёл B, как и на графике.
func TestFirstRaiderSkipsAutoStart(t *testing.T) {
	t0 := time.Date(2026, 10, 24, 14, 0, 0, 0, time.UTC)
	at := func(m int) time.Time { return t0.Add(time.Duration(m) * time.Minute) }
	auto := []raidMark{
		{at: at(0), side: 0, start: true}, {at: at(1), side: 1, start: true}, {at: at(5), side: 1},
		{at: at(25), side: 0, start: true}, {at: at(30), side: 0}, {at: at(50), side: 1, start: true},
	}
	if first, ok := firstRaider(auto); !ok || first != 1 {
		t.Fatalf("переключение на B через минуту заход A не начинает: %v %v", first, ok)
	}
	// Заход A без очков, но длиннее случайного переключения - настоящий пустой заход.
	empty := []raidMark{{at: at(0), side: 0, start: true}, {at: at(6), side: 1, start: true}, {at: at(9), side: 1}}
	if first, ok := firstRaider(empty); !ok || first != 0 {
		t.Fatalf("пустой заход A первым: %v %v", first, ok)
	}
	// B пошёл в заход без отметки, по действиям в журнале; его заход закрыла отметка начала захода A.
	silent := []raidMark{{at: at(0), side: 0, start: true}, {at: at(1), side: 1}, {at: at(25), side: 0, start: true}}
	if first, ok := firstRaider(silent); !ok || first != 1 {
		t.Fatalf("заход B по действиям: %v %v", first, ok)
	}
}
