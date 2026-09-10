package stream

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"tracker-proxy/pkg/playback"
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

type MountRequest struct {
	Tconst      string `json:"tconst"`
	Title       string `json:"title"`
	RuTitle     string `json:"ru_title,omitempty"`
	Year        string `json:"year,omitempty"`
	Type        string `json:"type,omitempty"` // "movie" or "tvSeries"
	Magnet      string `json:"magnet,omitempty"`
	Season      int    `json:"season,omitempty"`
	Tracker     string `json:"tracker,omitempty"`
	TorrentID   string `json:"torrent_id,omitempty"`
	DetailsURL  string `json:"details_url,omitempty"`
	Mode        string `json:"mode,omitempty"`
	VersionName string `json:"version_name,omitempty"`
	Resolution  string `json:"resolution,omitempty"`
	FolderName  string `json:"folder_name,omitempty"`
}

type MountResponse struct {
	Success      bool     `json:"success"`
	Message      string   `json:"message"`
	JellyfinURL  string   `json:"jellyfin_url,omitempty"`
	MountedFiles []string `json:"mounted_files"`
}

type UnmountRequest struct {
	Tconst     string `json:"tconst,omitempty"`
	Type       string `json:"type,omitempty"`
	FolderName string `json:"folder_name,omitempty"`
}

type UnmountResponse struct {
	Success       bool     `json:"success"`
	Message       string   `json:"message"`
	RemovedFiles  []string `json:"removed_files,omitempty"`
	RemovedSwarms []string `json:"removed_swarms,omitempty"`
}

type MountedStatusResponse struct {
	Mounted      bool     `json:"mounted"`
	Tconst       string   `json:"tconst,omitempty"`
	Type         string   `json:"type,omitempty"`
	FolderName   string   `json:"folder_name,omitempty"`
	LibraryPath  string   `json:"library_path,omitempty"`
	SourcePath   string   `json:"source_path,omitempty"`
	MountedFiles []string `json:"mounted_files,omitempty"`
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
	DurationSeconds float64 `json:"duration_seconds,omitempty"`
	IsPaused        bool    `json:"is_paused"`
	Event           string  `json:"event,omitempty"`
}

type PlaybackStopRequest struct {
	ItemId          string  `json:"item_id"`
	MediaSourceId   string  `json:"media_source_id,omitempty"`
	PositionSeconds float64 `json:"position_seconds"`
	DurationSeconds float64 `json:"duration_seconds,omitempty"`
	ClosePlayer     bool    `json:"close_player,omitempty"`
	IsPlayed        bool    `json:"is_played,omitempty"`
}

type PlaybackActionResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message,omitempty"`
}

type ResumeItem struct {
	ItemId           string  `json:"item_id"`
	Tconst           string  `json:"tconst,omitempty"`
	Title            string  `json:"title"`
	SeriesName       string  `json:"series_name,omitempty"`
	EpisodeTitle     string  `json:"episode_title,omitempty"`
	MediaType        string  `json:"media_type"`
	SeasonNumber     int     `json:"season_number,omitempty"`
	EpisodeNumber    int     `json:"episode_number,omitempty"`
	DurationSeconds  float64 `json:"duration_seconds"`
	ResumeSeconds    float64 `json:"resume_seconds"`
	PlayedPercentage float64 `json:"played_percentage"`
	ImageUrl         string  `json:"image_url"`
	IsNextUp         bool    `json:"is_next_up,omitempty"`
}

type JellyfinDeletedWebhook struct {
	NotificationType string `json:"NotificationType"`
	ItemId           string `json:"ItemId"`
	Name             string `json:"Name"`
	ItemType         string `json:"ItemType"`
	Path             string `json:"Path"`
}

type MountedTorrentInfo struct {
	Tconst     string
	Title      string
	RuTitle    string
	Type       string
	Hash       string
	Magnet     string
	FileIndex  int
	TargetFile string
	MountedAt  time.Time
}

