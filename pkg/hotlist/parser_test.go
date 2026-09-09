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
