package stream

import (
	"regexp"
	"strings"

	"tracker-proxy/pkg/playback"
)

type ChapterMarker struct {
	Title     string  `json:"title"`
	StartTime float64 `json:"start_time"`
	EndTime   float64 `json:"end_time"`
}

var (
	introRegex   = regexp.MustCompile(`(?i)(intro|opening|заставка|вступление|cold\s*open\s*end|op)`)
	creditsRegex = regexp.MustCompile(`(?i)(credits|ending|outro|титры|ed)`)
)

// ClassifyChaptersToSkipSegments detects intro and credits segments based on titles and timing heuristics
func ClassifyChaptersToSkipSegments(chapters []ChapterMarker, durationSeconds float64, isTvSeries bool) []playback.SkipSegment {
	if len(chapters) == 0 {
		return nil
	}

	var segments []playback.SkipSegment
	var introSeg *playback.SkipSegment
	var creditsSeg *playback.SkipSegment

	// 1. Pass 1: Semantic Title Matching
	for _, ch := range chapters {
		title := strings.TrimSpace(ch.Title)
		if introSeg == nil && introRegex.MatchString(title) {
			introSeg = &playback.SkipSegment{
				Type:      "intro",
				StartTime: ch.StartTime,
				EndTime:   ch.EndTime,
				Label:     "Пропустить заставку",
			}
		}
		if creditsSeg == nil && creditsRegex.MatchString(title) {
			creditsSeg = &playback.SkipSegment{
				Type:      "credits",
				StartTime: ch.StartTime,
				EndTime:   ch.EndTime,
				Label:     func() string { if isTvSeries { return "Следующая серия" }; return "Пропустить титры" }(),
			}
		}
	}

	// 2. Pass 2: Heuristic Fallback if titles are generic (e.g. "Chapter 01", "Chapter 02")
	if introSeg == nil && isTvSeries && len(chapters) >= 2 {
		firstCh := chapters[0]
		dur := firstCh.EndTime - firstCh.StartTime
		// Intro starting at or near the beginning (<= 35s) and lasting 25s - 130s
		if firstCh.StartTime <= 35.0 && dur >= 25.0 && dur <= 130.0 {
			introSeg = &playback.SkipSegment{
				Type:      "intro",
				StartTime: firstCh.StartTime,
				EndTime:   firstCh.EndTime,
				Label:     "Пропустить заставку",
			}
		} else if len(chapters) >= 3 && firstCh.StartTime <= 10.0 && dur >= 60.0 && dur <= 420.0 {
			// Possible Cold Open in Chapter 01 (1 to 7 mins) -> Chapter 02 might be the Intro
			secondCh := chapters[1]
			secondDur := secondCh.EndTime - secondCh.StartTime
			if secondDur >= 25.0 && secondDur <= 130.0 {
				introSeg = &playback.SkipSegment{
					Type:      "intro",
					StartTime: secondCh.StartTime,
					EndTime:   secondCh.EndTime,
					Label:     "Пропустить заставку",
				}
			}
		}
	}

	if creditsSeg == nil && len(chapters) >= 2 {
		lastCh := chapters[len(chapters)-1]
		dur := lastCh.EndTime - lastCh.StartTime
		// If last chapter is within the last 240s of the video and duration is 25s - 240s
		if (durationSeconds <= 0 || durationSeconds-lastCh.StartTime <= 240.0) && dur >= 25.0 && dur <= 240.0 {
			creditsSeg = &playback.SkipSegment{
				Type:      "credits",
				StartTime: lastCh.StartTime,
				EndTime:   lastCh.EndTime,
				Label:     func() string { if isTvSeries { return "Следующая серия" }; return "Пропустить титры" }(),
			}
		}
	}

	if introSeg != nil {
		segments = append(segments, *introSeg)
	}
	if creditsSeg != nil {
		segments = append(segments, *creditsSeg)
	}
	return segments
}