// TorrStreamService manages native streaming from TorrServer MatriX and tracks watch progress
type TorrStreamService struct {
	torrClient    *TorrClient
	playbackStore *playback.Store
	nextUpService *playback.NextUpService
	indexerURL    string
	torrServerURL string

	mu              sync.RWMutex
	mountedTorrents map[string]*MountedTorrentInfo
	probeCache      map[string]*ProbeInfo
}

// Alias Mounter for backwards-compatibility with api.Handler
type Mounter = TorrStreamService

func NewTorrStreamService(torrURL, indexerURL string, store *playback.Store, nextUp *playback.NextUpService) *TorrStreamService {
	if torrURL == "" {
		torrURL = "http://127.0.0.1:8092"
	}
	if indexerURL == "" {
		indexerURL = "http://127.0.0.1:8090"
	}

	return &TorrStreamService{
		torrClient:      NewTorrClient(torrURL),
		playbackStore:   store,
		nextUpService:   nextUp,
		indexerURL:      indexerURL,
		torrServerURL:   torrURL,
		mountedTorrents: make(map[string]*MountedTorrentInfo),
		probeCache:      make(map[string]*ProbeInfo),
	}
}

type ffprobeStreamTag struct {
	Language string `json:"language"`
	Title    string `json:"title"`
}

type ffprobeStream struct {
	Index     int              `json:"index"`
	CodecName string           `json:"codec_name"`
	CodecType string           `json:"codec_type"`
	Channels  int              `json:"channels"`
	Width     int              `json:"width"`
	Height    int              `json:"height"`
	Tags      ffprobeStreamTag `json:"tags"`
}

type ffprobeOutput struct {
	Streams []ffprobeStream `json:"streams"`
	Format  struct {
		Duration string `json:"duration"`
	} `json:"format"`
}

func (s *TorrStreamService) probeStream(ctx context.Context, hash string, fileId int) *ProbeInfo {
	cacheKey := fmt.Sprintf("%s:%d", hash, fileId)
	s.mu.RLock()
	cached := s.probeCache[cacheKey]
	s.mu.RUnlock()
	if cached != nil {
		return cached
	}

	ffprobePath, err := exec.LookPath("ffprobe")
	if err != nil {
		return nil
	}

	torrURL := fmt.Sprintf("%s/stream?link=%s&index=%d&play", s.torrServerURL, hash, fileId)
	probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	cmd := exec.CommandContext(probeCtx, ffprobePath,
		"-v", "error",
		"-show_entries", "stream=index,codec_name,codec_type,channels,width,height:stream_tags=language,title:format=duration",
		"-of", "json",
		torrURL,
	)

	out, err := cmd.Output()
	if err != nil {
		return nil
	}

	var data ffprobeOutput
	if err := json.Unmarshal(out, &data); err != nil {
		return nil
	}

	var durSec float64
	if data.Format.Duration != "" {
		durSec, _ = strconv.ParseFloat(data.Format.Duration, 64)
	}

	info := &ProbeInfo{
		DurationNS: int64(durSec * 1e9),
	}

	for _, st := range data.Streams {
		track := ProbeTrack{
			Index:    st.Index,
			Type:     st.CodecType,
			Codec:    st.CodecName,
			Channels: st.Channels,
			Width:    st.Width,
			Height:   st.Height,
			Language: st.Tags.Language,
			Title:    st.Tags.Title,
		}
		info.Tracks = append(info.Tracks, track)
	}

	s.mu.Lock()
	if s.probeCache == nil {
		s.probeCache = make(map[string]*ProbeInfo)
	}
	s.probeCache[cacheKey] = info
	s.mu.Unlock()

	return info
}

func (s *TorrStreamService) GetServiceURLs() (string, string, string) {
	return s.torrServerURL, "", s.indexerURL
}

func (s *TorrStreamService) StartReconciler(ctx context.Context, interval time.Duration) {}
func (s *TorrStreamService) StartTorrentIdleReaper(ctx context.Context, interval time.Duration) {}

