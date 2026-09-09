package hotlist

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
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
	PosterURL string `json:"poster_url"`
	Posters   struct {
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
	type key struct {
		id string
	}

	// Map tconst (or fallback key) -> Aggregated Item
	grouped := make(map[string]*Item)
	lookupCache := make(map[string]*IndexerHit)
	var order []string

	for i, t := range torrents {
		// Attempt match against Tantivy indexer (with in-run cache)
		cacheKey := fmt.Sprintf("%s:%s:%s:%d", mediaType, t.OriginalTitle, t.RussianTitle, t.Year)
		hit, cached := lookupCache[cacheKey]
		if !cached {
			hit = m.lookup(ctx, t, mediaType)
			lookupCache[cacheKey] = hit
		}

		groupKey := ""
		displayTitle := t.RussianTitle
		originalTitle := t.OriginalTitle
		year := t.Year
		rating := 0.0
		voteCount := 0
		posterPath := ""
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
			posterPath = hit.Posters.Large
			if posterPath == "" {
				posterPath = hit.PosterURL
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
			if shouldUpgradeQuality(existing.Quality, t.Quality) {
				existing.Quality = t.Quality
			}
			newRes := determineResolution(t.Quality)
			if newRes == "4k" || existing.Resolution == "" {
				existing.Resolution = determineResolution(existing.Quality)
			}
		} else {
			id := i + 1
			item := &Item{
				ID:            id,
				Tconst:        tconst,
				MediaType:     mediaType,
				Title:         displayTitle,
				OriginalTitle: originalTitle,
				Year:          year,
				Rating:        rating,
				VoteCount:     voteCount,
				PosterPath:    posterPath,
				Genres:        genres,
				Seeds:         t.Seeds,
				Leeches:       t.Leeches,
				Tracker:       t.Tracker,
				Quality:       t.Quality,
				Resolution:    determineResolution(t.Quality),
				TorrentCount:  1,
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

	// Sort by total seeds descending
	sort.SliceStable(result, func(i, j int) bool {
		return result[i].Seeds > result[j].Seeds
	})

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
	if mediaType == "tv" {
		titleType = "tvSeries"
	}

	candidateHosts := []string{m.indexerURL}
	if !strings.Contains(m.indexerURL, "host.docker.internal") {
		candidateHosts = append(candidateHosts, "http://host.docker.internal:8090")
	}

	for _, host := range candidateHosts {
		for _, q := range queries {
			u, err := url.Parse(fmt.Sprintf("%s/search", strings.TrimRight(host, "/")))
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

