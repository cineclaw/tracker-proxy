package stream

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"sort"
	"strings"
)

type AudioTrack struct {
	Index     int    `json:"index"`
	Title     string `json:"title"`
	Language  string `json:"language"`
	Codec     string `json:"codec"`
	Channels  int    `json:"channels"`
	IsDefault bool   `json:"is_default"`
}

type SubtitleTrack struct {
	Index       int    `json:"index"`
	Title       string `json:"title"`
	Language    string `json:"language"`
	Codec       string `json:"codec"`
	IsDefault   bool   `json:"is_default"`
	DeliveryURL string `json:"delivery_url,omitempty"`
}

type EpisodeInfo struct {
	Id              string  `json:"id"`
	Name            string  `json:"name"`
	SeasonNumber    int     `json:"season_number"`
	EpisodeNumber   int     `json:"episode_number"`
	DurationSeconds float64 `json:"duration_seconds"`
	ResumeSeconds   float64 `json:"resume_seconds"`
	IsPlayed        bool    `json:"is_played"`
}

type PlayerInfoResponse struct {
	Success         bool            `json:"success"`
	Error           string          `json:"error,omitempty"`
	ItemId          string          `json:"item_id,omitempty"`
	Title           string          `json:"title,omitempty"`
	RuTitle         string          `json:"ru_title,omitempty"`
	MediaType       string          `json:"media_type,omitempty"` // "Movie" or "Episode"
	DurationSeconds float64         `json:"duration_seconds"`
	ResumeSeconds   float64         `json:"resume_seconds"`
	IsPlayed        bool            `json:"is_played"`
	StreamURL       string          `json:"stream_url,omitempty"`
	MediaSourceId   string          `json:"media_source_id,omitempty"`
	AudioTracks     []AudioTrack    `json:"audio_tracks,omitempty"`
	Subtitles       []SubtitleTrack `json:"subtitles,omitempty"`
	Episodes        []EpisodeInfo   `json:"episodes,omitempty"`
	CurrentSeason   int             `json:"current_season,omitempty"`
	CurrentEpisode  int             `json:"current_episode,omitempty"`
	HasNextEpisode  bool            `json:"has_next_episode,omitempty"`
	NextEpisode     *EpisodeInfo    `json:"next_episode,omitempty"`
}

type PlaybackStartRequest struct {
	ItemId              string  `json:"item_id"`
	MediaSourceId       string  `json:"media_source_id,omitempty"`
	AudioStreamIndex    int     `json:"audio_stream_index,omitempty"`
	SubtitleStreamIndex int     `json:"subtitle_stream_index,omitempty"`
	PositionSeconds     float64 `json:"position_seconds,omitempty"`
}

type PlaybackProgressRequest struct {
	ItemId          string  `json:"item_id"`
	MediaSourceId   string  `json:"media_source_id,omitempty"`
	PositionSeconds float64 `json:"position_seconds"`
	IsPaused        bool    `json:"is_paused"`
	Event           string  `json:"event,omitempty"` // "timeupdate", "pause", "unpause"
}

type PlaybackStopRequest struct {
	ItemId          string  `json:"item_id"`
	MediaSourceId   string  `json:"media_source_id,omitempty"`
	PositionSeconds float64 `json:"position_seconds"`
}

type PlaybackActionResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message,omitempty"`
}