// MountTorrent adds the torrent to TorrServer and prepares it for instant playback
func (s *TorrStreamService) MountTorrent(ctx context.Context, req MountRequest) (*MountResponse, error) {
	log.Printf("[stream] MountTorrent requested: title=%q tconst=%q magnet=%s season=%d", req.Title, req.Tconst, req.Magnet != "", req.Season)

	if req.Magnet == "" {
		return &MountResponse{
			Success: false,
			Message: "Отсутствует magnet-ссылка",
		}, fmt.Errorf("magnet link is required")
	}

	hash := ExtractHashFromMagnet(req.Magnet)
	if hash == "" {
		return &MountResponse{
			Success: false,
			Message: "Некорректная magnet-ссылка (отсутствует infohash)",
		}, fmt.Errorf("invalid magnet link")
	}

	// Add torrent to TorrServer
	title := req.Title
	if req.RuTitle != "" {
		title = req.RuTitle + " / " + req.Title
	}

	rec, err := s.torrClient.AddTorrent(ctx, req.Magnet, title)
	if err != nil {
		log.Printf("[stream] Warning: AddTorrent error: %v", err)
	}

	// Wait up to 3 seconds for metadata
	if rec == nil || len(rec.FileStats) == 0 {
		rec, _ = s.torrClient.WaitMetadata(ctx, hash, 3*time.Second)
	}

	targetFileIdx := 0
	targetFilePath := ""
	if rec != nil && len(rec.FileStats) > 0 {
		targetEp := 1
		if req.Season == 0 {
			targetEp = 0
		}
		matched, matchErr := s.torrClient.MatchFile(rec, req.Season, targetEp)
		if matchErr == nil && matched != nil {
			targetFileIdx = matched.ID
			targetFilePath = matched.Path
		}
	}

	// Record in memory
	info := &MountedTorrentInfo{
		Tconst:     req.Tconst,
		Title:      req.Title,
		RuTitle:    req.RuTitle,
		Type:       req.Type,
		Hash:       hash,
		Magnet:     req.Magnet,
		FileIndex:  targetFileIdx,
		TargetFile: targetFilePath,
		MountedAt:  time.Now(),
	}

	s.mu.Lock()
	s.mountedTorrents[req.Tconst] = info
	s.mu.Unlock()

	// Pre-seed watch progress entry in SQLite so it is tracked
	if s.playbackStore != nil {
		mediaType := "movie"
		if strings.EqualFold(req.Type, "tvseries") || strings.EqualFold(req.Type, "tv") || req.Season > 0 {
			mediaType = "tv"
		}
		_ = s.playbackStore.SaveProgress(&playback.WatchProgressItem{
			ImdbID:       req.Tconst,
			MediaType:    mediaType,
			Title:        req.Title,
			SeasonNumber: req.Season,
			TorrentHash:  hash,
			TorrentLink:  req.Magnet,
			FileIndex:    targetFileIdx,
		})
	}

	mountedFiles := []string{}
	if targetFilePath != "" {
		mountedFiles = append(mountedFiles, targetFilePath)
	}

	return &MountResponse{
		Success:      true,
		Message:      "Раздача готова к мгновенному просмотру",
		MountedFiles: mountedFiles,
	}, nil
}

// UnmountTorrent drops active torrent swarm from TorrServer
func (s *TorrStreamService) UnmountTorrent(ctx context.Context, req UnmountRequest) (*UnmountResponse, error) {
	s.mu.Lock()
	info := s.mountedTorrents[req.Tconst]
	delete(s.mountedTorrents, req.Tconst)
	s.mu.Unlock()

	if info != nil && info.Hash != "" {
		_ = s.torrClient.DropTorrent(ctx, info.Hash)
	}

	return &UnmountResponse{
		Success: true,
		Message: "Раздача остановлена",
	}, nil
}

