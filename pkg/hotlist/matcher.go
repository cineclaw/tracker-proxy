package hotlist

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
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

func (m *Matcher) lookup(ctx context.Context, t RawTorrent, mediaType string) *IndexerHit {
	// Query priority: OriginalTitle > RussianTitle
	queries := []string{}
	if t.OriginalTitle != "" {
		queries = append(queries, t.OriginalTitle)
	}
	if t.RussianTitle != "" && t.RussianTitle != t.OriginalTitle {
		queries = append(queries, t.RussianTitle)
	}

	titleType := "movie"
	if mediaType == "tv" || mediaType == "new_tv" {
		titleType = "tvSeries"
	}

	for _, q := range queries {
		u, err := url.Parse(fmt.Sprintf("%s/search", strings.TrimRight(m.indexerURL, "/")))
		if err != nil {
			continue
		}
		params := url.Values{}
		params.Set("q", q)
		params.Set("type", titleType)
		params.Set("limit", "2")
		if t.Year > 1900 {
			params.Set("year_from", fmt.Sprintf("%d", t.Year-1))
			params.Set("year_to", fmt.Sprintf("%d", t.Year+1))
		}
		u.RawQuery = params.Encode()

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		if err != nil {
			continue
		}

		resp, err := m.client.Do(req)
		if err != nil {
			continue
		}

		var searchResp IndexerSearchResponse
		err = json.NewDecoder(resp.Body).Decode(&searchResp)
		resp.Body.Close()

		if err == nil && len(searchResp.Hits) > 0 {
			return &searchResp.Hits[0]
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