// GetAdminUserId resolves and caches the primary administrator's ID in Jellyfin
func (m *Mounter) GetAdminUserId(ctx context.Context) (string, error) {
	m.adminUserMu.RLock()
	if m.adminUserId != "" {
		id := m.adminUserId
		m.adminUserMu.RUnlock()
		return id, nil
	}
	m.adminUserMu.RUnlock()

	// Check environment override
	if envId := os.Getenv("JELLYFIN_USER_ID"); envId != "" {
		m.adminUserMu.Lock()
		m.adminUserId = envId
		m.adminUserMu.Unlock()
		return envId, nil
	}

	reqURL := fmt.Sprintf("%s/Users", m.jellyfinURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return "", err
	}
	if m.jellyfinAPIKey != "" {
		req.Header.Set("Authorization", fmt.Sprintf("MediaBrowser Token=\"%s\"", m.jellyfinAPIKey))
	}

	resp, err := m.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to fetch users from Jellyfin: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("Jellyfin /Users returned %d: %s", resp.StatusCode, string(body))
	}

	var users []struct {
		ID     string `json:"Id"`
		Name   string `json:"Name"`
		Policy struct {
			IsAdministrator bool `json:"IsAdministrator"`
		} `json:"Policy"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&users); err != nil {
		return "", fmt.Errorf("failed to decode Jellyfin users: %w", err)
	}

	var chosenId string
	for _, u := range users {
		if u.Policy.IsAdministrator {
			chosenId = u.ID
			break
		}
	}
	if chosenId == "" && len(users) > 0 {
		chosenId = users[0].ID
	}

	if chosenId != "" {
		m.adminUserMu.Lock()
		m.adminUserId = chosenId
		m.adminUserMu.Unlock()
		log.Printf("[player] Resolved Jellyfin admin user ID: %s", chosenId)
		return chosenId, nil
	}

	return "", fmt.Errorf("no suitable user found in Jellyfin")
}

// GetPlayerInfo finds the mounted media in Jellyfin and builds comprehensive playback info
func (m *Mounter) GetPlayerInfo(ctx context.Context, tconst string, season int, episode int) (*PlayerInfoResponse, error) {
	if tconst == "" {
		return &PlayerInfoResponse{Success: false, Error: "tconst is required"}, nil
	}

	mountStatus := m.GetMountedStatus(tconst)
	if !mountStatus.Mounted {
		return &PlayerInfoResponse{
			Success: false,
			Error:   "Медиафайл еще не смонтирован в Jellyfin. Пожалуйста, сначала добавьте раздачу.",
		}, nil
	}

	userId, err := m.GetAdminUserId(ctx)
	if err != nil {
		log.Printf("[player] Warning: could not resolve admin user id: %v", err)
	}

	// 1. Search for Jellyfin item matching this media
	itemsURL := fmt.Sprintf("%s/Items?recursive=true&fields=Path,MediaSources,UserData,RunTimeTicks,Overview,ProviderIds", m.jellyfinURL)
	if userId != "" {
		itemsURL += fmt.Sprintf("&userId=%s", userId)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, itemsURL, nil)
	if err != nil {
		return nil, err
	}
	if m.jellyfinAPIKey != "" {
		req.Header.Set("Authorization", fmt.Sprintf("MediaBrowser Token=\"%s\"", m.jellyfinAPIKey))
	}

	resp, err := m.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to search Jellyfin items: %w", err)
	}
	defer resp.Body.Close()

	var jfResp struct {
		Items []struct {
			ID           string `json:"Id"`
			Name         string `json:"Name"`
			Type         string `json:"Type"`
			Path         string `json:"Path"`
			RunTimeTicks int64  `json:"RunTimeTicks"`
			UserData     *struct {
				PlaybackPositionTicks int64 `json:"PlaybackPositionTicks"`
				Played                bool  `json:"Played"`
			} `json:"UserData"`
			ProviderIds map[string]string `json:"ProviderIds"`
			MediaSources []struct {
				ID           string `json:"Id"`
				Container    string `json:"Container"`
				MediaStreams []struct {
					Type         string `json:"Type"`
					Index        int    `json:"Index"`
					DisplayTitle string `json:"DisplayTitle"`
					Language     string `json:"Language"`
					Codec        string `json:"Codec"`
					Channels     int    `json:"Channels"`
					IsDefault    bool   `json:"IsDefault"`
				} `json:"MediaStreams"`
			} `json:"MediaSources"`
		} `json:"Items"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&jfResp); err != nil {
		return nil, fmt.Errorf("failed to decode Jellyfin items: %w", err)
	}

	imdbPattern := fmt.Sprintf("[imdbid-%s]", tconst)
	folderName := mountStatus.FolderName

	var matchedItem *struct {
		ID           string `json:"Id"`
		Name         string `json:"Name"`
		Type         string `json:"Type"`
		Path         string `json:"Path"`
		RunTimeTicks int64  `json:"RunTimeTicks"`
		UserData     *struct {
			PlaybackPositionTicks int64 `json:"PlaybackPositionTicks"`
			Played                bool  `json:"Played"`
		} `json:"UserData"`
		ProviderIds map[string]string `json:"ProviderIds"`
		MediaSources []struct {
			ID           string `json:"Id"`
			Container    string `json:"Container"`
			MediaStreams []struct {
				Type         string `json:"Type"`
				Index        int    `json:"Index"`
				DisplayTitle string `json:"DisplayTitle"`
				Language     string `json:"Language"`
				Codec        string `json:"Codec"`
				Channels     int    `json:"Channels"`
				IsDefault    bool   `json:"IsDefault"`
			} `json:"MediaStreams"`
		} `json:"MediaSources"`
	}

	for i := range jfResp.Items {
		it := &jfResp.Items[i]
		if (folderName != "" && strings.Contains(it.Path, folderName)) ||
			strings.Contains(it.Path, imdbPattern) ||
			(it.ProviderIds != nil && strings.EqualFold(it.ProviderIds["Imdb"], tconst)) {
			matchedItem = it
			break
		}
	}

	if matchedItem == nil {
		return &PlayerInfoResponse{
			Success: false,
			Error:   "Тайтл смонтирован, но Jellyfin еще сканирует библиотеку. Подождите пару секунд и повторите.",
		}, nil
	}

	// 2. Handle Series vs Movie
	isSeries := strings.EqualFold(matchedItem.Type, "Series") || mountStatus.Type == "shows"

	if !isSeries {
		// Single Movie
		var audioTracks []AudioTrack
		var subtitleTracks []SubtitleTrack
		var mediaSourceId string

		if len(matchedItem.MediaSources) > 0 {
			src := matchedItem.MediaSources[0]
			mediaSourceId = src.ID
			for _, stream := range src.MediaStreams {
				if strings.EqualFold(stream.Type, "Audio") {
					title := stream.DisplayTitle
					if title == "" {
						title = fmt.Sprintf("Audio #%d (%s)", stream.Index, strings.ToUpper(stream.Language))
					}
					audioTracks = append(audioTracks, AudioTrack{
						Index:     stream.Index,
						Title:     title,
						Language:  stream.Language,
						Codec:     stream.Codec,
						Channels:  stream.Channels,
						IsDefault: stream.IsDefault,
					})
				} else if strings.EqualFold(stream.Type, "Subtitle") {
					title := stream.DisplayTitle
					if title == "" {
						title = fmt.Sprintf("Subtitles #%d (%s)", stream.Index, strings.ToUpper(stream.Language))
					}
					delivURL := fmt.Sprintf("/jellyfin/Videos/%s/%s/Subtitles/%d/Stream.vtt?api_key=%s",
						matchedItem.ID, mediaSourceId, stream.Index, m.jellyfinAPIKey)
					subtitleTracks = append(subtitleTracks, SubtitleTrack{
						Index:       stream.Index,
						Title:       title,
						Language:    stream.Language,
						Codec:       stream.Codec,
						IsDefault:   stream.IsDefault,
						DeliveryURL: delivURL,
					})
				}
			}
		}

		durationSec := float64(matchedItem.RunTimeTicks) / 10000000.0
		resumeSec := 0.0
		isPlayed := false
		if matchedItem.UserData != nil {
			resumeSec = float64(matchedItem.UserData.PlaybackPositionTicks) / 10000000.0
			isPlayed = matchedItem.UserData.Played
		}

		// Jellyfin HLS master playlist URL
		streamURL := fmt.Sprintf("/jellyfin/Videos/%s/master.m3u8?MediaSourceId=%s&api_key=%s",
			matchedItem.ID, mediaSourceId, m.jellyfinAPIKey)

		return &PlayerInfoResponse{
			Success:         true,
			ItemId:          matchedItem.ID,
			Title:           matchedItem.Name,
			MediaType:       "Movie",
			DurationSeconds: durationSec,
			ResumeSeconds:   resumeSec,
			IsPlayed:        isPlayed,
			StreamURL:       streamURL,
			MediaSourceId:   mediaSourceId,
			AudioTracks:     audioTracks,
			Subtitles:       subtitleTracks,
		}, nil
	}

	// TV Series: fetch all episodes from Jellyfin
	seriesId := matchedItem.ID
	epURL := fmt.Sprintf("%s/Shows/%s/Episodes?fields=Path,MediaSources,UserData,RunTimeTicks,Overview,IndexNumber,ParentIndexNumber",
		m.jellyfinURL, seriesId)
	if userId != "" {
		epURL += fmt.Sprintf("&userId=%s", userId)
	}

	epReq, err := http.NewRequestWithContext(ctx, http.MethodGet, epURL, nil)
	if err != nil {
		return nil, err
	}
	if m.jellyfinAPIKey != "" {
		epReq.Header.Set("Authorization", fmt.Sprintf("MediaBrowser Token=\"%s\"", m.jellyfinAPIKey))
	}

	epResp, err := m.client.Do(epReq)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch episodes: %w", err)
	}
	defer epResp.Body.Close()

	var epsData struct {
		Items []struct {
			ID                string `json:"Id"`
			Name              string `json:"Name"`
			IndexNumber       int    `json:"IndexNumber"`       // Episode #
			ParentIndexNumber int    `json:"ParentIndexNumber"` // Season #
			RunTimeTicks      int64  `json:"RunTimeTicks"`
			UserData          *struct {
				PlaybackPositionTicks int64 `json:"PlaybackPositionTicks"`
				Played                bool  `json:"Played"`
			} `json:"UserData"`
			MediaSources []struct {
				ID           string `json:"Id"`
				Container    string `json:"Container"`
				MediaStreams []struct {
					Type         string `json:"Type"`
					Index        int    `json:"Index"`
					DisplayTitle string `json:"DisplayTitle"`
					Language     string `json:"Language"`
					Codec        string `json:"Codec"`
					Channels     int    `json:"Channels"`
					IsDefault    bool   `json:"IsDefault"`
				} `json:"MediaStreams"`
			} `json:"MediaSources"`
		} `json:"Items"`
	}

	if err := json.NewDecoder(epResp.Body).Decode(&epsData); err != nil {
		return nil, fmt.Errorf("failed to decode episodes: %w", err)
	}

	var episodes []EpisodeInfo
	for _, ep := range epsData.Items {
		durSec := float64(ep.RunTimeTicks) / 10000000.0
		resSec := 0.0
		played := false
		if ep.UserData != nil {
			resSec = float64(ep.UserData.PlaybackPositionTicks) / 10000000.0
			played = ep.UserData.Played
		}
		episodes = append(episodes, EpisodeInfo{
			Id:              ep.ID,
			Name:            ep.Name,
			SeasonNumber:    ep.ParentIndexNumber,
			EpisodeNumber:   ep.IndexNumber,
			DurationSeconds: durSec,
			ResumeSeconds:   resSec,
			IsPlayed:        played,
		})
	}

	// Sort episodes by season, then episode number
	sort.Slice(episodes, func(i, j int) bool {
		if episodes[i].SeasonNumber == episodes[j].SeasonNumber {
			return episodes[i].EpisodeNumber < episodes[j].EpisodeNumber
		}
		return episodes[i].SeasonNumber < episodes[j].SeasonNumber
	})

	// Find the targeted episode
	var selectedIndex = -1
	if season > 0 && episode > 0 {
		for i, ep := range episodes {
			if ep.SeasonNumber == season && ep.EpisodeNumber == episode {
				selectedIndex = i
				break
			}
		}
	}

	// If not specified or not found, find the first in-progress or unwatched episode
	if selectedIndex == -1 {
		for i, ep := range episodes {
			if ep.ResumeSeconds > 0 && !ep.IsPlayed {
				selectedIndex = i
				break
			}
		}
	}
	if selectedIndex == -1 {
		for i, ep := range episodes {
			if !ep.IsPlayed {
				selectedIndex = i
				break
			}
		}
	}
	if selectedIndex == -1 && len(episodes) > 0 {
		selectedIndex = 0
	}

	if selectedIndex == -1 || len(episodes) == 0 {
		return &PlayerInfoResponse{
			Success: false,
			Error:   "В библиотеке сериала пока не обнаружено воспроизводимых серий.",
		}, nil
	}

	curEp := episodes[selectedIndex]
	var nextEp *EpisodeInfo
	hasNext := false
	if selectedIndex+1 < len(episodes) {
		nextEp = &episodes[selectedIndex+1]
		hasNext = true
	}

	// Find the raw episode item to extract media sources and audio/subtitle tracks
	var rawEp *struct {
		ID                string `json:"Id"`
		Name              string `json:"Name"`
		IndexNumber       int    `json:"IndexNumber"`
		ParentIndexNumber int    `json:"ParentIndexNumber"`
		RunTimeTicks      int64  `json:"RunTimeTicks"`
		UserData          *struct {
			PlaybackPositionTicks int64 `json:"PlaybackPositionTicks"`
			Played                bool  `json:"Played"`
		} `json:"UserData"`
		MediaSources []struct {
			ID           string `json:"Id"`
			Container    string `json:"Container"`
			MediaStreams []struct {
				Type         string `json:"Type"`
				Index        int    `json:"Index"`
				DisplayTitle string `json:"DisplayTitle"`
				Language     string `json:"Language"`
				Codec        string `json:"Codec"`
				Channels     int    `json:"Channels"`
				IsDefault    bool   `json:"IsDefault"`
			} `json:"MediaStreams"`
		} `json:"MediaSources"`
	}

	for i := range epsData.Items {
		if epsData.Items[i].ID == curEp.Id {
			rawEp = &epsData.Items[i]
			break
		}
	}

	var audioTracks []AudioTrack
	var subtitleTracks []SubtitleTrack
	var mediaSourceId string

	if rawEp != nil && len(rawEp.MediaSources) > 0 {
		src := rawEp.MediaSources[0]
		mediaSourceId = src.ID
		for _, stream := range src.MediaStreams {
			if strings.EqualFold(stream.Type, "Audio") {
				title := stream.DisplayTitle
				if title == "" {
					title = fmt.Sprintf("Audio #%d (%s)", stream.Index, strings.ToUpper(stream.Language))
				}
				audioTracks = append(audioTracks, AudioTrack{
					Index:     stream.Index,
					Title:     title,
					Language:  stream.Language,
					Codec:     stream.Codec,
					Channels:  stream.Channels,
					IsDefault: stream.IsDefault,
				})
			} else if strings.EqualFold(stream.Type, "Subtitle") {
				title := stream.DisplayTitle
				if title == "" {
					title = fmt.Sprintf("Subtitles #%d (%s)", stream.Index, strings.ToUpper(stream.Language))
				}
				delivURL := fmt.Sprintf("/jellyfin/Videos/%s/%s/Subtitles/%d/Stream.vtt?api_key=%s",
					rawEp.ID, mediaSourceId, stream.Index, m.jellyfinAPIKey)
				subtitleTracks = append(subtitleTracks, SubtitleTrack{
					Index:       stream.Index,
					Title:       title,
					Language:    stream.Language,
					Codec:       stream.Codec,
					IsDefault:   stream.IsDefault,
					DeliveryURL: delivURL,
				})
			}
		}
	}

	streamURL := fmt.Sprintf("/jellyfin/Videos/%s/master.m3u8?MediaSourceId=%s&api_key=%s",
		curEp.Id, mediaSourceId, m.jellyfinAPIKey)

	return &PlayerInfoResponse{
		Success:         true,
		ItemId:          curEp.Id,
		Title:           fmt.Sprintf("S%02dE%02d - %s", curEp.SeasonNumber, curEp.EpisodeNumber, curEp.Name),
		MediaType:       "Episode",
		DurationSeconds: curEp.DurationSeconds,
		ResumeSeconds:   curEp.ResumeSeconds,
		IsPlayed:        curEp.IsPlayed,
		StreamURL:       streamURL,
		MediaSourceId:   mediaSourceId,
		AudioTracks:     audioTracks,
		Subtitles:       subtitleTracks,
		Episodes:        episodes,
		CurrentSeason:   curEp.SeasonNumber,
		CurrentEpisode:  curEp.EpisodeNumber,
		HasNextEpisode:  hasNext,
		NextEpisode:     nextEp,
	}, nil
}