// GetMountedStatus checks if torrent is active in TorrServer or watched in SQLite
func (s *TorrStreamService) GetMountedStatus(tconst string) *MountedStatusResponse {
	s.mu.RLock()
	info, exists := s.mountedTorrents[tconst]
	s.mu.RUnlock()

	if exists && info != nil {
		files := []string{}
		if info.TargetFile != "" {
			files = append(files, info.TargetFile)
		}
		return &MountedStatusResponse{
			Mounted:      true,
			Tconst:       tconst,
			Type:         info.Type,
			MountedFiles: files,
		}
	}

	// Check if in SQLite watch progress
	if s.playbackStore != nil {
		item, _ := s.playbackStore.GetItemProgress(tconst, 0, 0)
		if item == nil {
			item, _ = s.playbackStore.GetLatestWatchedEpisode(tconst)
		}
		if item != nil && item.TorrentHash != "" {
			return &MountedStatusResponse{
				Mounted: true,
				Tconst:  tconst,
				Type:    item.MediaType,
			}
		}
	}

	return &MountedStatusResponse{Mounted: false, Tconst: tconst}
}

func (s *TorrStreamService) HandleJellyfinItemDeleted(ctx context.Context, payload JellyfinDeletedWebhook) error {
	return nil
}

// GetPlayerInfo resolves stream URL, audio/subtitle tracks, and resume time for the player
func (s *TorrStreamService) GetPlayerInfo(ctx context.Context, tconst string, season int, episode int) (*PlayerInfoResponse, error) {
	log.Printf("[stream] GetPlayerInfo: tconst=%q season=%d episode=%d", tconst, season, episode)

	s.mu.RLock()
	mountedInfo := s.mountedTorrents[tconst]
	s.mu.RUnlock()

	var hash, magnet, title, targetFilePath string
	var targetFileIdx int = -1

	if mountedInfo != nil {
		hash = mountedInfo.Hash
		magnet = mountedInfo.Magnet
		title = mountedInfo.Title
		targetFileIdx = mountedInfo.FileIndex
		targetFilePath = mountedInfo.TargetFile
	} else if s.playbackStore != nil {
		// Look up in SQLite
		lastEp, _ := s.playbackStore.GetLatestWatchedEpisode(tconst)
		if lastEp != nil {
			hash = lastEp.TorrentHash
			magnet = lastEp.TorrentLink
			title = lastEp.Title
			targetFileIdx = lastEp.FileIndex
		} else {
			movieItem, _ := s.playbackStore.GetItemProgress(tconst, 0, 0)
			if movieItem != nil {
				hash = movieItem.TorrentHash
				magnet = movieItem.TorrentLink
				title = movieItem.Title
				targetFileIdx = movieItem.FileIndex
			}
		}
	}

	if hash == "" {
		return &PlayerInfoResponse{
			Success: false,
			Error:   "Раздача не найдена. Нажмите «Смотреть» для выбора раздачи.",
		}, nil
	}

	// Fetch torrent from TorrServer
	rec, err := s.torrClient.GetTorrent(ctx, hash)
	if err != nil || rec == nil || len(rec.FileStats) == 0 {
		if magnet != "" {
			rec, _ = s.torrClient.AddTorrent(ctx, magnet, title)
			rec, _ = s.torrClient.WaitMetadata(ctx, hash, 3*time.Second)
		}
	}

	if rec != nil && len(rec.FileStats) > 0 {
		matched, matchErr := s.torrClient.MatchFile(rec, season, episode)
		if matchErr == nil && matched != nil {
			targetFileIdx = matched.ID
			targetFilePath = matched.Path
		}
	}
	if targetFileIdx <= 0 {
		if rec != nil && len(rec.FileStats) > 0 {
			targetFileIdx = rec.FileStats[0].ID
			if targetFilePath == "" {
				targetFilePath = rec.FileStats[0].Path
			}
		} else {
			targetFileIdx = 1
		}
	}

	// Resume seconds from SQLite
	var resumeSec float64 = 0
	var isPlayed bool = false
	if s.playbackStore != nil {
		prog, _ := s.playbackStore.GetItemProgress(tconst, season, episode)
		if prog != nil {
			resumeSec = prog.PositionSeconds
			isPlayed = prog.IsCompleted
		}
	}

	// Audio & Subtitle tracks from Probe (via ffprobe if available)
	audioTracks := []AudioTrack{}
	subtitleTracks := []SubtitleTrack{}
	var videoWidth, videoHeight int
	var videoCodec string

	probe := s.probeStream(ctx, hash, targetFileIdx)
	if probe != nil {
		for _, tr := range probe.Tracks {
			switch strings.ToLower(tr.Type) {
			case "video":
				videoWidth = tr.Width
				videoHeight = tr.Height
				videoCodec = tr.Codec
			case "audio":
				lang := tr.Language
				if lang == "" {
					lang = "rus"
				}
				trackTitle := tr.Title
				if trackTitle == "" {
					trackTitle = fmt.Sprintf("Аудио #%d (%s)", tr.Index+1, strings.ToUpper(lang))
				}
				audioTracks = append(audioTracks, AudioTrack{
					Index:     tr.Index,
					Title:     trackTitle,
					Language:  lang,
					Codec:     tr.Codec,
					Channels:  tr.Channels,
					IsDefault: len(audioTracks) == 0,
				})
			case "subtitle":
				lang := tr.Language
				if lang == "" {
					lang = "rus"
				}
				trackTitle := tr.Title
				if trackTitle == "" {
					trackTitle = fmt.Sprintf("Субтитры #%d (%s)", tr.Index+1, strings.ToUpper(lang))
				}
				subtitleTracks = append(subtitleTracks, SubtitleTrack{
					Index:     tr.Index,
					Title:     trackTitle,
					Language:  lang,
					Codec:     tr.Codec,
					IsDefault: len(subtitleTracks) == 0,
				})
			}
		}
	}

	// Fallback audio tracks if probe not available
	if len(audioTracks) == 0 {
		audioTracks = append(audioTracks, AudioTrack{
			Index:     0,
			Title:     "Основная дорожка (Русский / Оригинал)",
			Language:  "rus",
			Codec:     "aac",
			Channels:  2,
			IsDefault: true,
		})
	}

	// Stream URL: Direct TorrServer HTTP streaming route on port 3000 (/torr/stream/...)
	encodedFilename := "video.mkv"
	if targetFilePath != "" {
		encodedFilename = url.PathEscape(filepath.Base(targetFilePath))
	}
	streamURL := fmt.Sprintf("/torr/stream/%s?link=%s&index=%d&play", encodedFilename, hash, targetFileIdx)

	// Build Series Episodes Playlist if TV show
	episodesList := []EpisodeInfo{}
	var hasNextEpisode bool = false
	var nextEpisode *EpisodeInfo = nil

	mediaType := "Movie"
	if season > 0 || episode > 0 {
		mediaType = "Episode"
	}

	if s.nextUpService != nil && (mediaType == "Episode" || season > 0) {
		allEps, _ := s.nextUpService.FetchSeriesEpisodes(tconst)
		seriesProg, _ := s.playbackStore.GetSeriesProgress(tconst)

		for _, ep := range allEps {
			key := fmt.Sprintf("%dx%d", ep.SeasonNumber, ep.EpisodeNumber)
			var epResume float64 = 0
			var epPlayed bool = false
			if st, ok := seriesProg[key]; ok {
				epResume = st.PositionSeconds
				epPlayed = st.IsCompleted
			}

			epId := fmt.Sprintf("%s_s%d_e%d", tconst, ep.SeasonNumber, ep.EpisodeNumber)
			epInfo := EpisodeInfo{
				Id:            epId,
				Name:          ep.Name,
				SeasonNumber:  ep.SeasonNumber,
				EpisodeNumber: ep.EpisodeNumber,
				ResumeSeconds: epResume,
				IsPlayed:      epPlayed,
			}
			episodesList = append(episodesList, epInfo)

			// Next episode detection
			if ep.SeasonNumber == season && ep.EpisodeNumber == episode+1 {
				hasNextEpisode = true
				nextEpCopy := epInfo
				nextEpisode = &nextEpCopy
			}
		}

		if !hasNextEpisode {
			for _, ep := range allEps {
				if ep.SeasonNumber == season+1 && ep.EpisodeNumber == 1 {
					hasNextEpisode = true
					epId := fmt.Sprintf("%s_s%d_e%d", tconst, ep.SeasonNumber, ep.EpisodeNumber)
					nextEpisode = &EpisodeInfo{
						Id:            epId,
						Name:          ep.Name,
						SeasonNumber:  ep.SeasonNumber,
						EpisodeNumber: ep.EpisodeNumber,
					}
					break
				}
			}
		}
	}

	durationSeconds := 3600.0
	if probe != nil && probe.DurationNS > 0 {
		durationSeconds = float64(probe.DurationNS) / 1e9
	}

	return &PlayerInfoResponse{
		Success:         true,
		ItemId:          fmt.Sprintf("%s_s%d_e%d", tconst, season, episode),
		Title:           title,
		MediaType:       mediaType,
		DurationSeconds: durationSeconds,
		ResumeSeconds:   resumeSec,
		IsPlayed:        isPlayed,
		StreamURL:       streamURL,
		MediaSourceId:   hash,
		AudioTracks:     audioTracks,
		Subtitles:       subtitleTracks,
		Episodes:        episodesList,
		CurrentSeason:   season,
		CurrentEpisode:  episode,
		HasNextEpisode:  hasNextEpisode,
		NextEpisode:     nextEpisode,
		Width:           videoWidth,
		Height:          videoHeight,
		VideoCodec:      videoCodec,
	}, nil
}

