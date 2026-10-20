package models

import "time"

// SeasonRecap - итоги сезона для страницы /season/{номер}: всё посчитано на сервере, страница только рисует.
// Игроки и матчи ссылаются друг на друга индексами в Players и Matches.
type SeasonRecap struct {
	Season    Season          `json:"season"`
	Live      bool            `json:"live"` // сезон ещё идёт: на странице лидер, а не чемпион
	Summary   RecapSummary    `json:"summary"`
	Players   []RecapPlayer   `json:"players"`  // по местам в таблице
	Matches   []RecapMatch    `json:"matches"`  // по времени
	Days      []RecapDay      `json:"days"`     // каждый день от первого матча до последнего
	Leaders   []RecapLeader   `json:"leaders"`  // кто был первым на конец дня, отрезками
	Decisive  int             `json:"decisive"` // матч, после которого первым стал итоговый лидер; -1 - лидер не менялся
	Maps      []RecapMap      `json:"maps"`
	Knockers  []RecapKnocker  `json:"knockers"`
	BestKnock *RecapBestKnock `json:"bestKnock,omitempty"` // лучший заход сезона по киллам
	Places    [16]int         `json:"places"`              // заходов на каждом месте, с первого
	Placers   []RecapPlacer   `json:"placers"`             // игроки с записанными местами
	Comebacks []RecapComeback `json:"comebacks"`
	Tasks     RecapTasks      `json:"tasks"`
	Favorites [2]int          `json:"favorites"` // побед фаворита по MMR и матчей, где MMR сторон различался
	Upsets    []RecapUpset    `json:"upsets"`
	Swings    []int           `json:"swings"` // матчи с самым большим изменением MMR
	Rivals    []RecapRival    `json:"rivals"`
	Weekday   [7]int          `json:"weekday"` // матчей по дням недели, с понедельника
	Hours     [24]int         `json:"hours"`   // час начала по Москве - только у матчей с точным временем
	Upcoming  []RecapUpcoming `json:"upcoming"`
}

type RecapSummary struct {
	Matches  int        `json:"matches"`
	Games    int        `json:"games"` // с учётом ×2 из таблицы организатора, как победы и поражения на сайте
	Players  int        `json:"players"`
	OneMatch int        `json:"oneMatch"`
	X2       int        `json:"x2"`
	Detailed int        `json:"detailed"` // матчей с раундами и заданиями
	Knocks   int        `json:"knocks"`
	Heats    int        `json:"heats"` // сыгранных заходов
	Chickens int        `json:"chickens"`
	GameDays int        `json:"gameDays"`
	First    *time.Time `json:"first,omitempty"`
	Last     *time.Time `json:"last,omitempty"`
	StartMmr int        `json:"startMmr"`
}

type RecapPlayer struct {
	Login      string     `json:"login"`
	Mmr        int        `json:"mmr"`
	Rank       int        `json:"rank"`
	Peak       int        `json:"peak"`
	PeakAt     time.Time  `json:"peakAt"`
	Low        int        `json:"low"`
	Wins       int        `json:"wins"`
	Losses     int        `json:"losses"`
	Matches    int        `json:"matches"`
	WinStreak  int        `json:"winStreak"`
	LossStreak int        `json:"lossStreak"`
	Opponents  int        `json:"opponents"`
	First      time.Time  `json:"first"`
	Tags       []string   `json:"tags"`
	Curve      [][3]int64 `json:"curve"` // [время в мс, MMR после, 1 - сверка рейтинга]
}

type RecapMatch struct {
	ID     string       `json:"id"`
	At     time.Time    `json:"at"`
	P      [2]int       `json:"p"`      // стороны A и B
	Winner int          `json:"winner"` // 0 | 1, -1 - ничья
	Mult   int          `json:"mult"`
	Games  int          `json:"games"`
	Show   bool         `json:"show"`
	Before [2]int       `json:"before"`
	Delta  [2]int       `json:"delta"`
	Score  *[2]int      `json:"score,omitempty"` // у матчей из таблицы организатора счёта нет
	Rounds []RecapRound `json:"rounds"`
}