// ReportPlaybackStart notifies Jellyfin that playback has begun
func (m *Mounter) ReportPlaybackStart(ctx context.Context, req PlaybackStartRequest) (*PlaybackActionResponse, error) {
	if req.ItemId == "" {
		return &PlaybackActionResponse{Success: false, Message: "Missing ItemId"}, nil
	}

	ticks := int64(req.PositionSeconds * 10000000.0)
	bodyMap := map[string]interface{}{
		"ItemId":              req.ItemId,
		"MediaSourceId":       req.MediaSourceId,
		"AudioStreamIndex":    req.AudioStreamIndex,
		"SubtitleStreamIndex": req.SubtitleStreamIndex,
		"PlayMethod":          "DirectStream",
		"PositionTicks":       ticks,
		"CanSeek":             true,
		"IsPaused":            false,
	}
	bodyBytes, _ := json.Marshal(bodyMap)

	reqURL := fmt.Sprintf("%s/Sessions/Playing", m.jellyfinURL)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if m.jellyfinAPIKey != "" {
		httpReq.Header.Set("Authorization", fmt.Sprintf("MediaBrowser Token=\"%s\"", m.jellyfinAPIKey))
	}

	resp, err := m.client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	return &PlaybackActionResponse{Success: true, Message: "Playback started"}, nil
}

