package hotlist

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

var (
	// Matches (2026), [2026], [1986, СССР...], [2008-2013, ...], [2020-..., ...], (2019-2024), (2025/WEB-DL)
	yearRegex = regexp.MustCompile(`(?i)[\(\[]\s*(19\d{2}|20\d{2})(?:\s*[-–—]\s*(19\d{2}|20\d{2}|\.{2,3}))?(?:[,\s\)\/\]]|$)`)

	// Fallback standalone year: 1900..2035 bounded by word boundaries
	standaloneYearRegex = regexp.MustCompile(`\b(19\d{2}|20\d{2})\b`)

	// Matches season tags like [S01], [S03], [02x01-07], [Сезон 1], [01-04 из 04]
	seasonRegex = regexp.MustCompile(`(?i)\[(?:S(\d+)|(\d+)x(\d+(?:-\d+)?)|сезон\s*(\d+)|(\d+-\d+\s*из\s*\d+))[^\]]*\]`)

	// Matches season/episode text in title parts like "Сезон: 1", "Сезоны 1-5", "Серии 1-8"
	seasonTextRegex = regexp.MustCompile(`(?i)\b(?:сезон\S*|сери\S*)\s*[:\s]*\d+`)

	// Trailing director/author in parentheses before year bracket, e.g. "(Карен Шахназаров)" or "(Даниэль Кальпарсоро / Daniel Calparsoro)"
	trailingDirectorRegex = regexp.MustCompile(`\s*\([^\)]*\)\s*$`)

	// Resolution tags
	resRegex = regexp.MustCompile(`(?i)\b(2160p|4k|uhd|1080p|1080i|720p|480p)\b`)

	// Rip / Format tags
	formatRegex = regexp.MustCompile(`(?i)\b(bdremux|web-dlremux|web-dlrip|web-dl|webrip|bdrip|hdtvrip|hdtv|dvdrip)\b`)

	// HDR tags
	hdrRegex = regexp.MustCompile(`(?i)\b(hdr10\+|hdr10|hdr|dolby vision|dv|sdr)\b`)

	// Non-video media tags (audiobooks, music, games, books, software)
	// Note: Go regexp \b is ASCII-only, so we use (?:^|[^\p{L}\p{N}]) for Unicode word boundaries
	nonVideoRegex = regexp.MustCompile(`(?i)(` +
		`(?:^|[^\p{L}\p{N}])(flac|lossless|alac|ape|soundtrack|ost|audiobook|аудиокниг\S*|m4b|wav|ogg|aac)(?:$|[^\p{L}\p{N}])|` +
		`(?:^|[^\p{L}\p{N}])(?:mp3|мп3|мр3|[мm][рpп]3)(?:$|[^\p{L}\p{N}])|` +
		`(?:^|[^\p{L}\p{N}])(repack by|gog|pc game|crack|patch|pdf|fb2|epub|djvu|cbr|cbz)(?:$|[^\p{L}\p{N}])|` +
		`\[(?:flac|mp3|мп3|мр3|[мm][рpп]3|lossless|pc|iso|android|ios)[^\]]*\]|` +
		`(?:^|[^\p{L}\p{N}])том\s*\d+` +
	`)`)
)

// IsNonVideo returns true if the release title represents non-video media
func IsNonVideo(title string) bool {
	return nonVideoRegex.MatchString(title)
}

