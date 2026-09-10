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
	"regexp"
	"sort"
	"strings"
	"time"
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
	Width           int             `json:"width,omitempty"`
	Height          int             `json:"height,omitempty"`
	Bitrate         int64           `json:"bitrate,omitempty"`
	VideoCodec      string          `json:"video_codec,omitempty"`
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
	ClosePlayer     bool    `json:"close_player,omitempty"`
	IsPlayed        bool    `json:"is_played,omitempty"`
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

type JellyfinMediaStream struct {
	Type         string `json:"Type"`
	Index        int    `json:"Index"`
	DisplayTitle string `json:"DisplayTitle"`
	Language     string `json:"Language"`
	Codec        string `json:"Codec"`
	Channels     int    `json:"Channels"`
	Width        int    `json:"Width"`
	Height       int    `json:"Height"`
	BitRate      int64  `json:"BitRate"`
	IsDefault    bool   `json:"IsDefault"`
	DeliveryURL  string `json:"DeliveryUrl,omitempty"`
}

type JellyfinMediaSource struct {
	ID           string                `json:"Id"`
	Container    string                `json:"Container"`
	Bitrate      int64                 `json:"Bitrate"`
	MediaStreams []JellyfinMediaStream `json:"MediaStreams"`
}

type JellyfinUserData struct {
	PlaybackPositionTicks int64 `json:"PlaybackPositionTicks"`
	Played                bool  `json:"Played"`
}

type JellyfinItem struct {
	ID           string                `json:"Id"`
	Name         string                `json:"Name"`
	Type         string                `json:"Type"`
	Path         string                `json:"Path"`
	SeriesId     string                `json:"SeriesId,omitempty"`
	RunTimeTicks int64                 `json:"RunTimeTicks"`
	UserData     *JellyfinUserData     `json:"UserData"`
	ProviderIds  map[string]string     `json:"ProviderIds"`
	MediaSources []JellyfinMediaSource `json:"MediaSources"`
}

type rawEpisodeStream struct {
	Type         string `json:"Type"`
	Index        int    `json:"Index"`
	DisplayTitle string `json:"DisplayTitle"`
	Language     string `json:"Language"`
	Codec        string `json:"Codec"`
	Channels     int    `json:"Channels"`
	Width        int    `json:"Width"`
	Height       int    `json:"Height"`
	BitRate      int64  `json:"BitRate"`
	IsDefault    bool   `json:"IsDefault"`
}

type rawEpisodeMediaSource struct {
	ID           string             `json:"Id"`
	Container    string             `json:"Container"`
	Bitrate      int64              `json:"Bitrate"`
	MediaStreams []rawEpisodeStream `json:"MediaStreams"`
}

