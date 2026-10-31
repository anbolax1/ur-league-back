package api

import (
	"testing"

	"github.com/ur-league/backend/internal/store"
)

func TestValidRegistrationForm(t *testing.T) {
	kd, bigKd := 2.5, 120.0
	ten, five, neg := 10, 5, -1
	cases := []struct {
		name string
		f    store.RegistrationForm
		bad  bool
	}{
		{"пустая анкета", store.RegistrationForm{}, false},
		{"полная", store.RegistrationForm{GameID: "5123456789", GameNick: "Ник", Kd: &kd, Matches: &ten, Wins: &five, Kills: &ten}, false},
		{"ID с буквами", store.RegistrationForm{GameID: "abc12345"}, true},
		{"K/D слишком большой", store.RegistrationForm{Kd: &bigKd}, true},
		{"побед больше матчей", store.RegistrationForm{Matches: &five, Wins: &ten}, true},
		{"отрицательные убийства", store.RegistrationForm{Kills: &neg}, true},
	}
	for _, c := range cases {
		if msg := validRegistrationForm(c.f); (msg != "") != c.bad {
			t.Errorf("%s: ответ %q", c.name, msg)
		}
	}
}