// RecapRound - раунд матча: у каждой стороны свой заход со своей картой и местом.
type RecapRound struct {
	Maps   [2]string `json:"maps"`   // коды карт заходов A и B, пусто - не отмечена
	Places [2]int    `json:"places"` // места в заходах, 0 - не записано
	Played bool      `json:"played"`
	Points [2]int    `json:"points"`
	Knocks [2]int    `json:"knocks"`
}

type RecapDay struct {
	Date    string `json:"date"` // ГГГГ-ММ-ДД по Москве
	Matches int    `json:"matches"`
	New     int    `json:"new"`   // игроков, сыгравших первый матч
	Order   []int  `json:"order"` // места на конец дня, первый - лидер
}

type RecapLeader struct {
	Player int `json:"player"`
	From   int `json:"from"` // индексы дней в Days, включительно
	To     int `json:"to"`
}

// RecapMap - заходы на карте за сезон: очки, киллы и задания стороны в её заходе, места по экрану итогов.
type RecapMap struct {
	Code         string  `json:"code"`
	Name         string  `json:"name"`
	Heats        int     `json:"heats"`
	Points       int     `json:"points"`
	Knocks       int     `json:"knocks"`
	TasksDone    int     `json:"tasksDone"`
	TasksOffered int     `json:"tasksOffered"`
	Placed       int     `json:"placed"`   // заходов с записанным местом
	AvgPlace     float64 `json:"avgPlace"` // 0 - мест не записано
	Chickens     int     `json:"chickens"`
}

type RecapKnocker struct {
	Player int `json:"player"`
	Knocks int `json:"knocks"`
	Heats  int `json:"heats"`
}

type RecapBestKnock struct {
	Match  int    `json:"match"`
	Player int    `json:"player"`
	Knocks int    `json:"knocks"`
	Round  int    `json:"round"`
	Map    string `json:"map"` // код карты захода
}

// RecapPlacer - места игрока в заходах сезона: сколько записано, среднее, чикены и топ-5.
type RecapPlacer struct {
	Player   int     `json:"player"`
	Placed   int     `json:"placed"`
	AvgPlace float64 `json:"avgPlace"`
	Chickens int     `json:"chickens"`
	Top5     int     `json:"top5"`
}

// RecapComeback - победа после проигранного первого раунда.
type RecapComeback struct {
	Match  int    `json:"match"`
	Round1 [2]int `json:"round1"` // очки победителя и проигравшего в первом раунде
}

type RecapTasks struct {
	All       [2]int          `json:"all"` // выполнено и выдано
	MapTasks  [2]int          `json:"mapTasks"`
	General   [2]int          `json:"general"`
	Protocols [2]int          `json:"protocols"`
	Distinct  int             `json:"distinct"`
	Easy      []RecapTask     `json:"easy"`
	Hard      []RecapTask     `json:"hard"`
	Doers     []RecapTaskDoer `json:"doers"`
}

type RecapTask struct {
	Name     string `json:"name"`
	Text     string `json:"text"`
	Category string `json:"category"` // task | protocol
	Map      string `json:"map"`
	Done     int    `json:"done"`
	Offered  int    `json:"offered"`
}

type RecapTaskDoer struct {
	Player  int `json:"player"`
	Done    int `json:"done"`
	Offered int `json:"offered"`
}

// RecapUpset - победа вопреки рейтингу: Chance - шанс победителя по Эло перед матчем.
type RecapUpset struct {
	Match  int     `json:"match"`
	Chance float64 `json:"chance"`
}

type RecapRival struct {
	A     int `json:"a"`
	B     int `json:"b"`
	WinsA int `json:"winsA"`
	WinsB int `json:"winsB"`
}

type RecapUpcoming struct {
	ID     string     `json:"id"`
	Title  string     `json:"title"`
	At     *time.Time `json:"at,omitempty"`
	Format string     `json:"format"`
	Prize  string     `json:"prize"`
}