type rawEpisodeItem struct {
	ID                string                  `json:"Id"`
	Name              string                  `json:"Name"`
	Path              string                  `json:"Path"`
	IndexNumber       *int                    `json:"IndexNumber"`
	ParentIndexNumber *int                    `json:"ParentIndexNumber"`
	RunTimeTicks      int64                   `json:"RunTimeTicks"`
	UserData          *JellyfinUserData       `json:"UserData"`
	MediaSources      []rawEpisodeMediaSource `json:"MediaSources"`
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

	imdbPattern := fmt.Sprintf("[imdbid-%s]", tconst)
	folderName := mountStatus.FolderName

	fetchAndMatch := func() (*JellyfinItem, error) {
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
			Items []JellyfinItem `json:"Items"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&jfResp); err != nil {
			return nil, fmt.Errorf("failed to decode Jellyfin items: %w", err)
		}

		// 1. If looking for a TV series, prioritize items with Type == "Series"
		if mountStatus.Type == "shows" {
			for i := range jfResp.Items {
				it := &jfResp.Items[i]
				if strings.EqualFold(it.Type, "Series") {
					if (folderName != "" && strings.Contains(it.Path, folderName)) ||
						strings.Contains(it.Path, imdbPattern) ||
						(it.ProviderIds != nil && strings.EqualFold(it.ProviderIds["Imdb"], tconst)) {
						return it, nil
					}
				}
			}
		} else {
			// 1. If looking for a Movie, prioritize items with Type == "Movie"
			for i := range jfResp.Items {
				it := &jfResp.Items[i]
				if strings.EqualFold(it.Type, "Movie") {
					if (folderName != "" && strings.Contains(it.Path, folderName)) ||
						strings.Contains(it.Path, imdbPattern) ||
						(it.ProviderIds != nil && strings.EqualFold(it.ProviderIds["Imdb"], tconst)) {
						return it, nil
					}
				}
			}
		}

		// 2. Fallback: match any item
		for i := range jfResp.Items {
			it := &jfResp.Items[i]
			if (folderName != "" && strings.Contains(it.Path, folderName)) ||
				strings.Contains(it.Path, imdbPattern) ||
				(it.ProviderIds != nil && strings.EqualFold(it.ProviderIds["Imdb"], tconst)) {
				return it, nil
			}
		}
		return nil, nil
	}

	matchedItem, err := fetchAndMatch()
	if err != nil {
		return nil, err
	}

	// If not found, Jellyfin may still be scanning the library folder; retry briefly
	for attempt := 0; attempt < 3 && matchedItem == nil; attempt++ {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(800 * time.Millisecond):
		}
		matchedItem, _ = fetchAndMatch()
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
		var videoWidth, videoHeight int
		var videoBitrate int64
		var videoCodec string

		if len(matchedItem.MediaSources) > 0 {
			src := matchedItem.MediaSources[0]
			mediaSourceId = src.ID
			if src.Bitrate > 0 {
				videoBitrate = src.Bitrate
			}
			for _, stream := range src.MediaStreams {
				if strings.EqualFold(stream.Type, "Video") {
					videoWidth = stream.Width
					videoHeight = stream.Height
					videoCodec = stream.Codec
					if stream.BitRate > 0 {
						videoBitrate = stream.BitRate
					}
				} else if strings.EqualFold(stream.Type, "Audio") {
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

		// Jellyfin HLS master playlist URL with standard web audio/video codecs, fMP4 container and stream-copy
		streamURL := fmt.Sprintf("/jellyfin/Videos/%s/master.m3u8?MediaSourceId=%s&VideoCodec=h264&AudioCodec=aac&TranscodingMaxAudioChannels=2&SegmentContainer=mp4&MinSegments=2&BreakOnNonKeyFrames=True&EnableAutoStreamCopy=true&VideoBitRate=35000000&AudioBitRate=384000&api_key=%s",
			matchedItem.ID, mediaSourceId, m.jellyfinAPIKey)

		ruTitle := ""
		if containsCyrillic(matchedItem.Name) {
			ruTitle = matchedItem.Name
		} else if mountStatus.FolderName != "" {
			parts := strings.Split(mountStatus.FolderName, " (")
			if len(parts) > 0 && containsCyrillic(parts[0]) {
				ruTitle = strings.TrimSpace(parts[0])
			}
		}

		return &PlayerInfoResponse{
			Success:         true,
			ItemId:          matchedItem.ID,
			Title:           matchedItem.Name,
			RuTitle:         ruTitle,
			MediaType:       "Movie",
			DurationSeconds: durationSec,
			ResumeSeconds:   resumeSec,
			IsPlayed:        isPlayed,
			StreamURL:       streamURL,
			MediaSourceId:   mediaSourceId,
			AudioTracks:     audioTracks,
			Subtitles:       subtitleTracks,
			Width:           videoWidth,
			Height:          videoHeight,
			Bitrate:         videoBitrate,
			VideoCodec:      videoCodec,
		}, nil
	}

	// TV Series: fetch all episodes from Jellyfin
	seriesId := matchedItem.ID
	if matchedItem.SeriesId != "" {
		seriesId = matchedItem.SeriesId
	}

	epURL := fmt.Sprintf("%s/Shows/%s/Episodes?fields=Path,MediaSources,UserData,RunTimeTicks,Overview,IndexNumber,ParentIndexNumber",
		m.jellyfinURL, seriesId)
	if userId != "" {
		epURL += fmt.Sprintf("&userId=%s", userId)
	}

	fetchEpisodes := func() ([]rawEpisodeItem, error) {
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

		if epResp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("jellyfin returned status %d for episodes", epResp.StatusCode)
		}

		var epsData struct {
			Items []rawEpisodeItem `json:"Items"`
		}
		if err := json.NewDecoder(epResp.Body).Decode(&epsData); err != nil {
			return nil, fmt.Errorf("failed to decode episodes: %w", err)
		}
		return epsData.Items, nil
	}

	rawEpisodeItems, err := fetchEpisodes()
	if err != nil {
		return nil, err
	}

	// If 0 episodes found, trigger targeted recursive refresh on series and retry up to 4 times
	if len(rawEpisodeItems) == 0 {
		refreshURL := fmt.Sprintf("%s/Items/%s/Refresh?Recursive=true", m.jellyfinURL, seriesId)
		if refReq, err := http.NewRequestWithContext(ctx, http.MethodPost, refreshURL, nil); err == nil {
			if m.jellyfinAPIKey != "" {
				refReq.Header.Set("Authorization", fmt.Sprintf("MediaBrowser Token=\"%s\"", m.jellyfinAPIKey))
			}
			if resp, err := m.client.Do(refReq); err == nil {
				resp.Body.Close()
			}
		}

		for attempt := 0; attempt < 4 && len(rawEpisodeItems) == 0; attempt++ {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(1000 * time.Millisecond):
			}
			rawEpisodeItems, _ = fetchEpisodes()
		}
	}

	episodesMeta := m.fetchEpisodesMetadata(ctx, tconst)

	var episodes []EpisodeInfo
	for _, ep := range rawEpisodeItems {
		durSec := float64(ep.RunTimeTicks) / 10000000.0
		resSec := 0.0
		played := false
		if ep.UserData != nil {
			resSec = float64(ep.UserData.PlaybackPositionTicks) / 10000000.0
			played = ep.UserData.Played
		}

		sNum := 0
		if ep.ParentIndexNumber != nil && *ep.ParentIndexNumber > 0 {
			sNum = *ep.ParentIndexNumber
		}
		epNum := 0
		if ep.IndexNumber != nil && *ep.IndexNumber > 0 {
			epNum = *ep.IndexNumber
		}

		// Fallback: extract season & episode from Path or Name if Jellyfin didn't provide them
		if sNum == 0 || epNum == 0 {
			ps, pe := parseSeasonEpisode(ep.Path)
			if ps == 0 && pe == 0 {
				ps, pe = parseSeasonEpisode(ep.Name)
			}
			if sNum == 0 {
				sNum = ps
			}
			if epNum == 0 {
				epNum = pe
			}
		}

		// Episode name: prioritize TMDB metadata in Russian
		epName := strings.TrimSpace(ep.Name)
		if sMap, ok := episodesMeta[sNum]; ok {
			if meta, ok := sMap[epNum]; ok && meta.Name != "" {
				epName = meta.Name
			}
		}
		// If epName is still raw filename (like "Укрытие - S02E01" or empty), clean it
		if epName == "" || strings.Contains(epName, " - S") || strings.HasPrefix(strings.ToLower(epName), "s0") {
			if sMap, ok := episodesMeta[sNum]; ok {
				if meta, ok := sMap[epNum]; ok && meta.Name != "" {
					epName = meta.Name
				} else {
					epName = fmt.Sprintf("Серия %d", epNum)
				}
			} else {
				epName = fmt.Sprintf("Серия %d", epNum)
			}
		}

		episodes = append(episodes, EpisodeInfo{
			Id:              ep.ID,
			Name:            epName,
			SeasonNumber:    sNum,
			EpisodeNumber:   epNum,
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

	// If a specific season was requested, check if episodes for this season exist
	if season > 0 {
		var seasonEpisodesCount = 0
		for _, ep := range episodes {
			if ep.SeasonNumber == season {
				seasonEpisodesCount++
			}
		}

		if seasonEpisodesCount == 0 {
			return &PlayerInfoResponse{
				Success: false,
				Error:   fmt.Sprintf("Сезон %d еще сканируется или не смонтирован в Jellyfin. Подождите пару секунд.", season),
			}, nil
		}

		// Find targeted episode within this season
		if episode > 0 {
			for i, ep := range episodes {
				if ep.SeasonNumber == season && ep.EpisodeNumber == episode {
					selectedIndex = i
					break
				}
			}
		}

		// If specific episode wasn't found (or episode == 0), find first in-progress or unwatched IN THIS SEASON
		if selectedIndex == -1 {
			for i, ep := range episodes {
				if ep.SeasonNumber == season && ep.ResumeSeconds > 0 && !ep.IsPlayed {
					selectedIndex = i
					break
				}
			}
		}
		if selectedIndex == -1 {
			for i, ep := range episodes {
				if ep.SeasonNumber == season && !ep.IsPlayed {
					selectedIndex = i
					break
				}
			}
		}
		if selectedIndex == -1 {
			for i, ep := range episodes {
				if ep.SeasonNumber == season {
					selectedIndex = i
					break
				}
			}
		}
	} else {
		// No specific season requested: find first in-progress or unwatched across all seasons
		for i, ep := range episodes {
			if ep.ResumeSeconds > 0 && !ep.IsPlayed {
				selectedIndex = i
				break
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
	}

	if selectedIndex == -1 || len(episodes) == 0 {
		return &PlayerInfoResponse{
			Success: false,
			Error:   "Сериал смонтирован, но Jellyfin еще сканирует серии. Подождите пару секунд и повторите.",
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
	var rawEp *rawEpisodeItem
	for i := range rawEpisodeItems {
		if rawEpisodeItems[i].ID == curEp.Id {
			rawEp = &rawEpisodeItems[i]
			break
		}
	}

	var audioTracks []AudioTrack
	var subtitleTracks []SubtitleTrack
	var mediaSourceId string
	var videoWidth, videoHeight int
	var videoBitrate int64
	var videoCodec string

	if rawEp != nil && len(rawEp.MediaSources) > 0 {
		src := rawEp.MediaSources[0]
		mediaSourceId = src.ID
		if src.Bitrate > 0 {
			videoBitrate = src.Bitrate
		}
		for _, stream := range src.MediaStreams {
			if strings.EqualFold(stream.Type, "Video") {
				videoWidth = stream.Width
				videoHeight = stream.Height
				videoCodec = stream.Codec
				if stream.BitRate > 0 {
					videoBitrate = stream.BitRate
				}
			} else if strings.EqualFold(stream.Type, "Audio") {
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

	streamURL := fmt.Sprintf("/jellyfin/Videos/%s/master.m3u8?MediaSourceId=%s&VideoCodec=h264&AudioCodec=aac&TranscodingMaxAudioChannels=2&SegmentContainer=mp4&MinSegments=2&BreakOnNonKeyFrames=True&EnableAutoStreamCopy=true&VideoBitRate=35000000&AudioBitRate=384000&api_key=%s",
		curEp.Id, mediaSourceId, m.jellyfinAPIKey)

	// Format natural Russian title for the episode: "Сезон 2, серия 3 — Соло"
	var displayTitle string
	cleanEpName := strings.TrimSpace(curEp.Name)
	if cleanEpName != "" && !strings.EqualFold(cleanEpName, fmt.Sprintf("Серия %d", curEp.EpisodeNumber)) {
		displayTitle = fmt.Sprintf("Сезон %d, серия %d — %s", curEp.SeasonNumber, curEp.EpisodeNumber, cleanEpName)
	} else {
		displayTitle = fmt.Sprintf("Сезон %d, серия %d", curEp.SeasonNumber, curEp.EpisodeNumber)
	}

	ruTitle := ""
	if containsCyrillic(matchedItem.Name) {
		ruTitle = matchedItem.Name
	} else if mountStatus.FolderName != "" {
		parts := strings.Split(mountStatus.FolderName, " (")
		if len(parts) > 0 && containsCyrillic(parts[0]) {
			ruTitle = strings.TrimSpace(parts[0])
		}
	}

	return &PlayerInfoResponse{
		Success:         true,
		ItemId:          curEp.Id,
		Title:           displayTitle,
		RuTitle:         ruTitle,
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
		Width:           videoWidth,
		Height:          videoHeight,
		Bitrate:         videoBitrate,
		VideoCodec:      videoCodec,
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

	// 2. Direct user progress update in Jellyfin DB and PlayingItems state
	userId, _ := m.GetAdminUserId(ctx)
	if userId != "" {
		// Update persistent UserData ticks in Jellyfin DB
		userDataURL := fmt.Sprintf("%s/UserItems/%s/UserData?userId=%s", m.jellyfinURL, req.ItemId, userId)
		dataMap := map[string]interface{}{
			"PlaybackPositionTicks": ticks,
			"Played":                false,
		}
		dataBytes, _ := json.Marshal(dataMap)
		userReq, err := http.NewRequestWithContext(ctx, http.MethodPost, userDataURL, bytes.NewReader(dataBytes))
		if err == nil {
			userReq.Header.Set("Content-Type", "application/json")
			if m.jellyfinAPIKey != "" {
				userReq.Header.Set("Authorization", fmt.Sprintf("MediaBrowser Token=\"%s\"", m.jellyfinAPIKey))
			}
			if resp, err := m.client.Do(userReq); err == nil {
				resp.Body.Close()
			}
		}

		// Also notify active PlayingItems progress
		itemProgURL := fmt.Sprintf("%s/PlayingItems/%s/Progress?positionTicks=%d", m.jellyfinURL, req.ItemId, ticks)
		itemReq, err := http.NewRequestWithContext(ctx, http.MethodPost, itemProgURL, nil)
		if err == nil {
			if m.jellyfinAPIKey != "" {
				itemReq.Header.Set("Authorization", fmt.Sprintf("MediaBrowser Token=\"%s\"", m.jellyfinAPIKey))
			}
			if resp, err := m.client.Do(itemReq); err == nil {
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

	// 2. Persist stopped position to UserData or mark played, and clear active playing item
	userId, _ := m.GetAdminUserId(ctx)
	if userId != "" {
		if req.IsPlayed {
			// Mark item as played in Jellyfin
			playedURL := fmt.Sprintf("%s/UserPlayedItems/%s?userId=%s", m.jellyfinURL, req.ItemId, userId)
			playReq, err := http.NewRequestWithContext(ctx, http.MethodPost, playedURL, nil)
			if err == nil {
				if m.jellyfinAPIKey != "" {
					playReq.Header.Set("Authorization", fmt.Sprintf("MediaBrowser Token=\"%s\"", m.jellyfinAPIKey))
				}
				if resp, err := m.client.Do(playReq); err == nil {
					resp.Body.Close()
				}
			}
		} else if ticks > 50000000 {
			// Save stopped position in UserData only if > 5 seconds watched to prevent wiping existing progress
			userDataURL := fmt.Sprintf("%s/UserItems/%s/UserData?userId=%s", m.jellyfinURL, req.ItemId, userId)
			dataMap := map[string]interface{}{
				"PlaybackPositionTicks": ticks,
				"Played":                false,
			}
			dataBytes, _ := json.Marshal(dataMap)
			userReq, err := http.NewRequestWithContext(ctx, http.MethodPost, userDataURL, bytes.NewReader(dataBytes))
			if err == nil {
				userReq.Header.Set("Content-Type", "application/json")
				if m.jellyfinAPIKey != "" {
					userReq.Header.Set("Authorization", fmt.Sprintf("MediaBrowser Token=\"%s\"", m.jellyfinAPIKey))
				}
				if resp, err := m.client.Do(userReq); err == nil {
					resp.Body.Close()
				}
			}
		}

		// Clear active playing item in Jellyfin
		playClearURL := fmt.Sprintf("%s/PlayingItems/%s?positionTicks=%d", m.jellyfinURL, req.ItemId, ticks)
		clearReq, err := http.NewRequestWithContext(ctx, http.MethodDelete, playClearURL, nil)
		if err == nil {
			if m.jellyfinAPIKey != "" {
				clearReq.Header.Set("Authorization", fmt.Sprintf("MediaBrowser Token=\"%s\"", m.jellyfinAPIKey))
			}
			if resp, err := m.client.Do(clearReq); err == nil {
				resp.Body.Close()
			}
		}
	}

	// 3. Drop active torrent from memory in GoStorm only when player is completely closed
	if req.ClosePlayer {
		go func() {
			bgCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			m.DropAllWorkingTorrents(bgCtx)
		}()
	}

	return &PlaybackActionResponse{Success: true, Message: "Playback stopped"}, nil
}

// DropAllWorkingTorrents sends an action: "drop" command to GoStorm for any torrent currently in "Torrent working" state (stat: 3)
// This immediately frees memory and terminates active BitTorrent peer downloads/uploads.
// When the file is next read by Jellyfin, Tiramisu FUSE will transparently re-open it.
func (m *Mounter) DropAllWorkingTorrents(ctx context.Context) {
	if m.gostormURL == "" {
		return
	}
	listBody, _ := json.Marshal(map[string]string{"action": "list"})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.gostormURL+"/torrents", bytes.NewReader(listBody))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := m.client.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()

	var torrents []struct {
		Hash  string `json:"hash"`
		Title string `json:"title"`
		Stat  int    `json:"stat"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&torrents); err != nil {
		return
	}

	for _, t := range torrents {
		if t.Stat == 3 && t.Hash != "" { // Stat 3 = Torrent working
			dropBody, _ := json.Marshal(map[string]string{"action": "drop", "hash": t.Hash})
			if dReq, err := http.NewRequestWithContext(ctx, http.MethodPost, m.gostormURL+"/torrents", bytes.NewReader(dropBody)); err == nil {
				dReq.Header.Set("Content-Type", "application/json")
				if dResp, err := m.client.Do(dReq); err == nil {
					dResp.Body.Close()
					log.Printf("[player] Dropped active torrent '%s' (%s) from GoStorm memory to stop idle swarm traffic", t.Title, t.Hash)
				}
			}
		}
	}
}

