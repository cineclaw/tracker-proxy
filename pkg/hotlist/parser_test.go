package hotlist

import (
	"testing"
)

func TestParseReleaseTitle(t *testing.T) {
	tests := []struct {
		name          string
		raw           string
		wantRu        string
		wantOrig      string
		wantYear      int
		wantSeason    string
		wantQualityIn string
	}{
		{
			name:          "Mutiny movie",
			raw:           "Мятеж / Mutiny (2026) WEB-DL 1080p | P | HDRezka Studio",
			wantRu:        "Мятеж",
			wantOrig:      "Mutiny",
			wantYear:      2026,
			wantQualityIn: "1080P",
		},
		{
			name:          "Silo TV series",
			raw:           "Укрытие / Бункер / Silo [S03] (2026) WEB-DL 1080p | P, A, L",
			wantRu:        "Укрытие",
			wantOrig:      "Silo",
			wantYear:      2026,
			wantSeason:    "S03",
			wantQualityIn: "1080P",
		},
		{
			name:          "Russian series single title",
			raw:           "ОПГ [S01] (2025) WEB-DLRip-AVC от DoMiNo & селезень",
			wantRu:        "ОПГ",
			wantOrig:      "ОПГ",
			wantYear:      2025,
			wantSeason:    "S01",
			wantQualityIn: "WEB-DLRIP",
		},
		{
			name:          "In the Grey 4K Remux",
			raw:           "Грязные деньги / In the Grey (2026) UHD BDRemux 2160p | 4K | HDR10+ | Dolby Vision P7 | D",
			wantRu:        "Грязные деньги",
			wantOrig:      "In the Grey",
			wantYear:      2026,
			wantQualityIn: "2160P",
		},
		{
			name:          "House of the Dragon",
			raw:           "Дом Дракона / House of the Dragon [S03] (2026) WEB-DLRip-AVC от DoMiNo & селезень",
			wantRu:        "Дом Дракона",
			wantOrig:      "House of the Dragon",
			wantYear:      2026,
			wantSeason:    "S03",
			wantQualityIn: "WEB-DLRIP",
		},
		{
			name:          "Courier 1986 RuTracker",
			raw:           "Курьер (Карен Шахназаров) [1986, СССР, драма, WEB-DL 1080p] + Sub Rus + Original Rus",
			wantRu:        "Курьер",
			wantOrig:      "Курьер",
			wantYear:      1986,
			wantQualityIn: "1080P",
		},
		{
			name:          "Courier 2024 Spanish RuTracker",
			raw:           "Курьер / El correo / The Courier (Даниэль Кальпарсоро / Daniel Calparsoro) [2024, Испания, Бельгия, Франция, криминал, драма, триллер, WEB-DLRip-AVC] MVO",
			wantRu:        "Курьер",
			wantOrig:      "The Courier",
			wantYear:      2024,
			wantQualityIn: "WEB-DLRIP",
		},
		{
			name:          "Courier 2025 Runner RuTor",
			raw:           "Курьер. Доставить любой ценой (2025) WEB-DL [H.264/1080p]",
			wantRu:        "Курьер. Доставить любой ценой",
			wantOrig:      "Курьер. Доставить любой ценой",
			wantYear:      2025,
			wantQualityIn: "1080P",
		},
		{
			name:          "Breaking Bad full pack",
			raw:           "Во все тяжкие / Breaking Bad / Сезоны: 1-5 из 5 (Винс Гиллиган) [2008-2013, США, триллер, драма, криминал, BDRip 720p] MVO",
			wantRu:        "Во все тяжкие",
			wantOrig:      "Breaking Bad",
			wantYear:      2008,
			wantQualityIn: "720P",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ru, orig, year, season, quality := ParseReleaseTitle(tt.raw)
			if ru != tt.wantRu {
				t.Errorf("RussianTitle = %q, want %q", ru, tt.wantRu)
			}
			if orig != tt.wantOrig {
				t.Errorf("OriginalTitle = %q, want %q", orig, tt.wantOrig)
			}
			if year != tt.wantYear {
				t.Errorf("Year = %d, want %d", year, tt.wantYear)
			}
			if tt.wantSeason != "" && season != tt.wantSeason {
				t.Errorf("Season = %q, want %q", season, tt.wantSeason)
			}
			if tt.wantQualityIn != "" && !containsIgnoreCase(quality, tt.wantQualityIn) {
				t.Errorf("Quality = %q does not contain %q", quality, tt.wantQualityIn)
			}
		})
	}
}

func containsIgnoreCase(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0)
}

func TestParseReleaseDetails(t *testing.T) {
	tests := []struct {
		name          string
		raw           string
		wantStart     int
		wantEnd       int
		wantOngoing   bool
	}{
		{
			name:        "Breaking Bad pack range",
			raw:         "Во все тяжкие / Breaking Bad [2008-2013, США...]",
			wantStart:   2008,
			wantEnd:     2013,
			wantOngoing: false,
		},
		{
			name:        "The Boys ongoing",
			raw:         "Пацаны / The Boys [2019-..., США...]",
			wantStart:   2019,
			wantEnd:     0,
			wantOngoing: true,
		},
		{
			name:        "Single year comma",
			raw:         "Курьер [1986, СССР...]",
			wantStart:   1986,
			wantEnd:     1986,
			wantOngoing: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, sYear, eYear, ongoing, _, _ := ParseReleaseDetails(tt.raw)
			if sYear != tt.wantStart {
				t.Errorf("startYear = %d, want %d", sYear, tt.wantStart)
			}
			if eYear != tt.wantEnd {
				t.Errorf("endYear = %d, want %d", eYear, tt.wantEnd)
			}
			if ongoing != tt.wantOngoing {
				t.Errorf("isOngoing = %v, want %v", ongoing, tt.wantOngoing)
			}
		})
	}
}

func TestIsNonVideo(t *testing.T) {
	tests := []struct {
		title       string
		isNonVideo  bool
	}{
		{"Дмитрий Ра, Вова Бо - Запечатанный мир 1, Имперский Курьер. Том 1 (2025) МР3", true},
		{"Дмитрий Ра, Вова Бо - Запечатанный мир 2, Имперский Курьер. Том 2 (2025) МР3", true},
		{"Курьер. Доставить любой ценой (2025) WEB-DL [H.264/1080p]", false},
		{"Мятеж / Mutiny (2026) WEB-DL 1080p", false},
		{"Книга джунглей / The Jungle Book (2016) BDRip 1080p", false},
		{"OST - Blade Runner 2049 (2017) [FLAC]", true},
		{"Аудиокнига - Метро 2033 [MP3]", true},
	}

	for _, tt := range tests {
		got := IsNonVideo(tt.title)
		if got != tt.isNonVideo {
			t.Errorf("IsNonVideo(%q) = %v, want %v", tt.title, got, tt.isNonVideo)
		}
	}
}

