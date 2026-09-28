package hotlist

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
)

type IndexerHit struct {
	Movie struct {
		Tconst         string   `json:"tconst"`
		TitleRu        *string  `json:"title_ru"`
		TitleOrig      string   `json:"title_orig"`
		TitlePrimary   string   `json:"title_primary"`
		RussianTitles  []string `json:"russian_titles"`
		Year           *int     `json:"year"`
		TitleType      string   `json:"title_type"`
		Rating         *float64 `json:"rating"`
		NumVotes       int      `json:"num_votes"`
		Genres         []string `json:"genres"`
		RuntimeMinutes *int     `json:"runtime_minutes"`
	} `json:"movie"`
	PosterPath   *string `json:"poster_path"`
	BackdropPath *string `json:"backdrop_path"`
	PosterURL    string  `json:"poster_url"`
	Posters      struct {
		Thumbnail string `json:"thumbnail"`
		Small     string `json:"small"`
		Medium    string `json:"medium"`
		Large     string `json:"large"`
	} `json:"posters"`
}

type IndexerSearchResponse struct {
	Query     string       `json:"query"`
	TotalHits int          `json:"total_hits"`
	TookMs    float64      `json:"took_ms"`
	Hits      []IndexerHit `json:"hits"`
}

type Matcher struct {
	indexerURL string
	client     *http.Client
}

func NewMatcher(indexerURL string) *Matcher {
	if indexerURL == "" {
		indexerURL = "http://imdb-indexer:8090"
	}
	return &Matcher{
		indexerURL: strings.TrimRight(indexerURL, "/"),
		client: &http.Client{
			Timeout: 5 * time.Second,
		},
	}
}

// MatchAndGroup matches raw torrents to IMDb metadata and aggregates duplicates
func (m *Matcher) MatchAndGroup(ctx context.Context, torrents []RawTorrent, mediaType string) []Item {
	// First gather unique torrent items needing lookup
	uniqueTorrents := make(map[string]RawTorrent)
	for _, t := range torrents {
		cacheKey := fmt.Sprintf("%s:%s:%s:%d", mediaType, t.OriginalTitle, t.RussianTitle, t.Year)
		if _, exists := uniqueTorrents[cacheKey]; !exists {
			uniqueTorrents[cacheKey] = t
		}
	}

	// Concurrently resolve IMDb metadata with 16 workers
	lookupCache := make(map[string]*IndexerHit)
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 16)

	for k, t := range uniqueTorrents {
		wg.Add(1)
		go func(key string, item RawTorrent) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				return
			}

			hit := m.lookup(ctx, item, mediaType)
			if hit != nil {
				mu.Lock()
				lookupCache[key] = hit
				mu.Unlock()
			}
		}(k, t)
	}
	wg.Wait()

	// Map tconst (or fallback key) -> Aggregated Item
	grouped := make(map[string]*Item)
	var order []string

	for _, t := range torrents {
		cacheKey := fmt.Sprintf("%s:%s:%s:%d", mediaType, t.OriginalTitle, t.RussianTitle, t.Year)
		hit := lookupCache[cacheKey]

		groupKey := ""
		displayTitle := t.RussianTitle
		originalTitle := t.OriginalTitle
		year := t.Year
		rating := 0.0
		voteCount := 0
		posterPath := ""
		backdropPath := ""
		genres := []string{}
		tconst := ""

		if hit != nil {
			tconst = hit.Movie.Tconst
			groupKey = tconst
			if hit.Movie.TitleRu != nil && *hit.Movie.TitleRu != "" {
				displayTitle = *hit.Movie.TitleRu
			} else if hit.Movie.TitlePrimary != "" {
				displayTitle = hit.Movie.TitlePrimary
			}
			originalTitle = hit.Movie.TitleOrig
			if hit.Movie.Year != nil {
				year = *hit.Movie.Year
			}
			if hit.Movie.Rating != nil {
				rating = *hit.Movie.Rating
			}
			voteCount = hit.Movie.NumVotes
			if hit.PosterPath != nil && *hit.PosterPath != "" {
				posterPath = *hit.PosterPath
			} else if hit.Posters.Large != "" && !strings.HasPrefix(hit.Posters.Large, "/poster/") {
				posterPath = hit.Posters.Large
			} else if hit.PosterURL != "" && !strings.HasPrefix(hit.PosterURL, "/poster/") {
				posterPath = hit.PosterURL
			}
			if hit.BackdropPath != nil && *hit.BackdropPath != "" {
				backdropPath = *hit.BackdropPath
			} else {
				backdropPath = posterPath
			}
			genres = hit.Movie.Genres
		} else {
			// Fallback grouping key: title + year
			groupKey = fmt.Sprintf("fallback:%s:%d", strings.ToLower(originalTitle), year)
			if displayTitle == "" {
				displayTitle = originalTitle
			}
		}

		if existing, found := grouped[groupKey]; found {
			existing.Seeds += t.Seeds
			existing.Leeches += t.Leeches
			existing.TorrentCount++
			if t.PublishDate.After(existing.PublishDate) {
				existing.PublishDate = t.PublishDate
			}
			if shouldUpgradeQuality(existing.Quality, t.Quality) {
				existing.Quality = t.Quality
				existing.Resolution = determineResolution(t.Quality)
			}
		} else {
			item := &Item{
				Tconst:        tconst,
				Title:         displayTitle,
				OriginalTitle: originalTitle,
				Year:          year,
				Rating:        rating,
				VoteCount:     voteCount,
				PosterPath:    posterPath,
				BackdropPath:  backdropPath,
				Genres:        genres,
				MediaType:     mediaType,
				Seeds:         t.Seeds,
				Leeches:       t.Leeches,
				Tracker:       t.Tracker,
				Quality:       t.Quality,
				Resolution:    determineResolution(t.Quality),
				TorrentCount:  1,
				PublishDate:   t.PublishDate,
			}
			grouped[groupKey] = item
			order = append(order, groupKey)
		}
	}

	// Collect items
	result := make([]Item, 0, len(grouped))
	for _, k := range order {
		result = append(result, *grouped[k])
	}

	// For fresh/new categories, sort strictly by PublishDate descending; otherwise by seeds
	if strings.HasPrefix(mediaType, "new") || strings.HasPrefix(mediaType, "fresh") {
		sort.SliceStable(result, func(i, j int) bool {
			return result[i].PublishDate.After(result[j].PublishDate)
		})
	} else {
		sort.SliceStable(result, func(i, j int) bool {
			return result[i].Seeds > result[j].Seeds
		})
	}

	// Reassign IDs 1..N for stable frontend rendering
	for i := range result {
		result[i].ID = i + 1
	}

	return result
}