// StartTorrentIdleReaper periodically monitors Jellyfin playback sessions.
// If no sessions are actively streaming, it unloads active torrents from GoStorm memory.
func (m *Mounter) StartTorrentIdleReaper(ctx context.Context, interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				sessURL := fmt.Sprintf("%s/Sessions", m.jellyfinURL)
				sReq, err := http.NewRequestWithContext(ctx, http.MethodGet, sessURL, nil)
				if err != nil {
					continue
				}
				if m.jellyfinAPIKey != "" {
					sReq.Header.Set("Authorization", fmt.Sprintf("MediaBrowser Token=\"%s\"", m.jellyfinAPIKey))
				}
				sResp, err := m.client.Do(sReq)
				if err != nil {
					continue
				}

				var sessions []struct {
					NowPlayingItem *struct {
						Id string `json:"Id"`
					} `json:"NowPlayingItem"`
				}
				decodeErr := json.NewDecoder(sResp.Body).Decode(&sessions)
				sResp.Body.Close()
				if decodeErr != nil {
					continue
				}

				hasActivePlayback := false
				for _, s := range sessions {
					if s.NowPlayingItem != nil && s.NowPlayingItem.Id != "" {
						hasActivePlayback = true
						break
					}
				}

				if !hasActivePlayback {
					m.lastMountMu.RLock()
					lastMount := m.lastMountTime
					m.lastMountMu.RUnlock()

					// Do not drop torrents if a mount happened in the last 3 minutes
					if !lastMount.IsZero() && time.Since(lastMount) < 3*time.Minute {
						continue
					}

					m.DropAllWorkingTorrents(ctx)
				}
			}
		}
	}()
}

