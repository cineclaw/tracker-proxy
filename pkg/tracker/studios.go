package tracker

import (
	"regexp"
	"strings"
)

// StudioDefinition maps search patterns to a canonical studio name
type StudioDefinition struct {
	Canonical string
	Patterns  []*regexp.Regexp
}

var knownStudios = []StudioDefinition{
	{
		Canonical: "Red Head Sound",
		Patterns: []*regexp.Regexp{
			regexp.MustCompile(`(?i)(?:^|[^\p{L}\p{N}])(?:red\s*head\s*sound|rhs|ред\s*хед\s*саунд)(?:$|[^\p{L}\p{N}])`),
		},
	},
	{
		Canonical: "Кубик в Кубе",
		Patterns: []*regexp.Regexp{
			regexp.MustCompile(`(?i)(?:^|[^\p{L}\p{N}])(?:кубик\s*в\s*кубе|квк|kubik\s*(?:v\s*kube)?)(?:$|[^\p{L}\p{N}])`),
		},
	},
	{
		Canonical: "LostFilm",
		Patterns: []*regexp.Regexp{
			regexp.MustCompile(`(?i)(?:^|[^\p{L}\p{N}])(?:lostfilm|лостфильм|lost\s*film)(?:$|[^\p{L}\p{N}])`),
		},
	},
	{
		Canonical: "HDRezka Studio",
		Patterns: []*regexp.Regexp{
			regexp.MustCompile(`(?i)(?:^|[^\p{L}\p{N}])(?:hdrezka(?:\s*studio)?(?:\s*18\+)?|резка)(?:$|[^\p{L}\p{N}])`),
		},
	},
	{
		Canonical: "TVShows",
		Patterns: []*regexp.Regexp{
			regexp.MustCompile(`(?i)(?:^|[^\p{L}\p{N}])(?:tvshows|твшоус|tv\s*shows)(?:$|[^\p{L}\p{N}])`),
		},
	},
	{
		Canonical: "NewStudio",
		Patterns: []*regexp.Regexp{
			regexp.MustCompile(`(?i)(?:^|[^\p{L}\p{N}])(?:newstudio|ньюстудио|new\s*studio)(?:$|[^\p{L}\p{N}])`),
		},
	},
	{
		Canonical: "NewComers",
		Patterns: []*regexp.Regexp{
			regexp.MustCompile(`(?i)(?:^|[^\p{L}\p{N}])(?:newcomers|ньюкамерс|new\s*comers)(?:$|[^\p{L}\p{N}])`),
		},
	},
	{
		Canonical: "Кураж-Бамбей",
		Patterns: []*regexp.Regexp{
			regexp.MustCompile(`(?i)(?:^|[^\p{L}\p{N}])(?:кураж(?:-|\s*)бамбей|kuraj(?:-|\s*)bambey)(?:$|[^\p{L}\p{N}])`),
		},
	},
	{
		Canonical: "AlexFilm",
		Patterns: []*regexp.Regexp{
			regexp.MustCompile(`(?i)(?:^|[^\p{L}\p{N}])(?:alexfilm|алексфильм|alex\s*film)(?:$|[^\p{L}\p{N}])`),
		},
	},
	{
		Canonical: "ViruseProject",
		Patterns: []*regexp.Regexp{
			regexp.MustCompile(`(?i)(?:^|[^\p{L}\p{N}])(?:viruseproject|вируспроджект|viruse\s*project)(?:$|[^\p{L}\p{N}])`),
		},
	},
	{
		Canonical: "ColdFilm",
		Patterns: []*regexp.Regexp{
			regexp.MustCompile(`(?i)(?:^|[^\p{L}\p{N}])(?:coldfilm|колдфильм|cold\s*film)(?:$|[^\p{L}\p{N}])`),
		},
	},
	{
		Canonical: "Jaskier",
		Patterns: []*regexp.Regexp{
			regexp.MustCompile(`(?i)(?:^|[^\p{L}\p{N}])(?:jaskier|яскьер)(?:$|[^\p{L}\p{N}])`),
		},
	},
	{
		Canonical: "Пифагор",
		Patterns: []*regexp.Regexp{
			regexp.MustCompile(`(?i)(?:^|[^\p{L}\p{N}])(?:пифагор|pifagor)(?:$|[^\p{L}\p{N}])`),
		},
	},
	{
		Canonical: "Сербин",
		Patterns: []*regexp.Regexp{
			regexp.MustCompile(`(?i)(?:^|[^\p{L}\p{N}])(?:сербин|serbin)(?:$|[^\p{L}\p{N}])`),
		},
	},
	{
		Canonical: "Гоблин",
		Patterns: []*regexp.Regexp{
			regexp.MustCompile(`(?i)(?:^|[^\p{L}\p{N}])(?:гоблин|пучков|goblin)(?:$|[^\p{L}\p{N}])`),
		},
	},
	{
		Canonical: "Дубляж",
		Patterns: []*regexp.Regexp{
			regexp.MustCompile(`(?i)(?:^|[^\p{L}\p{N}])(?:дубляж|полное\s+дублирование|dub)(?:$|[^\p{L}\p{N}])`),
		},
	},
	{
		Canonical: "Оригинал + Субтитры",
		Patterns: []*regexp.Regexp{
			regexp.MustCompile(`(?i)(?:^|[^\p{L}\p{N}])(?:original|оригинал)(?:$|[^\p{L}\p{N}]).*(?:sub|субтитр)`),
		},
	},
}

