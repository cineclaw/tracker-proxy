package tracker

import (
	"testing"
)

func TestExtractStudios(t *testing.T) {
	tests := []struct {
		title       string
		wantStudios []string
	}{
		{
			title:       "Гангстерленд / MobLand (2026) WEB-DL [H.264/1080p] (сезон 2, серии 1-2 из 10) RHS, LostFilm, HDRezka, NewComers, ViruseProject, ColdFilm (обновляемая)",
			wantStudios: []string{"Red Head Sound", "LostFilm", "HDRezka Studio", "NewComers", "ViruseProject", "ColdFilm"},
		},
		{
			title:       "Гангстерленд / MobLand / Сезон: 2 / Серии: 1-2 из 10 (Даниэль Сыркин, Гай Ричи, Энтони Бирн) [2026, WEB-DL 1080p] MVO + Dub (Red Head Sound) + DVO + Original + Sub (Rus, Eng)",
			wantStudios: []string{"Red Head Sound", "Дубляж", "Оригинал + Субтитры"},
		},
		{
			title:       "Гангстерленд / MobLand [02х01-02 из 10] (2026) WEBRip 1080p | P2 | ViruseProject",
			wantStudios: []string{"ViruseProject"},
		},
		{
			title:       "Пингвин / The Penguin / Сезон: 1 / Серии: 1-8 из 8 [2024, WEB-DL 1080p] DVO (Кубик в Кубе) + MVO (HDRezka)",
			wantStudios: []string{"Кубик в Кубе", "HDRezka Studio"},
		},
	}

	for _, tt := range tests {
		got := ExtractStudios(tt.title)
		for _, w := range tt.wantStudios {
			found := false
			for _, g := range got {
				if g == w {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("ExtractStudios(%q) missing expected studio %q, got: %v", tt.title, w, got)
			}
		}
	}
}

func TestMatchesStudio(t *testing.T) {
	studios := []string{"Red Head Sound", "LostFilm", "HDRezka Studio"}

	if !MatchesStudio(studios, "Red Head Sound") {
		t.Errorf("expected match for Red Head Sound")
	}
	if !MatchesStudio(studios, "rhs") {
		t.Errorf("expected match for rhs")
	}
	if !MatchesStudio(studios, "lostfilm") {
		t.Errorf("expected match for lostfilm")
	}
	if MatchesStudio(studios, "Кубик в Кубе") {
		t.Errorf("unexpected match for Кубик в Кубе")
	}
	if !MatchesStudio(studios, "Любая") {
		t.Errorf("expected match for 'Любая'")
	}
	if !MatchesStudio(studios, "") {
		t.Errorf("expected match for empty string")
	}
}

func TestExtractEpisodeMax(t *testing.T) {
	tests := []struct {
		title   string
		wantMax int
	}{
		{
			title:   "Гангстерленд / MobLand (2026) WEB-DL [H.264/1080p] (сезон 2, серии 1-2 из 10) RHS",
			wantMax: 2,
		},
		{
			title:   "Гангстерленд / MobLand [02х01-04 из 10] (2026) WEBRip 1080p",
			wantMax: 4,
		},
		{
			title:   "MobLand S02E03 1080p WEB-DL",
			wantMax: 3,
		},
		{
			title:   "MobLand Сезон 2, Серия 5",
			wantMax: 5,
		},
	}

	for _, tt := range tests {
		got := ExtractEpisodeMax(tt.title)
		if got != tt.wantMax {
			t.Errorf("ExtractEpisodeMax(%q) = %d, want %d", tt.title, got, tt.wantMax)
		}
	}
}