// ReportPlaybackProgress notifies Jellyfin of playback progress and persists resume position
func (m *Mounter) ReportPlaybackProgress(ctx context.Context, req PlaybackProgressRequest) (*PlaybackActionResponse, error) {
	if req.ItemId == "" {
		return &PlaybackActionResponse{Success: false, Message: "Missing ItemId"}, nil
	}

	ticks := int64(req.PositionSeconds * 10000000.0)
	bodyMap := map[string]interface{}{
		"ItemId":        req.ItemId,
		"MediaSourceId": req.MediaSourceId,
		"PositionTicks": ticks,
		"IsPaused":      req.IsPaused,
		"EventName":     req.Event,
	}
	bodyBytes, _ := json.Marshal(bodyMap)

	// 1. Notify Sessions/Playing/Progress
	reqURL := fmt.Sprintf("%s/Sessions/Playing/Progress", m.jellyfinURL)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, bytes.NewReader(bodyBytes))
	if err == nil {
		httpReq.Header.Set("Content-Type", "application/json")
		if m.jellyfinAPIKey != "" {
			httpReq.Header.Set("Authorization", fmt.Sprintf("MediaBrowser Token=\"%s\"", m.jellyfinAPIKey))
		}
		if resp, err := m.client.Do(httpReq); err == nil {
			resp.Body.Close()
		}
	}

	// 2. Direct user progress update in Jellyfin DB
	userId, _ := m.GetAdminUserId(ctx)
	if userId != "" {
		userProgURL := fmt.Sprintf("%s/Users/%s/PlayingItems/%s/Progress?positionTicks=%d",
			m.jellyfinURL, userId, req.ItemId, ticks)
		userReq, err := http.NewRequestWithContext(ctx, http.MethodPost, userProgURL, nil)
		if err == nil {
			if m.jellyfinAPIKey != "" {
				userReq.Header.Set("Authorization", fmt.Sprintf("MediaBrowser Token=\"%s\"", m.jellyfinAPIKey))
			}
			if resp, err := m.client.Do(userReq); err == nil {
				resp.Body.Close()
			}
		}
	}

	return &PlaybackActionResponse{Success: true, Message: "Progress updated"}, nil
}