// NormalizeStudio attempts to map any alias or variant to its canonical studio name.
func NormalizeStudio(studio string) string {
	clean := strings.TrimSpace(studio)
	if clean == "" {
		return ""
	}
	for _, s := range knownStudios {
		if strings.EqualFold(clean, s.Canonical) {
			return s.Canonical
		}
		for _, pat := range s.Patterns {
			if pat.MatchString(" " + clean + " ") {
				return s.Canonical
			}
		}
	}
	return clean
}

// ExtractStudios inspects a release title and returns all matched canonical studio names.
func ExtractStudios(title string) []string {
	clean := strings.TrimSpace(title)
	if clean == "" {
		return nil
	}
	spaced := " " + clean + " "

	foundMap := make(map[string]bool)
	var result []string

	for _, s := range knownStudios {
		for _, pat := range s.Patterns {
			if pat.MatchString(spaced) {
				if !foundMap[s.Canonical] {
					foundMap[s.Canonical] = true
					result = append(result, s.Canonical)
				}
				break
			}
		}
	}

	return result
}

// MatchesStudio checks whether a list of studios matches a user-preferred studio.
// If preferred is empty or "Любая", it matches any Russian studio or dub.
func MatchesStudio(studios []string, preferred string) bool {
	prefClean := strings.TrimSpace(strings.ToLower(preferred))
	if prefClean == "" || prefClean == "любая" || prefClean == "any" {
		return len(studios) > 0
	}

	canonicalPref := NormalizeStudio(preferred)

	for _, st := range studios {
		if strings.EqualFold(st, canonicalPref) {
			return true
		}
		stLower := strings.ToLower(st)
		if stLower == prefClean || strings.Contains(stLower, prefClean) || strings.Contains(prefClean, stLower) {
			return true
		}
	}
	return false
}

// ExtractEpisodeMax extracts the maximum episode number detected in a release title.
// Supports patterns like "серии 1-2 из 10", "[02x01-04]", "серии: 1-5", "Серия 3".
func ExtractEpisodeMax(title string) int {
	clean := strings.TrimSpace(title)
	if clean == "" {
		return 0
	}

	// 1. Range: "серии: 1-4 из 10" or "серии 1-4"
	reRange := regexp.MustCompile(`(?i)сери(?:и|я)?\s*[:\s]\s*\d+\s*[-–—]\s*(\d+)`)
	if m := reRange.FindStringSubmatch(clean); len(m) > 1 {
		var n int
		for _, c := range m[1] {
			if c >= '0' && c <= '9' {
				n = n*10 + int(c-'0')
			}
		}
		if n > 0 && n < 1000 {
			return n
		}
	}

	// 2. Tag: "[02x01-04]" or "02х01-04"
	reTagRange := regexp.MustCompile(`(?i)\d+[xх]\d+\s*[-–—]\s*(\d+)`)
	if m := reTagRange.FindStringSubmatch(clean); len(m) > 1 {
		var n int
		for _, c := range m[1] {
			if c >= '0' && c <= '9' {
				n = n*10 + int(c-'0')
			}
		}
		if n > 0 && n < 1000 {
			return n
		}
	}

	// 3. Single episode: "02х03", "S02E03", "серия 3"
	reSingle := regexp.MustCompile(`(?i)(?:[xх]|e|сери[яи]\s*)0*(\d{1,3})\b`)
	if m := reSingle.FindStringSubmatch(clean); len(m) > 1 {
		var n int
		for _, c := range m[1] {
			if c >= '0' && c <= '9' {
				n = n*10 + int(c-'0')
			}
		}
		if n > 0 && n < 1000 {
			return n
		}
	}

	return 0
}

// ListKnownStudios returns a slice of all canonical studio names known to the system.
func ListKnownStudios() []string {
	res := make([]string, len(knownStudios))
	for i, s := range knownStudios {
		res[i] = s.Canonical
	}
	return res
}
