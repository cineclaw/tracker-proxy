package hotlist

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

var (
	// Matches (2026) or [2026] or 2026 surrounded by spaces/brackets
	yearRegex = regexp.MustCompile(`[\(\[]\s*(\d{4})\s*[\)\]]`)
	
	// Matches season tags like [S01], [S03], [02x01-07], [Сезон 1], [01-04 из 04]
	seasonRegex = regexp.MustCompile(`(?i)\[(?:S(\d+)|(\d+)x(\d+(?:-\d+)?)|сезон\s*(\d+)|(\d+-\d+\s*из\s*\d+))[^\]]*\]`)
	
	// Resolution tags
	resRegex = regexp.MustCompile(`(?i)\b(2160p|4k|uhd|1080p|1080i|720p|480p)\b`)
	
	// Rip / Format tags
	formatRegex = regexp.MustCompile(`(?i)\b(bdremux|web-dlremux|web-dlrip|web-dl|webrip|bdrip|hdtvrip|hdtv|dvdrip)\b`)
	
	// HDR tags
	hdrRegex = regexp.MustCompile(`(?i)\b(hdr10\+|hdr10|hdr|dolby vision|dv|sdr)\b`)
)

// ParseReleaseTitle parses a tracker release title into structured fields
func ParseReleaseTitle(raw string) (russianTitle, originalTitle string, year int, season, quality string) {
	clean := strings.TrimSpace(raw)
	if clean == "" {
		return "", "", 0, "", ""
	}

	// 1. Extract Year
	if m := yearRegex.FindStringSubmatch(clean); len(m) > 1 {
		if y, err := strconv.Atoi(m[1]); err == nil && y >= 1900 && y <= 2035 {
			year = y
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
	// Cut off at first occurrence of (YEAR) or [YEAR]
	if loc := yearRegex.FindStringIndex(clean); len(loc) > 0 {
		titlePart = clean[:loc[0]]
	}
	// Also cut off before season bracket if season came earlier
	if loc := seasonRegex.FindStringIndex(titlePart); len(loc) > 0 {
		titlePart = titlePart[:loc[0]]
	}

	titlePart = strings.TrimSpace(titlePart)
	parts := strings.Split(titlePart, "/")

	var cyrillicParts []string
	var latinParts []string

	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
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

	// Fallbacks
	if russianTitle == "" && originalTitle != "" {
		russianTitle = originalTitle
	}
	if originalTitle == "" && russianTitle != "" {
		originalTitle = russianTitle
	}
	if russianTitle == "" && len(parts) > 0 {
		russianTitle = strings.TrimSpace(parts[0])
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