// ReportPlaybackStop notifies Jellyfin that playback has ceased, persisting final resume or played state
func (m *Mounter) ReportPlaybackStop(ctx context.Context, req PlaybackStopRequest) (*PlaybackActionResponse, error) {
	if req.ItemId == "" {
		return &PlaybackActionResponse{Success: false, Message: "Missing ItemId"}, nil
	}

	ticks := int64(req.PositionSeconds * 10000000.0)
	bodyMap := map[string]interface{}{
		"ItemId":        req.ItemId,
		"MediaSourceId": req.MediaSourceId,
		"PositionTicks": ticks,
	}
	bodyBytes, _ := json.Marshal(bodyMap)

	// 1. Notify Sessions/Playing/Stopped
	reqURL := fmt.Sprintf("%s/Sessions/Playing/Stopped", m.jellyfinURL)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, bytes.NewReader(bodyBytes))
	if err == nil {
		httpReq.Header.Set("Content-Type", "application/json")
		if m.jellyfinAPIKey != "" {
			httpReq.Header.Set("Authorization", fmt.Sprintf("MediaBrowser Token=\"%s\"", m.jellyfinAPIKey))
		}
		if resp, err := m.client.Do(httpReq); err == nil {
			resp.Body.Close()
		}
	}

	// 2. Clear active playing item and persist stopped position
	userId, _ := m.GetAdminUserId(ctx)
	if userId != "" {
		userStopURL := fmt.Sprintf("%s/Users/%s/PlayingItems/%s?positionTicks=%d",
			m.jellyfinURL, userId, req.ItemId, ticks)
		userReq, err := http.NewRequestWithContext(ctx, http.MethodDelete, userStopURL, nil)
		if err == nil {
			if m.jellyfinAPIKey != "" {
				userReq.Header.Set("Authorization", fmt.Sprintf("MediaBrowser Token=\"%s\"", m.jellyfinAPIKey))
			}
			if resp, err := m.client.Do(userReq); err == nil {
				resp.Body.Close()
			}
		}
	}

	return &PlaybackActionResponse{Success: true, Message: "Playback stopped"}, nil
}
