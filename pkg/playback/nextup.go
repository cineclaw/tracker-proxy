package playback

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

type TmdbEpisodeItem struct {
	SeasonNumber  int    `json:"season_number"`
	EpisodeNumber int    `json:"episode_number"`
	Name          string `json:"name"`
	Overview      string `json:"overview"`
	AirDate       string `json:"air_date"`
	StillPath     string `json:"still_path"`
}

type NextUpItem struct {
	ImdbID           string    `json:"imdb_id"`
	MediaType        string    `json:"media_type"` // "tv"
	Title            string    `json:"title"`
	PosterPath       string    `json:"poster_path"`
	BackdropPath     string    `json:"backdrop_path"`
	SeasonNumber     int       `json:"season_number"`
	EpisodeNumber    int       `json:"episode_number"`
	EpisodeTitle     string    `json:"episode_title"`
	EpisodeOverview  string    `json:"episode_overview"`
	EpisodeStillPath string    `json:"episode_still_path"`
	AirDate          string    `json:"air_date"`
	TorrentHash      string    `json:"torrent_hash,omitempty"`
	TorrentLink      string    `json:"torrent_link,omitempty"`
	LastWatchedAt    time.Time `json:"last_watched_at"`
	IsNextUp         bool      `json:"is_next_up"`
}

type NextUpService struct {
	store      *Store
	indexerURL string
	client     *http.Client
	cacheMu    sync.RWMutex
	cache      map[string][]TmdbEpisodeItem
	cacheExp   map[string]time.Time
}

func NewNextUpService(store *Store, indexerURL string) *NextUpService {
	return &NextUpService{
		store:      store,
		indexerURL: indexerURL,
		client:     &http.Client{Timeout: 5 * time.Second},
		cache:      make(map[string][]TmdbEpisodeItem),
		cacheExp:   make(map[string]time.Time),
	}
}

func (s *NextUpService) FetchSeriesEpisodes(imdbId string) ([]TmdbEpisodeItem, error) {
	s.cacheMu.RLock()
	eps, found := s.cache[imdbId]
	exp := s.cacheExp[imdbId]
	s.cacheMu.RUnlock()

	if found && time.Now().Before(exp) {
		return eps, nil
	}

	url := fmt.Sprintf("%s/api/series/%s/episodes", s.indexerURL, imdbId)
	resp, err := s.client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("failed to call indexer episodes API: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("indexer returned status %d", resp.StatusCode)
	}

	var items []TmdbEpisodeItem
	if err := json.NewDecoder(resp.Body).Decode(&items); err != nil {
		return nil, fmt.Errorf("failed to decode episodes JSON: %w", err)
	}

	s.cacheMu.Lock()
	s.cache[imdbId] = items
	s.cacheExp[imdbId] = time.Now().Add(1 * time.Hour)
	s.cacheMu.Unlock()

	return items, nil
}

// GetNextUpItems finds the next episode for recently completed episodes of series
func (s *NextUpService) GetNextUpItems(limit int) ([]NextUpItem, error) {
	if limit <= 0 {
		limit = 10
	}

	completedSeries, err := s.store.GetRecentlyCompletedSeries(limit * 2)
	if err != nil {
		return nil, err
	}

	var nextUpList []NextUpItem
	for _, lastEp := range completedSeries {
		// Check if there is already an in-progress episode for this show
		inProgress, _ := s.store.GetResumeList(50)
		hasInProgress := false
		for _, ip := range inProgress {
			if ip.ImdbID == lastEp.ImdbID {
				hasInProgress = true
				break
			}
		}
		if hasInProgress {
			// If user is already in the middle of watching another episode, show that in Continue Watching instead of Next Up
			continue
		}

		allEps, err := s.FetchSeriesEpisodes(lastEp.ImdbID)
		if err != nil || len(allEps) == 0 {
			continue
		}

		// Find next episode in the same season
		var targetEp *TmdbEpisodeItem
		for i := range allEps {
			if allEps[i].SeasonNumber == lastEp.SeasonNumber && allEps[i].EpisodeNumber == lastEp.EpisodeNumber+1 {
				targetEp = &allEps[i]
				break
			}
		}

		// If no next episode in same season, look for episode 1 of next season
		if targetEp == nil {
			for i := range allEps {
				if allEps[i].SeasonNumber == lastEp.SeasonNumber+1 && allEps[i].EpisodeNumber == 1 {
					targetEp = &allEps[i]
					break
				}
			}
		}

		if targetEp != nil {
			// Check if this next episode is already completed
			nextProgress, _ := s.store.GetItemProgress(lastEp.ImdbID, targetEp.SeasonNumber, targetEp.EpisodeNumber)
			if nextProgress != nil && nextProgress.IsCompleted {
				continue
			}

			nextUpList = append(nextUpList, NextUpItem{
				ImdbID:           lastEp.ImdbID,
				MediaType:        "tv",
				Title:            lastEp.Title,
				PosterPath:       lastEp.PosterPath,
				BackdropPath:     lastEp.BackdropPath,
				SeasonNumber:     targetEp.SeasonNumber,
				EpisodeNumber:    targetEp.EpisodeNumber,
				EpisodeTitle:     targetEp.Name,
				EpisodeOverview:  targetEp.Overview,
				EpisodeStillPath: targetEp.StillPath,
				AirDate:          targetEp.AirDate,
				TorrentHash:      lastEp.TorrentHash,
				TorrentLink:      lastEp.TorrentLink,
				LastWatchedAt:    lastEp.LastWatchedAt,
				IsNextUp:         true,
			})

			if len(nextUpList) >= limit {
				break
			}
		}
	}

	return nextUpList, nil
}
