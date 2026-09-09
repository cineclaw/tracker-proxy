package tracker

import (
	"reflect"
	"testing"
)

func TestExtractSeasonInfo(t *testing.T) {
	tests := []struct {
		title        string
		wantSeasons  []int
		wantComplete bool
	}{
		{
			title:       "Секретные материалы / The X-Files / Сезоны: 1-11 из 11 / Серии: 1-218 из 218",
			wantSeasons: []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11},
		},
		{
			title:       "Секретные материалы / The X-Files / Сезон 1-11 / Серии 1-217",
			wantSeasons: []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11},
		},
		{
			title:       "Секретные Материалы / The X-Files / Сезон: 7 / Серии: 1-22 (22)",
			wantSeasons: []int{7},
		},
		{
			title:       "Секретные материалы / The X-Files (1993-2008) DVDRip [H.264] (сезон 1-9 + Борьба за будущее)",
			wantSeasons: []int{1, 2, 3, 4, 5, 6, 7, 8, 9},
		},
		{
			title:       "Секретные материалы / The X-Files [S01-11] (1993-2018) BDRip",
			wantSeasons: []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11},
		},
		{
			title:       "Во все тяжкие / Breaking Bad / Сезоны: 1-5 / Серии: 1-62 из 62",
			wantSeasons: []int{1, 2, 3, 4, 5},
		},
		{
			title:       "Во все тяжкие / Breaking Bad [S01-05] (2008-2013) BDRip от qqss44",
			wantSeasons: []int{1, 2, 3, 4, 5},
		},
		{
			title:       "Во все тяжкие / Breaking Bad / Сезон: 2 / Серии 1-13 (13)",
			wantSeasons: []int{2},
		},
		{
			title:       "Доктор Хаус / House M.D. / Сезон: 1-8 (8) / Серии: 1-177 (177)",
			wantSeasons: []int{1, 2, 3, 4, 5, 6, 7, 8},
		},
		{
			title:       "Рик и Морти / Rick and Morty / Сезон: 1, 2 / Серии: 1-21 из 21",
			wantSeasons: []int{1, 2},
		},
		{
			title:       "Рик и Морти / Rick and Morty [S09] (2026) WEB-DL 1080p",
			wantSeasons: []int{9},
		},
		{
			title:       "Друзья / Friends (1994-2004) BDRip (сезон 1-10, серии 1-235 из 235)",
			wantSeasons: []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10},
		},
		{
			title:        "Во все тяжкие / Breaking Bad / Полный сериал [2008-2013]",
			wantComplete: true,
		},
		{
			title:       "Титаник / Titanic (Джеймс Кэмерон / James Cameron) [1997, США, драма]",
			wantSeasons: nil,
		},
		{
			title:       "El Camino: Во все тяжкие / El Camino: A Breaking Bad Movie [2019]",
			wantSeasons: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.title, func(t *testing.T) {
			seasons, complete := ExtractSeasonInfo(tc.title)
			if !reflect.DeepEqual(seasons, tc.wantSeasons) {
				t.Errorf("ExtractSeasonInfo(%q) seasons = %v, want %v", tc.title, seasons, tc.wantSeasons)
			}
			if complete != tc.wantComplete {
				t.Errorf("ExtractSeasonInfo(%q) complete = %v, want %v", tc.title, complete, tc.wantComplete)
			}
		})
	}
}