// ResumeItem represents an in-progress or next-up video item for the home screen shelf
type ResumeItem struct {
	ItemId           string  `json:"item_id"`
	Tconst           string  `json:"tconst,omitempty"`
	Title            string  `json:"title"`
	SeriesName       string  `json:"series_name,omitempty"`
	EpisodeTitle     string  `json:"episode_title,omitempty"`
	MediaType        string  `json:"media_type"` // "Movie" or "Episode"
	SeasonNumber     int     `json:"season_number,omitempty"`
	EpisodeNumber    int     `json:"episode_number,omitempty"`
	DurationSeconds  float64 `json:"duration_seconds"`
	ResumeSeconds    float64 `json:"resume_seconds"`
	PlayedPercentage float64 `json:"played_percentage"`
	ImageUrl         string  `json:"image_url"`
	IsNextUp         bool    `json:"is_next_up,omitempty"`
}

// GetResumeItems retrieves in-progress movies and episodes, and next-up episodes from Jellyfin.
// Enforces that only the latest single episode per series is displayed.
func (m *Mounter) GetResumeItems(ctx context.Context) ([]ResumeItem, error) {
	userId, err := m.GetAdminUserId(ctx)
	if err != nil || userId == "" {
		return nil, fmt.Errorf("failed to get admin user id: %w", err)
	}

	imdbRegex := regexp.MustCompile(`\[imdbid-(tt\d+)\]`)

	// 1. Fetch in-progress items from /UserItems/Resume
	reqURL := fmt.Sprintf("%s/UserItems/Resume?userId=%s&fields=ProviderIds,Overview,SeriesId,SeriesName,Path", m.jellyfinURL, userId)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, err
	}
	if m.jellyfinAPIKey != "" {
		httpReq.Header.Set("Authorization", fmt.Sprintf("MediaBrowser Token=\"%s\"", m.jellyfinAPIKey))
	}

	resp, err := m.client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var resumeResp struct {
		Items []struct {
			Id                string `json:"Id"`
			Name              string `json:"Name"`
			Type              string `json:"Type"`
			SeriesId          string `json:"SeriesId"`
			SeriesName        string `json:"SeriesName"`
			Path              string `json:"Path"`
			IndexNumber       *int   `json:"IndexNumber"`
			ParentIndexNumber *int   `json:"ParentIndexNumber"`
			RunTimeTicks      int64  `json:"RunTimeTicks"`
			UserData          struct {
				PlayedPercentage      float64 `json:"PlayedPercentage"`
				PlaybackPositionTicks int64   `json:"PlaybackPositionTicks"`
				Played                bool    `json:"Played"`
			} `json:"UserData"`
			ProviderIds map[string]string `json:"ProviderIds"`
		} `json:"Items"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&resumeResp); err != nil {
		return nil, err
	}

	var movies []ResumeItem
	seriesInProgressMap := make(map[string]ResumeItem)
	seriesScoreMap := make(map[string]int)

	for _, it := range resumeResp.Items {
		if it.UserData.Played {
			continue
		}

		durationSec := float64(it.RunTimeTicks) / 10000000.0
		resumeSec := float64(it.UserData.PlaybackPositionTicks) / 10000000.0
		if resumeSec <= 0 {
			continue
		}

		tconst := ""
		if it.ProviderIds != nil {
			tconst = it.ProviderIds["Imdb"]
		}
		if tconst == "" && it.Path != "" {
			if matches := imdbRegex.FindStringSubmatch(it.Path); len(matches) > 1 {
				tconst = matches[1]
			}
		}

		mediaType := it.Type
		title := it.Name
		seriesName := it.SeriesName
		episodeTitle := ""
		seasonNum := 0
		episodeNum := 0

		if mediaType == "Episode" {
			episodeTitle = it.Name
			if it.SeriesName != "" {
				title = it.SeriesName
			}
			if it.IndexNumber != nil {
				episodeNum = *it.IndexNumber
			}
			if it.ParentIndexNumber != nil {
				seasonNum = *it.ParentIndexNumber
			}

			if tconst == "" && it.SeriesId != "" {
				sURL := fmt.Sprintf("%s/Users/%s/Items/%s", m.jellyfinURL, userId, it.SeriesId)
				if sReq, sErr := http.NewRequestWithContext(ctx, http.MethodGet, sURL, nil); sErr == nil {
					if m.jellyfinAPIKey != "" {
						sReq.Header.Set("Authorization", fmt.Sprintf("MediaBrowser Token=\"%s\"", m.jellyfinAPIKey))
					}
					if sResp, sDoErr := m.client.Do(sReq); sDoErr == nil {
						var sItem struct {
							ProviderIds map[string]string `json:"ProviderIds"`
						}
						_ = json.NewDecoder(sResp.Body).Decode(&sItem)
						sResp.Body.Close()
						if sItem.ProviderIds != nil {
							tconst = sItem.ProviderIds["Imdb"]
						}
					}
				}
			}
		}

		imgUrl := fmt.Sprintf("/jellyfin/Items/%s/Images/Primary", it.Id)

		item := ResumeItem{
			ItemId:           it.Id,
			Tconst:           tconst,
			Title:            title,
			SeriesName:       seriesName,
			EpisodeTitle:     episodeTitle,
			MediaType:        mediaType,
			SeasonNumber:     seasonNum,
			EpisodeNumber:    episodeNum,
			DurationSeconds:  durationSec,
			ResumeSeconds:    resumeSec,
			PlayedPercentage: it.UserData.PlayedPercentage,
			ImageUrl:         imgUrl,
			IsNextUp:         false,
		}

		if mediaType == "Movie" {
			movies = append(movies, item)
		} else {
			seriesKey := it.SeriesId
			if seriesKey == "" {
				seriesKey = tconst
			}
			if seriesKey == "" {
				seriesKey = seriesName
			}
			score := seasonNum*1000 + episodeNum
			if prevScore, exists := seriesScoreMap[seriesKey]; !exists || score >= prevScore {
				seriesInProgressMap[seriesKey] = item
				seriesScoreMap[seriesKey] = score
			}
		}
	}

	// 2. Fetch Next Up episodes from /Shows/NextUp
	var nextUpItems []ResumeItem
	nextUpURL := fmt.Sprintf("%s/Shows/NextUp?userId=%s&fields=ProviderIds,Overview,SeriesId,SeriesName,Path", m.jellyfinURL, userId)
	if nReq, nErr := http.NewRequestWithContext(ctx, http.MethodGet, nextUpURL, nil); nErr == nil {
		if m.jellyfinAPIKey != "" {
			nReq.Header.Set("Authorization", fmt.Sprintf("MediaBrowser Token=\"%s\"", m.jellyfinAPIKey))
		}
		if nResp, nDoErr := m.client.Do(nReq); nDoErr == nil {
			var nextResp struct {
				Items []struct {
					Id                string `json:"Id"`
					Name              string `json:"Name"`
					Type              string `json:"Type"`
					SeriesId          string `json:"SeriesId"`
					SeriesName        string `json:"SeriesName"`
					Path              string `json:"Path"`
					IndexNumber       *int   `json:"IndexNumber"`
					ParentIndexNumber *int   `json:"ParentIndexNumber"`
					RunTimeTicks      int64  `json:"RunTimeTicks"`
					UserData          struct {
						Played bool `json:"Played"`
					} `json:"UserData"`
					ProviderIds map[string]string `json:"ProviderIds"`
				} `json:"Items"`
			}
			if err := json.NewDecoder(nResp.Body).Decode(&nextResp); err == nil {
				for _, it := range nextResp.Items {
					if it.UserData.Played {
						continue
					}

					tconst := ""
					if it.ProviderIds != nil {
						tconst = it.ProviderIds["Imdb"]
					}
					if tconst == "" && it.Path != "" {
						if matches := imdbRegex.FindStringSubmatch(it.Path); len(matches) > 1 {
							tconst = matches[1]
						}
					}

					seriesKey := it.SeriesId
					if seriesKey == "" {
						seriesKey = tconst
					}
					if seriesKey == "" {
						seriesKey = it.SeriesName
					}

					// Only show NextUp if the series does not already have an active in-progress episode
					if _, inProgress := seriesInProgressMap[seriesKey]; inProgress {
						continue
					}

					durationSec := float64(it.RunTimeTicks) / 10000000.0
					seasonNum := 0
					episodeNum := 0
					if it.IndexNumber != nil {
						episodeNum = *it.IndexNumber
					}
					if it.ParentIndexNumber != nil {
						seasonNum = *it.ParentIndexNumber
					}

					if tconst == "" && it.SeriesId != "" {
						sURL := fmt.Sprintf("%s/Users/%s/Items/%s", m.jellyfinURL, userId, it.SeriesId)
						if sReq, sErr := http.NewRequestWithContext(ctx, http.MethodGet, sURL, nil); sErr == nil {
							if m.jellyfinAPIKey != "" {
								sReq.Header.Set("Authorization", fmt.Sprintf("MediaBrowser Token=\"%s\"", m.jellyfinAPIKey))
							}
							if sResp, sDoErr := m.client.Do(sReq); sDoErr == nil {
								var sItem struct {
									ProviderIds map[string]string `json:"ProviderIds"`
								}
								_ = json.NewDecoder(sResp.Body).Decode(&sItem)
								sResp.Body.Close()
								if sItem.ProviderIds != nil {
									tconst = sItem.ProviderIds["Imdb"]
								}
							}
						}
					}

					title := it.Name
					if it.SeriesName != "" {
						title = it.SeriesName
					}

					imgUrl := fmt.Sprintf("/jellyfin/Items/%s/Images/Primary", it.Id)

					nextUpItems = append(nextUpItems, ResumeItem{
						ItemId:           it.Id,
						Tconst:           tconst,
						Title:            title,
						SeriesName:       it.SeriesName,
						EpisodeTitle:     it.Name,
						MediaType:        "Episode",
						SeasonNumber:     seasonNum,
						EpisodeNumber:    episodeNum,
						DurationSeconds:  durationSec,
						ResumeSeconds:    0,
						PlayedPercentage: 0,
						ImageUrl:         imgUrl,
						IsNextUp:         true,
					})
				}
			}
			nResp.Body.Close()
		}
	}

	// 3. Assemble unified results: movies first, then in-progress series episodes, then next up episodes
	var results []ResumeItem
	results = append(results, movies...)
	for _, ep := range seriesInProgressMap {
		results = append(results, ep)
	}
	results = append(results, nextUpItems...)

	return results, nil
}