func parseItemId(itemId string) (string, int, int) {
	// Format: tt14688458_s1_e2 or tt1063870
	parts := strings.Split(itemId, "_")
	if len(parts) == 1 {
		return parts[0], 0, 0
	}
	tconst := parts[0]
	season := 0
	episode := 0
	for _, p := range parts[1:] {
		if strings.HasPrefix(p, "s") {
			season, _ = strconv.Atoi(p[1:])
		} else if strings.HasPrefix(p, "e") {
			episode, _ = strconv.Atoi(p[1:])
		}
	}
	return tconst, season, episode
}

func (s *TorrStreamService) ReportPlaybackStart(ctx context.Context, req *PlaybackStartRequest) (*PlaybackActionResponse, error) {
	return &PlaybackActionResponse{Success: true}, nil
}

func (s *TorrStreamService) ReportPlaybackProgress(ctx context.Context, req *PlaybackProgressRequest) (*PlaybackActionResponse, error) {
	if s.playbackStore == nil || req == nil {
		return &PlaybackActionResponse{Success: true}, nil
	}

	tconst, season, episode := parseItemId(req.ItemId)
	if tconst == "" {
		return &PlaybackActionResponse{Success: false, Message: "Invalid ItemId"}, nil
	}

	s.mu.RLock()
	mounted := s.mountedTorrents[tconst]
	s.mu.RUnlock()

	var title, hash, magnet string
	var fileIdx int = -1

	if mounted != nil {
		title = mounted.Title
		hash = mounted.Hash
		magnet = mounted.Magnet
		fileIdx = mounted.FileIndex
	}

	mediaType := "movie"
	if season > 0 || episode > 0 {
		mediaType = "tv"
	}

	err := s.playbackStore.SaveProgress(&playback.WatchProgressItem{
		ImdbID:          tconst,
		MediaType:       mediaType,
		Title:           title,
		SeasonNumber:    season,
		EpisodeNumber:   episode,
		PositionSeconds: req.PositionSeconds,
		DurationSeconds: req.DurationSeconds,
		TorrentHash:     hash,
		TorrentLink:     magnet,
		FileIndex:       fileIdx,
	})

	if err != nil {
		log.Printf("[stream] SaveProgress error: %v", err)
	}

	return &PlaybackActionResponse{Success: true}, nil
}