var (
	bracketStripRegex = regexp.MustCompile(`\[.*?\]|\(.*?\)|<.*?>`)
)

func cleanHotlistQuery(s string) string {
	s = bracketStripRegex.ReplaceAllString(s, " ")
	s = strings.ReplaceAll(s, "&", " and ")
	s = strings.ReplaceAll(s, "/", " ")
	s = strings.ReplaceAll(s, ":", " ")
	s = strings.ReplaceAll(s, "!", " ")
	s = strings.ReplaceAll(s, "?", " ")
	s = strings.ReplaceAll(s, "\"", " ")
	s = strings.ReplaceAll(s, ".", " ")
	s = strings.ReplaceAll(s, ",", " ")
	s = strings.ReplaceAll(s, "|", " ")
	return strings.TrimSpace(strings.Join(strings.Fields(s), " "))
}

func normalizeHotlistText(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func selectBestHit(hits []IndexerHit, targetRaw RawTorrent, query string) *IndexerHit {
	if len(hits) == 0 {
		return nil
	}
	if len(hits) == 1 {
		return &hits[0]
	}

	normQuery := normalizeHotlistText(query)
	normOrig := normalizeHotlistText(targetRaw.OriginalTitle)
	normRu := normalizeHotlistText(targetRaw.RussianTitle)

	bestIdx := 0
	bestScore := -10000.0

	for i, hit := range hits {
		score := 0.0

		// Desirable vs undesirable title types
		tType := strings.ToLower(hit.Movie.TitleType)
		switch tType {
		case "movie", "tvseries", "tvminiseries":
			score += 25.0
		case "tvepisode", "videogame", "short":
			score -= 50.0
		}

		// Reward having a poster
		hasPoster := (hit.PosterPath != nil && *hit.PosterPath != "") ||
			(hit.Posters.Large != "" && !strings.HasPrefix(hit.Posters.Large, "/poster/")) ||
			(hit.PosterURL != "" && !strings.HasPrefix(hit.PosterURL, "/poster/"))
		if hasPoster {
			score += 30.0
		}

		// Year matching
		if targetRaw.Year > 1900 && hit.Movie.Year != nil {
			diff := *hit.Movie.Year - targetRaw.Year
			if diff < 0 {
				diff = -diff
			}
			if diff == 0 {
				score += 40.0
			} else if diff == 1 {
				score += 25.0
			} else if diff <= 3 {
				score += 10.0
			} else {
				score -= 20.0
			}
		}

		// Title similarity
		hitOrig := normalizeHotlistText(hit.Movie.TitleOrig)
		hitPrimary := normalizeHotlistText(hit.Movie.TitlePrimary)
		hitRu := ""
		if hit.Movie.TitleRu != nil {
			hitRu = normalizeHotlistText(*hit.Movie.TitleRu)
		}

		if (normOrig != "" && (hitOrig == normOrig || hitPrimary == normOrig)) ||
			(normRu != "" && hitRu == normRu) ||
			(normQuery != "" && (hitOrig == normQuery || hitRu == normQuery)) {
			score += 60.0
		} else if (normOrig != "" && (strings.HasPrefix(hitOrig, normOrig) || strings.HasPrefix(normOrig, hitOrig))) ||
			(normRu != "" && (strings.HasPrefix(hitRu, normRu) || strings.HasPrefix(normRu, hitRu))) {
			score += 30.0
		}

		if hit.Movie.Rating != nil && *hit.Movie.Rating > 0 {
			score += *hit.Movie.Rating
		}

		if score > bestScore {
			bestScore = score
			bestIdx = i
		}
	}

	return &hits[bestIdx]
}

func (m *Matcher) lookup(ctx context.Context, t RawTorrent, mediaType string) *IndexerHit {
	// Query priority: OriginalTitle > RussianTitle > Raw slash parts
	queries := []string{}
	addQuery := func(q string) {
		q = cleanHotlistQuery(q)
		if q == "" || len(q) < 2 {
			return
		}
		for _, existing := range queries {
			if strings.EqualFold(existing, q) {
				return
			}
		}
		queries = append(queries, q)
	}

	if t.OriginalTitle != "" {
		addQuery(t.OriginalTitle)
	}
	if t.RussianTitle != "" && t.RussianTitle != t.OriginalTitle {
		addQuery(t.RussianTitle)
	}

	// Extract slash-separated parts from RawTitle (e.g. "Поток / Straume / Flow (2024)...")
	rawBeforeYear := t.RawTitle
	if idx := strings.IndexAny(rawBeforeYear, "(["); idx > 0 {
		rawBeforeYear = rawBeforeYear[:idx]
	}
	for _, p := range strings.Split(rawBeforeYear, "/") {
		addQuery(p)
	}

	// Determine titleType for Tantivy search:
	// For "new_kids", "kids_popular", "anime": titleType is "" to allow both movies and TV series
	titleType := ""
	switch mediaType {
	case "tv", "new_tv":
		titleType = "tvSeries"
	case "movie", "new_movie", "doc":
		titleType = "movie"
	default:
		titleType = ""
	}

	doSearch := func(query string, withYear bool, filterType string) *IndexerHit {
		qClean := cleanHotlistQuery(query)
		if qClean == "" {
			return nil
		}
		u, err := url.Parse(fmt.Sprintf("%s/search", strings.TrimRight(m.indexerURL, "/")))
		if err != nil {
			return nil
		}
		params := url.Values{}
		params.Set("q", qClean)
		if filterType != "" {
			params.Set("type", filterType)
		}
		params.Set("limit", "5")
		if withYear && t.Year > 1900 {
			params.Set("year_from", fmt.Sprintf("%d", t.Year-1))
			params.Set("year_to", fmt.Sprintf("%d", t.Year+1))
		}
		u.RawQuery = params.Encode()

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		if err != nil {
			return nil
		}
		resp, err := m.client.Do(req)
		if err != nil {
			return nil
		}
		defer resp.Body.Close()

		var searchResp IndexerSearchResponse
		if err := json.NewDecoder(resp.Body).Decode(&searchResp); err == nil && len(searchResp.Hits) > 0 {
			return selectBestHit(searchResp.Hits, t, qClean)
		}
		return nil
	}

	// 1. Try queries with year filter and category titleType
	for _, q := range queries {
		if hit := doSearch(q, true, titleType); hit != nil {
			return hit
		}
	}
	// 2. Retry queries without year filter (trackers often differ by ±1-3 years or re-seeds like 2026 vs 1986)
	for _, q := range queries {
		if hit := doSearch(q, false, titleType); hit != nil {
			return hit
		}
	}
	// 3. If category had strict titleType (e.g. "movie" or "tvSeries"), retry without titleType restriction
	if titleType != "" {
		for _, q := range queries {
			if hit := doSearch(q, true, ""); hit != nil {
				return hit
			}
			if hit := doSearch(q, false, ""); hit != nil {
				return hit
			}
		}
	}

	return nil
}

func shouldUpgradeQuality(current, candidate string) bool {
	score := func(q string) int {
		q = strings.ToUpper(q)
		if strings.Contains(q, "2160P") || strings.Contains(q, "4K") || strings.Contains(q, "UHD") {
			return 4
		}
		if strings.Contains(q, "1080P") {
			return 3
		}
		if strings.Contains(q, "720P") {
			return 2
		}
		if strings.Contains(q, "480P") {
			return 1
		}
		return 0
	}
	return score(candidate) > score(current)
}

func determineResolution(quality string) string {
	q := strings.ToUpper(quality)
	if strings.Contains(q, "2160P") || strings.Contains(q, "4K") || strings.Contains(q, "UHD") {
		return "4k"
	}
	if strings.Contains(q, "1080P") || strings.Contains(q, "FHD") {
		return "1080p"
	}
	if strings.Contains(q, "720P") || strings.Contains(q, "HD") {
		return "720p"
	}
	return "lq"
}

