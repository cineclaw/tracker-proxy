package tracker

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var (
	// Complete pack indicators
	reComplete = regexp.MustCompile(`(?i)(?:все\s+сезоны|полный\s+сериал|все\s+серии|complete\s+(?:series|collection|pack)|all\s+seasons)`)

	// Range: "Сезоны: 1-11", "Сезон: 1-5", "Сезон 1-11", "сезон 1-9", "сезоны 1-6 из 6"
	reRuRange1 = regexp.MustCompile(`(?i)сезон(?:ы|а)?\s*[:\s]\s*(\d{1,2})\s*[-–—]\s*(\d{1,2})`)

	// Range: "1-5 сезон", "1-11 сезоны"
	reRuRange2 = regexp.MustCompile(`(?i)(\d{1,2})\s*[-–—]\s*(\d{1,2})\s*сезон`)

	// Comma list: "Сезон: 1, 2" or "Сезоны: 1, 2, 3"
	reRuList = regexp.MustCompile(`(?i)сезон(?:ы)?\s*[:\s]\s*(\d{1,2}(?:\s*,\s*\d{1,2})+)`)

	// Single Russian season: "Сезон: 7", "сезон 1, серии...", "Сезон 2"
	reRuSingle1 = regexp.MustCompile(`(?i)сезон(?:а)?\s*[:\s]\s*(\d{1,2})\b`)

	// Single Russian season: "2 сезон", "1 сезон"
	reRuSingle2 = regexp.MustCompile(`(?i)\b(\d{1,2})\s*сезон`)

	// English Range: "[S01-11]", "[S01-05]", "S01-S05", "S1-S3"
	reEnRange1 = regexp.MustCompile(`(?i)\[?s(\d{1,2})\s*[-–—]\s*s?(\d{1,2})\]?`)

	// English Range: "Season 1-8"
	reEnRange2 = regexp.MustCompile(`(?i)season\s*(\d{1,2})\s*[-–—]\s*(\d{1,2})`)

	// English Single in brackets: "[S09]", "[S2]"
	reEnBracket = regexp.MustCompile(`(?i)\[s(\d{1,2})\]`)

	// English Single: "Season 2"
	reEnSingle = regexp.MustCompile(`(?i)\bseason\s*(\d{1,2})\b`)

	// Scene Sxx tag: "S02", "S02E01" - bounded by word boundary or slash/space
	reSceneTag = regexp.MustCompile(`(?i)(?:^|[\s/\[(_])s(\d{1,2})(?:e\d+)?(?:[\s/\])_]|$)`)
)

// ExtractSeasonInfo parses a torrent title and returns detected seasons slice and whether it's a complete pack.
func ExtractSeasonInfo(title string) (seasons []int, isComplete bool) {
	cleanTitle := strings.TrimSpace(title)
	if cleanTitle == "" {
		return nil, false
	}

	if reComplete.MatchString(cleanTitle) {
		isComplete = true
	}

	seasonSet := make(map[int]struct{})

	// 1. Russian Range 1: "Сезоны: 1-11", "Сезон 1-5"
	if matches := reRuRange1.FindStringSubmatch(cleanTitle); len(matches) == 3 {
		addRange(seasonSet, matches[1], matches[2])
	}

	// 2. Russian Range 2: "1-5 сезон"
	if matches := reRuRange2.FindStringSubmatch(cleanTitle); len(matches) == 3 {
		addRange(seasonSet, matches[1], matches[2])
	}

	// 3. English Range 1: "[S01-11]", "S01-S05"
	if matches := reEnRange1.FindStringSubmatch(cleanTitle); len(matches) == 3 {
		addRange(seasonSet, matches[1], matches[2])
	}

	// 4. English Range 2: "Season 1-8"
	if matches := reEnRange2.FindStringSubmatch(cleanTitle); len(matches) == 3 {
		addRange(seasonSet, matches[1], matches[2])
	}

	// 5. Russian Comma List: "Сезон: 1, 2"
	if matches := reRuList.FindStringSubmatch(cleanTitle); len(matches) == 2 {
		parts := strings.Split(matches[1], ",")
		for _, p := range parts {
			if num, err := strconv.Atoi(strings.TrimSpace(p)); err == nil && num > 0 && num < 100 {
				seasonSet[num] = struct{}{}
			}
		}
	}

	// If no range/list matched, look for single season matches
	if len(seasonSet) == 0 {
		if matches := reRuSingle1.FindStringSubmatch(cleanTitle); len(matches) == 2 {
			addSingle(seasonSet, matches[1])
		} else if matches := reRuSingle2.FindStringSubmatch(cleanTitle); len(matches) == 2 {
			addSingle(seasonSet, matches[1])
		} else if matches := reEnBracket.FindStringSubmatch(cleanTitle); len(matches) == 2 {
			addSingle(seasonSet, matches[1])
		} else if matches := reEnSingle.FindStringSubmatch(cleanTitle); len(matches) == 2 {
			addSingle(seasonSet, matches[1])
		} else if matches := reSceneTag.FindStringSubmatch(cleanTitle); len(matches) == 2 {
			addSingle(seasonSet, matches[1])
		}
	}

	if len(seasonSet) > 0 {
		for s := range seasonSet {
			seasons = append(seasons, s)
		}
		sort.Ints(seasons)
	}

	return seasons, isComplete
}

func addRange(set map[int]struct{}, sStart, sEnd string) {
	start, err1 := strconv.Atoi(sStart)
	end, err2 := strconv.Atoi(sEnd)
	if err1 != nil || err2 != nil || start <= 0 || end < start || end > 100 {
		return
	}
	if end-start > 50 {
		return
	}
	for i := start; i <= end; i++ {
		set[i] = struct{}{}
	}
}

func addSingle(set map[int]struct{}, sNum string) {
	num, err := strconv.Atoi(sNum)
	if err != nil || num <= 0 || num > 100 {
		return
	}
	set[num] = struct{}{}
}