func (s *TorrStreamService) ReportPlaybackStop(ctx context.Context, req *PlaybackStopRequest) (*PlaybackActionResponse, error) {
	if s.playbackStore == nil || req == nil {
		return &PlaybackActionResponse{Success: true}, nil
	}

	tconst, season, episode := parseItemId(req.ItemId)
	if tconst == "" {
		return &PlaybackActionResponse{Success: true}, nil
	}

	item, _ := s.playbackStore.GetItemProgress(tconst, season, episode)
	if item != nil {
		item.PositionSeconds = req.PositionSeconds
		if req.DurationSeconds > 0 {
			item.DurationSeconds = req.DurationSeconds
		}
		if req.IsPlayed {
			item.IsCompleted = true
			item.PlaybackPercent = 100.0
		}
		_ = s.playbackStore.SaveProgress(item)
	}

	return &PlaybackActionResponse{Success: true}, nil
}

// GetResumeItems builds the unified Continue Watching and Next Up shelf from SQLite
func (s *TorrStreamService) GetResumeItems(ctx context.Context) ([]ResumeItem, error) {
	if s.playbackStore == nil {
		return []ResumeItem{}, nil
	}

	inProgressItems, err := s.playbackStore.GetResumeList(20)
	if err != nil {
		log.Printf("[stream] GetResumeList error: %v", err)
	}

	var result []ResumeItem
	for _, ip := range inProgressItems {
		imgURL := ip.PosterPath
		if ip.EpisodeStillPath != "" {
			imgURL = ip.EpisodeStillPath
		} else if ip.BackdropPath != "" {
			imgURL = ip.BackdropPath
		}

		mType := "Movie"
		if ip.MediaType == "tv" {
			mType = "Episode"
		}

		itemId := ip.ImdbID
		if ip.MediaType == "tv" {
			itemId = fmt.Sprintf("%s_s%d_e%d", ip.ImdbID, ip.SeasonNumber, ip.EpisodeNumber)
		}

		result = append(result, ResumeItem{
			ItemId:           itemId,
			Tconst:           ip.ImdbID,
			Title:            ip.Title,
			SeriesName:       ip.Title,
			EpisodeTitle:     ip.EpisodeTitle,
			MediaType:        mType,
			SeasonNumber:     ip.SeasonNumber,
			EpisodeNumber:    ip.EpisodeNumber,
			DurationSeconds:  ip.DurationSeconds,
			ResumeSeconds:    ip.PositionSeconds,
			PlayedPercentage: ip.PlaybackPercent,
			ImageUrl:         imgURL,
			IsNextUp:         false,
		})
	}

	// Add Next Up items
	if s.nextUpService != nil {
		nextUpItems, err := s.nextUpService.GetNextUpItems(10)
		if err == nil {
			for _, nu := range nextUpItems {
				imgURL := nu.EpisodeStillPath
				if imgURL == "" {
					imgURL = nu.PosterPath
				}
				itemId := fmt.Sprintf("%s_s%d_e%d", nu.ImdbID, nu.SeasonNumber, nu.EpisodeNumber)
				result = append(result, ResumeItem{
					ItemId:           itemId,
					Tconst:           nu.ImdbID,
					Title:            nu.Title,
					SeriesName:       nu.Title,
					EpisodeTitle:     nu.EpisodeTitle,
					MediaType:        "Episode",
					SeasonNumber:     nu.SeasonNumber,
					EpisodeNumber:    nu.EpisodeNumber,
					DurationSeconds:  3600,
					ResumeSeconds:    0,
					PlayedPercentage: 0,
					ImageUrl:         imgURL,
					IsNextUp:         true,
				})
			}
		}
	}

	return result, nil
}
