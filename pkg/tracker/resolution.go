package tracker

import (
	"regexp"
)

var (
	// Direct 2160p / UHD patterns
	re2160 = regexp.MustCompile(`(?i)(?:^|[^\w])(?:2160[pi]?|uhd|ultra[\s-]?hd)(?:[^\w]|$)`)

	// 1080p / FullHD / FHD patterns
	re1080 = regexp.MustCompile(`(?i)(?:^|[^\w])(?:1080[pi]?|1920x\d{3,4}|full[\s-]?hd|fhd)(?:[^\w]|$)`)

	// 720p patterns (assigned to LQ)
	re720 = regexp.MustCompile(`(?i)(?:^|[^\w])(?:720[pi]?|1280x\d{3,4})(?:[^\w]|$)`)

	// Standalone 4K / 4К (matches both Latin 'k' and Cyrillic 'к')
	re4K = regexp.MustCompile(`(?i)(?:^|[^\w])4[kк](?:[^\w]|$)`)

	// Remaster/restore tags where 4K refers to studio scan source, not release resolution
	re4KRemaster = regexp.MustCompile(`(?i)4[kк]\s*(?:remaster|restor)`)
)

// ExtractResolution classifies a torrent title into "4k", "1080p", or "lq".
func ExtractResolution(title string) string {
	// 1. Definite 2160p / UHD
	if re2160.MatchString(title) {
		return "4k"
	}

	// 2. 1080p / FHD (checked before generic 4k to prevent "4K Remaster 1080p" false positives)
	if re1080.MatchString(title) {
		return "1080p"
	}

	// 3. 720p is classified as LQ
	if re720.MatchString(title) {
		return "lq"
	}

	// 4. Standalone 4K (Latin 'k' or Cyrillic 'к') when not accompanied by remaster tag
	if re4K.MatchString(title) && !re4KRemaster.MatchString(title) {
		return "4k"
	}

	// 5. Default: LQ (DVDRip, HDRip, BDRip-AVC without 1080p, WEB-DLRip, etc.)
	return "lq"
}
