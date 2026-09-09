package tracker

import (
	"testing"
)

func TestExtractResolution(t *testing.T) {
	tests := []struct {
		title string
		want  string
	}{
		{"Титаник / Titanic [UHD BDRemux 2160p, HDR10, Dolby Vision]", "4k"},
		{"Титаник / Titanic [4К AI UPSCALE, HEVC, 2160p, SDR]", "4k"},
		{"Титаник / Titanic BDRip [AV1/2160p] [4K, SDR, 10-bit]", "4k"},
		{"Бегущая / The Runner (2026) UHD WEB-DL 2160p | 4K", "4k"},
		{"Мэйдэй / Mayday (2026) WEB-DL [H.265/2160p] [4K, HDR10+, 10-bit]", "4k"},
		{"Холоп 3 (2026) WEBRip 2160p от ELEKTRI4KA | 4K | SDR", "4k"},
		{"Аватар / Avatar [UHD BDRemux] [HDR]", "4k"},
		{"Титаник / Titanic WEB-DL 1080p [Локализованный видеоряд]", "1080p"},
		{"Титаник / Titanic (1997) WEB-DL [Н.264/1080p]", "1080p"},
		{"Титаник / Titanic [1997, драма, 35mm Film Scan 1080p]", "1080p"},
		{"Во все тяжкие / Breaking Bad / Сезон: 1 / BDRip 1080p", "1080p"},
		{"Славные парни / Goodfellas [BDRip 1080p] [4K Remaster]", "1080p"}, // 4K Remaster on 1080p!
		{"Дрожь Земли / Tremors [BDRip 720p] [Arrow 4K Remaster]", "lq"},     // 4K Remaster on 720p!
		{"Во все тяжкие [BDRip 720p] AVO Goblin", "lq"},
		{"Титаник / Titanic [1997, BDRip-AVC] Dub", "lq"},
		{"Титаник / Titanic [1997, WEB-DLRip] Dub", "lq"},
		{"10 ошибок, которые потопили Титаник [SATRip-AVC]", "lq"},
		{"Секретные материалы (1993-2008) DVDRip [H.264]", "lq"},
		{"Во все тяжкие [SERIAL] [HDRip] [MP4, 1280x]", "lq"},
	}

	for _, tc := range tests {
		got := ExtractResolution(tc.title)
		if got != tc.want {
			t.Errorf("ExtractResolution(%q) = %q, want %q", tc.title, got, tc.want)
		}
	}
}