// ParseReleaseDetails parses a tracker release title into comprehensive structured fields including year ranges and ongoing status
func ParseReleaseDetails(raw string) (russianTitle, originalTitle string, startYear, endYear int, isOngoing bool, season, quality string) {
	clean := strings.TrimSpace(raw)
	if clean == "" {
		return "", "", 0, 0, false, "", ""
	}

	// 1. Extract Year & Range
	if m := yearRegex.FindStringSubmatch(clean); len(m) > 1 {
		if y, err := strconv.Atoi(m[1]); err == nil && y >= 1900 && y <= 2035 {
			startYear = y
			endYear = y
		}
		if len(m) > 2 && m[2] != "" {
			if strings.Contains(m[2], ".") {
				isOngoing = true
				endYear = 0
			} else if y2, err := strconv.Atoi(m[2]); err == nil && y2 >= startYear && y2 <= 2035 {
				endYear = y2
			}
		}
	} else if m := standaloneYearRegex.FindStringSubmatch(clean); len(m) > 1 {
		if y, err := strconv.Atoi(m[1]); err == nil && y >= 1900 && y <= 2035 {
			startYear = y
			endYear = y
		}
	}

	// 2. Extract Season if present
	if m := seasonRegex.FindString(clean); m != "" {
		season = strings.Trim(m, "[] ")
	}

	// 3. Extract Quality tags
	var qParts []string
	if m := resRegex.FindString(clean); m != "" {
		qParts = append(qParts, strings.ToUpper(m))
	}
	if m := hdrRegex.FindString(clean); m != "" {
		qParts = append(qParts, strings.ToUpper(m))
	}
	if m := formatRegex.FindString(clean); m != "" {
		qParts = append(qParts, strings.ToUpper(m))
	}
	if len(qParts) > 0 {
		quality = strings.Join(qParts, " ")
	}

	// 4. Extract Titles: look at the portion before year and season
	titlePart := clean
	if loc := yearRegex.FindStringIndex(clean); len(loc) > 0 {
		titlePart = clean[:loc[0]]
	} else if loc := standaloneYearRegex.FindStringIndex(clean); len(loc) > 0 {
		titlePart = clean[:loc[0]]
	}
	if loc := seasonRegex.FindStringIndex(titlePart); len(loc) > 0 {
		titlePart = titlePart[:loc[0]]
	}

	titlePart = strings.TrimSpace(titlePart)

	// Strip trailing director/uploader note in parentheses like "(Карен Шахназаров)" or "(Винс Гиллиган)"
	if stripped := trailingDirectorRegex.ReplaceAllString(titlePart, ""); strings.TrimSpace(stripped) != "" {
		titlePart = strings.TrimSpace(stripped)
	}

	parts := strings.Split(titlePart, "/")

	var cyrillicParts []string
	var latinParts []string

	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}

		// Skip season/episode noise inside slash-separated parts like "Сезон: 1" or "Серии 1-7 из 7"
		if seasonTextRegex.MatchString(p) {
			continue
		}

		hasCyrillic := containsCyrillic(p)
		hasLatin := containsLatin(p)

		if hasCyrillic && !hasLatin {
			cyrillicParts = append(cyrillicParts, p)
		} else if hasLatin && !hasCyrillic {
			latinParts = append(latinParts, p)
		} else if hasCyrillic && hasLatin {
			// Mixed text, prioritize as Russian if it contains Cyrillic words
			cyrillicParts = append(cyrillicParts, p)
		} else {
			// Numbers or symbols
			if len(cyrillicParts) == 0 {
				cyrillicParts = append(cyrillicParts, p)
			}
		}
	}

	if len(cyrillicParts) > 0 {
		russianTitle = cyrillicParts[0]
	}
	if len(latinParts) > 0 {
		// Prefer the last latin part if multiple (e.g. Kazakh/Russian/English)
		originalTitle = latinParts[len(latinParts)-1]
	}

	// Fallbacks: if no Russian title, use original title
	if russianTitle == "" && originalTitle != "" {
		russianTitle = originalTitle
	}
	if russianTitle == "" && len(parts) > 0 {
		russianTitle = strings.TrimSpace(parts[0])
	}

	return russianTitle, originalTitle, startYear, endYear, isOngoing, season, quality
}

// ParseReleaseTitle parses a tracker release title into structured fields (backwards-compatible)
func ParseReleaseTitle(raw string) (russianTitle, originalTitle string, year int, season, quality string) {
	var endYear int
	var isOngoing bool
	russianTitle, originalTitle, year, endYear, isOngoing, season, quality = ParseReleaseDetails(raw)
	_ = endYear
	_ = isOngoing
	if originalTitle == "" && russianTitle != "" {
		originalTitle = russianTitle
	}
	return russianTitle, originalTitle, year, season, quality
}

func containsCyrillic(s string) bool {
	for _, r := range s {
		if unicode.Is(unicode.Cyrillic, r) {
			return true
		}
	}
	return false
}

func containsLatin(s string) bool {
	for _, r := range s {
		if unicode.Is(unicode.Latin, r) {
			return true
		}
	}
	return false
}
