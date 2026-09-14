package stream

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"tracker-proxy/pkg/aggregator"
	"tracker-proxy/pkg/cache"
	"tracker-proxy/pkg/hotlist"
	"tracker-proxy/pkg/models"
	"tracker-proxy/pkg/playback"
	"tracker-proxy/pkg/tracker"
	"tracker-proxy/pkg/transcode"
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
	DirectStreamURL string          `json:"direct_stream_url,omitempty"`
	MediaSourceId   string          `json:"media_source_id,omitempty"`
	AudioTracks     []AudioTrack    `json:"audio_tracks,omitempty"`
	Subtitles       []SubtitleTrack `json:"subtitles,omitempty"`
	Episodes        []EpisodeInfo   `json:"episodes,omitempty"`
	CurrentSeason   int             `json:"current_season,omitempty"`
	CurrentEpisode  int             `json:"current_episode,omitempty"`
	HasNextEpisode  bool            `json:"has_next_episode,omitempty"`
	NextEpisode     *EpisodeInfo    `json:"next_episode,omitempty"`
	Width             int                 `json:"width,omitempty"`
	Height            int                 `json:"height,omitempty"`
	Bitrate           int64               `json:"bitrate,omitempty"`
	VideoCodec        string              `json:"video_codec,omitempty"`
	TargetFileIdx      int                 `json:"target_file_idx"`
	TranscodeProfiles  []transcode.Profile `json:"transcode_profiles,omitempty"`
	TranscodeStreamURL string              `json:"transcode_stream_url,omitempty"`
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
	Resolution      string  `json:"resolution,omitempty"`
	FolderName      string  `json:"folder_name,omitempty"`
	PositionSeconds float64 `json:"position_seconds,omitempty"`
	Episode         int     `json:"episode,omitempty"`
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
	Seasons      []int    `json:"seasons,omitempty"`
	Versions     []string `json:"versions,omitempty"`
}

type PlaybackStartRequest struct {
	ItemId              string  `json:"item_id"`
	MediaSourceId       string  `json:"media_source_id,omitempty"`
	AudioStreamIndex    int     `json:"audio_stream_index,omitempty"`
	AudioTitle          string  `json:"audio_title,omitempty"`
	SubtitleStreamIndex int     `json:"subtitle_stream_index,omitempty"`
	PositionSeconds     float64 `json:"position_seconds,omitempty"`
}

type PlaybackProgressRequest struct {
	ItemId           string  `json:"item_id"`
	MediaSourceId    string  `json:"media_source_id,omitempty"`
	PositionSeconds  float64 `json:"position_seconds"`
	DurationSeconds  float64 `json:"duration_seconds,omitempty"`
	IsPaused         bool    `json:"is_paused"`
	Event            string  `json:"event,omitempty"`
	AudioStreamIndex int     `json:"audio_stream_index,omitempty"`
	AudioTitle       string  `json:"audio_title,omitempty"`
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
	Tconst      string
	Title       string
	RuTitle     string
	Type        string
	Hash        string
	Magnet      string
	Season      int
	VersionName string
	FileIndex   int
	TargetFile  string
	MountedAt   time.Time
}

// TorrStreamService manages native streaming from TorrServer MatriX and tracks watch progress
type TorrStreamService struct {
	torrClient    *TorrClient
	playbackStore *playback.Store
	nextUpService *playback.NextUpService
	aggregator    *aggregator.Aggregator
	cacheStore    *cache.Store
	indexerURL    string
	torrServerURL string

	mu              sync.RWMutex
	mountedTorrents map[string]*MountedTorrentInfo
	probeCache      map[string]*ProbeInfo

	metaMu    sync.RWMutex
	metaCache map[string]*IndexerMeta
}

// Alias Mounter for backwards-compatibility with api.Handler
type Mounter = TorrStreamService

type IndexerMeta struct {
	Tconst         string `json:"tconst"`
	Title          string `json:"title"`
	OriginalTitle  string `json:"original_title"`
	Year           int    `json:"year"`
	PosterPath     string `json:"poster_path"`
	BackdropPath   string `json:"backdrop_path"`
	RuntimeMinutes *int   `json:"runtime_minutes,omitempty"`
}

func (s *TorrStreamService) FetchIndexerMeta(ctx context.Context, tconst string) *IndexerMeta {
	return s.fetchIndexerMeta(ctx, tconst)
}

func isRawTorrentTitle(t string) bool {
	if strings.TrimSpace(t) == "" {
		return true
	}
	rawIndicators := []string{
		"WEB-DL", "WEBRip", "BDRip", "HDRip", "BluRay", "DVDRip",
		"1080p", "720p", "2160p", "4K", "UHD",
		"LostFilm", "HDRezka", "Кубик в кубе", "Невафильм", "NewStudio",
		"сезон", "серии", "серия",
	}
	lower := strings.ToLower(t)
	for _, ind := range rawIndicators {
		if strings.Contains(lower, strings.ToLower(ind)) {
			return true
		}
	}
	return false
}

func (s *TorrStreamService) fetchIndexerMeta(ctx context.Context, tconst string) *IndexerMeta {
	if tconst == "" || s.indexerURL == "" {
		return nil
	}

	s.metaMu.RLock()
	if s.metaCache != nil {
		if cached, ok := s.metaCache[tconst]; ok {
			s.metaMu.RUnlock()
			return cached
		}
	}
	s.metaMu.RUnlock()

	client := &http.Client{Timeout: 3 * time.Second}
	url := fmt.Sprintf("%s/api/movie/%s/metadata", s.indexerURL, tconst)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil
	}

	var meta IndexerMeta
	if err := json.NewDecoder(resp.Body).Decode(&meta); err != nil {
		return nil
	}

	s.metaMu.Lock()
	if s.metaCache == nil {
		s.metaCache = make(map[string]*IndexerMeta)
	}
	s.metaCache[tconst] = &meta
	s.metaMu.Unlock()

	return &meta
}

func NewTorrStreamService(
	torrURL, indexerURL string,
	store *playback.Store,
	nextUp *playback.NextUpService,
	agg *aggregator.Aggregator,
	cacheStore *cache.Store,
) *TorrStreamService {
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
		aggregator:      agg,
		cacheStore:      cacheStore,
		indexerURL:      indexerURL,
		torrServerURL:   torrURL,
		mountedTorrents: make(map[string]*MountedTorrentInfo),
		probeCache:      make(map[string]*ProbeInfo),
		metaCache:       make(map[string]*IndexerMeta),
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

// GetStreamDuration returns duration in seconds from probeCache or probeStream.
func (s *TorrStreamService) GetStreamDuration(ctx context.Context, hash string, fileId int) float64 {
	p := s.probeStream(ctx, hash, fileId)
	if p != nil && p.DurationNS > 0 {
		return float64(p.DurationNS) / 1e9
	}
	return 0
}

func (s *TorrStreamService) probeStreamFast(ctx context.Context, hash string, fileId int) *ProbeInfo {
	cacheKey := fmt.Sprintf("%s:%d", hash, fileId)
	s.mu.RLock()
	cached := s.probeCache[cacheKey]
	s.mu.RUnlock()
	if cached != nil {
		return cached
	}

	// Fast 500ms probe attempt
	fastCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()

	if s.torrClient != nil {
		gstProbe, err := s.torrClient.ProbeFile(fastCtx, hash, fileId)
		if err == nil && gstProbe != nil && len(gstProbe.Tracks) > 0 {
			s.mu.Lock()
			if s.probeCache == nil {
				s.probeCache = make(map[string]*ProbeInfo)
			}
			s.probeCache[cacheKey] = gstProbe
			s.mu.Unlock()
			return gstProbe
		}
	}

	// Trigger full probe in background so subsequent requests have full metadata
	go func() {
		bgCtx, bgCancel := context.WithTimeout(context.Background(), 25*time.Second)
		defer bgCancel()
		s.probeStream(bgCtx, hash, fileId)
	}()

	return nil
}

func (s *TorrStreamService) probeStream(ctx context.Context, hash string, fileId int) *ProbeInfo {
	cacheKey := fmt.Sprintf("%s:%d", hash, fileId)
	s.mu.RLock()
	cached := s.probeCache[cacheKey]
	s.mu.RUnlock()
	if cached != nil {
		return cached
	}

	if s.torrClient != nil {
		gstProbe, err := s.torrClient.ProbeFile(ctx, hash, fileId)
		if err == nil && gstProbe != nil && len(gstProbe.Tracks) > 0 {
			s.mu.Lock()
			if s.probeCache == nil {
				s.probeCache = make(map[string]*ProbeInfo)
			}
			s.probeCache[cacheKey] = gstProbe
			s.mu.Unlock()
			return gstProbe
		}
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
	log.Printf("[stream] MountTorrent requested: title=%q tconst=%q magnet=%t season=%d", req.Title, req.Tconst, req.Magnet != "", req.Season)

	if req.Magnet == "" && req.TorrentID != "" && req.Tracker != "" && s.aggregator != nil {
		hash, err := s.aggregator.ResolveInfoHash(ctx, req.Tracker, req.TorrentID)
		if err == nil && hash != "" {
			req.Magnet = aggregator.BuildMultiTrackerMagnet(hash, req.Title, nil, []string{req.Tracker})
		}
	}

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
		Tconst:      req.Tconst,
		Title:       req.Title,
		RuTitle:     req.RuTitle,
		Type:        req.Type,
		Hash:        hash,
		Magnet:      req.Magnet,
		Season:      req.Season,
		VersionName: req.VersionName,
		FileIndex:   targetFileIdx,
		TargetFile:  targetFilePath,
		MountedAt:   time.Now(),
	}

	s.mu.Lock()
	s.mountedTorrents[req.Tconst] = info
	if req.Season > 0 {
		s.mountedTorrents[fmt.Sprintf("%s:s%d", req.Tconst, req.Season)] = info
	}
	s.mu.Unlock()

	// Pre-seed watch progress entry in SQLite so it is tracked
	if s.playbackStore != nil {
		mediaType := "movie"
		if strings.EqualFold(req.Type, "tvseries") || strings.EqualFold(req.Type, "tv") || req.Season > 0 {
			mediaType = "tv"
		}
		title := req.RuTitle
		if title == "" {
			title = req.Title
		}
		var posterPath, backdropPath string
		meta := s.fetchIndexerMeta(ctx, req.Tconst)
		if meta != nil {
			if title == "" || isRawTorrentTitle(title) {
				if meta.Title != "" {
					title = meta.Title
				}
			}
			posterPath = meta.PosterPath
			backdropPath = meta.BackdropPath
		}

		existing, _ := s.playbackStore.GetItemProgress(req.Tconst, req.Season, req.Episode)
		var posSec float64 = 0
		var durSec float64 = 0
		if req.PositionSeconds > 0 {
			posSec = req.PositionSeconds
		} else if existing != nil && existing.PositionSeconds > 0 {
			posSec = existing.PositionSeconds
		}
		if existing != nil && existing.DurationSeconds > 0 {
			durSec = existing.DurationSeconds
		}

		_ = s.playbackStore.SaveProgress(&playback.WatchProgressItem{
			ImdbID:          req.Tconst,
			MediaType:       mediaType,
			Title:           title,
			PosterPath:      posterPath,
			BackdropPath:    backdropPath,
			SeasonNumber:    req.Season,
			EpisodeNumber:   req.Episode,
			PositionSeconds: posSec,
			DurationSeconds: durSec,
			TorrentHash:     hash,
			TorrentLink:     req.Magnet,
			FileIndex:       targetFileIdx,
		})
	}

	mountedFiles := []string{}
	if targetFilePath != "" {
		mountedFiles = append(mountedFiles, targetFilePath)
	}

	// Warm up probe and episode indexes in background
	if hash != "" && rec != nil && len(rec.FileStats) > 0 {
		go s.warmupSeasonIndexes(hash, rec.FileStats, targetFileIdx)
	} else if hash != "" && targetFileIdx > 0 {
		go func() {
			bgCtx, bgCancel := context.WithTimeout(context.Background(), 25*time.Second)
			defer bgCancel()
			s.probeStream(bgCtx, hash, targetFileIdx)
		}()
	}

	return &MountResponse{
		Success:      true,
		Message:      "Раздача готова к мгновенному просмотру",
		MountedFiles: mountedFiles,
	}, nil
}

// warmupSeasonIndexes pre-probes the current episode and gently warms up the rest of the season files
func (s *TorrStreamService) warmupSeasonIndexes(hash string, files []TorrentFileStat, priorityFileId int) {
	if hash == "" || len(files) == 0 {
		return
	}

	// 1. Probe the target/priority file first with high priority
	if priorityFileId > 0 {
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		s.probeStream(ctx, hash, priorityFileId)
		cancel()
	}

	// 2. Gently warm up remaining video files in background
	for _, fi := range files {
		if fi.ID == priorityFileId {
			continue
		}
		// Only video files >= 20MB
		ext := strings.ToLower(filepath.Ext(fi.Path))
		if fi.Length < 20<<20 || !videoExtensions[ext] {
			continue
		}
		cacheKey := fmt.Sprintf("%s:%d", hash, fi.ID)
		s.mu.RLock()
		alreadyCached := s.probeCache[cacheKey] != nil
		s.mu.RUnlock()
		if alreadyCached {
			continue
		}

		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		s.probeStream(ctx, hash, fi.ID)
		cancel()

		// Gentle pacing between files so we don't saturate network or TorrServer
		time.Sleep(300 * time.Millisecond)
	}
}

// UnmountTorrent drops active torrent swarm from TorrServer
func (s *TorrStreamService) UnmountTorrent(ctx context.Context, req UnmountRequest) (*UnmountResponse, error) {
	s.mu.Lock()
	info := s.mountedTorrents[req.Tconst]
	delete(s.mountedTorrents, req.Tconst)
	for k := range s.mountedTorrents {
		if strings.HasPrefix(k, req.Tconst+":") {
			delete(s.mountedTorrents, k)
		}
	}
	s.mu.Unlock()

	if info != nil && info.Hash != "" {
		_ = s.torrClient.DropTorrent(ctx, info.Hash)
		_ = os.RemoveAll(filepath.Join(os.TempDir(), "cineclaw_hls", info.Hash))
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
	var seasons []int
	var versions []string
	versionMap := make(map[string]bool)
	seasonMap := make(map[int]bool)
	files := []string{}

	for k, v := range s.mountedTorrents {
		if k == tconst || strings.HasPrefix(k, tconst+":") {
			if v.Season > 0 && !seasonMap[v.Season] {
				seasonMap[v.Season] = true
				seasons = append(seasons, v.Season)
			}
			if v.VersionName != "" && !versionMap[v.VersionName] {
				versionMap[v.VersionName] = true
				versions = append(versions, v.VersionName)
			}
			if v.TargetFile != "" {
				files = append(files, v.TargetFile)
			}
		}
	}
	s.mu.RUnlock()

	if s.playbackStore != nil {
		watchedSeasons, _ := s.playbackStore.GetWatchedSeasons(tconst)
		for _, sn := range watchedSeasons {
			if !seasonMap[sn] {
				seasonMap[sn] = true
				seasons = append(seasons, sn)
			}
		}
	}
	sort.Ints(seasons)

	if exists && info != nil {
		return &MountedStatusResponse{
			Mounted:      true,
			Tconst:       tconst,
			Type:         info.Type,
			MountedFiles: files,
			Seasons:      seasons,
			Versions:     versions,
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
				Mounted:      true,
				Tconst:       tconst,
				Type:         item.MediaType,
				MountedFiles: files,
				Seasons:      seasons,
				Versions:     versions,
			}
		}
	}

	return &MountedStatusResponse{
		Mounted:      len(seasons) > 0,
		Tconst:       tconst,
		MountedFiles: files,
		Seasons:      seasons,
		Versions:     versions,
	}
}

func (s *TorrStreamService) HandleJellyfinItemDeleted(ctx context.Context, payload JellyfinDeletedWebhook) error {
	return nil
}

// ScoreCandidate evaluates how well a torrent candidate matches the intended title, metadata, and season.
func ScoreCandidate(c *models.TorrentResult, meta *IndexerMeta, season int) int {
	score := c.Seeds * 10
	if score > 1000 {
		score = 1000 // cap raw seed contribution so seed count cannot overwhelm relevance
	}

	titleLower := strings.ToLower(c.Title)
	if strings.Contains(titleLower, "1080p") {
		score += 30
	} else if strings.Contains(titleLower, "2160p") || strings.Contains(titleLower, "4k") {
		score += 20
	} else if strings.Contains(titleLower, "720p") {
		score += 10
	}

	if meta != nil {
		cRus, cOrig, cYear, _, _ := hotlist.ParseReleaseTitle(c.Title)

		// 1. Year matching (crucial for movies and disambiguation)
		if meta.Year > 0 && cYear > 0 {
			if cYear == meta.Year {
				score += 500 // exact year match
			} else if cYear == meta.Year-1 || cYear == meta.Year+1 {
				score += 150 // acceptable boundary year (festival release vs tracker upload)
			} else {
				score -= 2000 // major year mismatch! (e.g. 2023 vs 2026)
			}
		}

		// 2. Original title matching
		if meta.OriginalTitle != "" {
			origClean := strings.ToLower(strings.TrimSpace(meta.OriginalTitle))
			if len(origClean) >= 3 {
				if strings.Contains(titleLower, origClean) {
					score += 600 // exact original title found in release
				} else if cOrig != "" && strings.EqualFold(cOrig, origClean) {
					score += 600
				} else if cOrig != "" && !strings.Contains(strings.ToLower(cOrig), origClean) {
					score -= 1000 // candidate has a completely different original title!
				}
			}
		}

		// 3. Russian title precision matching
		if meta.Title != "" {
			rusClean := strings.ToLower(strings.TrimSpace(meta.Title))
			if len(rusClean) >= 3 {
				if cRus != "" {
					cRusLower := strings.ToLower(strings.TrimSpace(cRus))
					if cRusLower == rusClean {
						score += 400 // exact Russian title match
					} else if strings.HasPrefix(cRusLower, rusClean+" ") || strings.Contains(cRusLower, " "+rusClean) {
						// e.g. "приглашение к убийству" vs "приглашение"
						score -= 400
					}
				}
			}
		}
	}

	if season > 0 {
		if len(c.Seasons) == 1 && c.Seasons[0] == season {
			score += 100
		}
	}

	return score
}

// autoResolveTorrent finds and resolves the best available torrent for a title/season
func (s *TorrStreamService) autoResolveTorrent(ctx context.Context, tconst string, season int) (*models.TorrentResult, error) {
	log.Printf("[stream] autoResolveTorrent: looking up torrent for tconst=%q season=%d", tconst, season)
	var candidates []models.TorrentResult

	normIMDb := cache.NormalizeIMDbID(tconst)
	var meta *IndexerMeta
	if normIMDb != "" {
		meta = s.fetchIndexerMeta(ctx, normIMDb)
	}

	if normIMDb != "" && s.cacheStore != nil {
		if cached, found, err := s.cacheStore.Get(normIMDb); err == nil && found && len(cached) > 0 {
			candidates = cached
		}
	}

	if len(candidates) == 0 && s.aggregator != nil && normIMDb != "" {
		mediaType := "movie"
		if season > 0 {
			mediaType = "tv"
		}
		var queryStr string
		if meta != nil {
			baseTitle := strings.TrimSpace(meta.Title)
			if baseTitle == "" {
				baseTitle = strings.TrimSpace(meta.OriginalTitle)
			}
			if baseTitle != "" {
				if meta.Year > 0 && mediaType == "movie" {
					queryStr = fmt.Sprintf("%s %d", baseTitle, meta.Year)
				} else {
					queryStr = baseTitle
				}
			}
		}
		if queryStr == "" {
			return nil, fmt.Errorf("unable to resolve title for %s to search torrents", normIMDb)
		}

		res := s.aggregator.Search(ctx, models.SearchQuery{
			Query:  queryStr,
			IMDbID: normIMDb,
			Type:   mediaType,
			Limit:  50,
		})
		if len(res) == 0 && meta != nil && strings.TrimSpace(meta.Title) != "" && queryStr != strings.TrimSpace(meta.Title) {
			res = s.aggregator.Search(ctx, models.SearchQuery{
				Query:  strings.TrimSpace(meta.Title),
				IMDbID: normIMDb,
				Type:   mediaType,
				Limit:  50,
			})
		}
		if len(res) > 0 {
			candidates = res
			if s.cacheStore != nil {
				_ = s.cacheStore.Set(normIMDb, queryStr, res)
			}
		}
	}

	if len(candidates) == 0 {
		return nil, fmt.Errorf("no torrents found for %s", tconst)
	}

	for i := range candidates {
		if len(candidates[i].Seasons) == 0 && !candidates[i].IsComplete {
			candidates[i].Seasons, candidates[i].IsComplete = tracker.ExtractSeasonInfo(candidates[i].Title)
		}
		if candidates[i].Resolution == "" {
			candidates[i].Resolution = tracker.ExtractResolution(candidates[i].Title)
		}
	}

	var pool []models.TorrentResult
	if season > 0 {
		for _, c := range candidates {
			if c.IsComplete {
				pool = append(pool, c)
				continue
			}
			for _, sn := range c.Seasons {
				if sn == season {
					pool = append(pool, c)
					break
				}
			}
		}
		if len(pool) == 0 {
			return nil, fmt.Errorf("сезон %d не найден среди доступных раздач", season)
		}
	} else {
		pool = candidates
	}

	sort.Slice(pool, func(i, j int) bool {
		return ScoreCandidate(&pool[i], meta, season) > ScoreCandidate(&pool[j], meta, season)
	})

	chosen := pool[0]

	if chosen.Magnet == "" && chosen.ID != "" && chosen.Tracker != "" && s.aggregator != nil {
		hash, err := s.aggregator.ResolveInfoHash(ctx, chosen.Tracker, chosen.ID)
		if err == nil && hash != "" {
			chosen.Magnet = aggregator.BuildMultiTrackerMagnet(hash, chosen.Title, nil, []string{chosen.Tracker})
		}
	}

	if chosen.Magnet == "" {
		return nil, fmt.Errorf("failed to resolve magnet for chosen torrent: %s", chosen.Title)
	}

	log.Printf("[stream] autoResolveTorrent: selected %q (seeds=%d, magnet=%t)", chosen.Title, chosen.Seeds, chosen.Magnet != "")
	return &chosen, nil
}

// selectDefaultAudioTrack chooses the best default audio track (prioritizing Russian dubbing/voiceover)
func selectDefaultAudioTrack(tracks []AudioTrack) int {
	return selectDefaultAudioTrackWithPreference(tracks, nil)
}

// selectDefaultAudioTrackWithPreference matches tracks against a remembered audio preference,
// falling back to standard Russian dub/voiceover ranking if not found.
func selectDefaultAudioTrackWithPreference(tracks []AudioTrack, pref *playback.AudioPreference) int {
	if len(tracks) == 0 {
		return 0
	}

	if pref != nil && (pref.AudioTitle != "" || pref.AudioIndex >= 0) {
		prefTitleLower := strings.ToLower(strings.TrimSpace(pref.AudioTitle))

		// 1. Exact title match
		if prefTitleLower != "" {
			for i, tr := range tracks {
				if strings.EqualFold(strings.TrimSpace(tr.Title), strings.TrimSpace(pref.AudioTitle)) {
					return i
				}
			}
		}

		// 2. Author / Studio / Keyword match
		var matchedIndex = -1
		var bestKeywordScore = 0

		keywords := []string{
			"гоблин", "goblin", "пучков",
			"сербин", "serbin",
			"amedia", "амедиа",
			"нтв", "ntv",
			"fox", "fox crime",
			"карповский", "боровой",
			"lostfilm", "лостфильм",
			"hdrezka", "резка",
			"кубик", "kubik",
			"дубляж", "дублированный", "dub",
			"newstudio", "ньюстудио",
			"кураж", "kuraj",
			"red head sound", "rhs",
			"пифагор", "мост-видео", "инис",
			"живов", "гаврилов", "михалев", "володарский",
			"novamedia", "новамедиа", "кириллица", "владимир захаров",
			"английский", "english", "original",
		}

		for _, kw := range keywords {
			if strings.Contains(prefTitleLower, kw) {
				for i, tr := range tracks {
					tLower := strings.ToLower(tr.Title)
					if strings.Contains(tLower, kw) {
						score := len(kw) * 10
						if score > bestKeywordScore {
							bestKeywordScore = score
							matchedIndex = i
						}
					}
				}
			}
		}

		if matchedIndex >= 0 {
			return matchedIndex
		}

		// 3. Fallback to index if within bounds and language matches
		if pref.AudioIndex >= 0 && pref.AudioIndex < len(tracks) {
			targetTrack := tracks[pref.AudioIndex]
			isPrefRussian := strings.Contains(prefTitleLower, "рус") || strings.Contains(prefTitleLower, "ru") || strings.Contains(prefTitleLower, "мво") || strings.Contains(prefTitleLower, "mvo")
			targetLang := strings.ToLower(targetTrack.Language)
			targetTitle := strings.ToLower(targetTrack.Title)
			isTargetRussian := targetLang == "ru" || targetLang == "rus" || strings.Contains(targetTitle, "рус") || strings.Contains(targetTitle, "аудио")

			if !isPrefRussian || isTargetRussian {
				return pref.AudioIndex
			}
		}
	}

	// 4. Default heuristic ranking
	bestIdx := 0
	bestScore := -1

	for i, tr := range tracks {
		score := 0
		langLower := strings.ToLower(tr.Language)
		titleLower := strings.ToLower(tr.Title)

		isRussian := langLower == "ru" || langLower == "rus" || langLower == "russian" ||
			strings.Contains(titleLower, "рус") || strings.Contains(titleLower, "дубл") ||
			strings.Contains(titleLower, "мво") || strings.Contains(titleLower, "mvo") ||
			strings.Contains(titleLower, "dub") || strings.Contains(titleLower, "hdrezka") ||
			strings.Contains(titleLower, "lostfilm") || strings.Contains(titleLower, "red head sound") ||
			strings.Contains(titleLower, "rhs") || strings.Contains(titleLower, "line")

		if isRussian {
			score += 100

			// Prefer professional full dubbing first
			if strings.Contains(titleLower, "dub") || strings.Contains(titleLower, "дубл") {
				score += 50
			} else if strings.Contains(titleLower, "mvo") || strings.Contains(titleLower, "многоголос") {
				score += 30
			} else if strings.Contains(titleLower, "hdrezka") || strings.Contains(titleLower, "lostfilm") {
				score += 25
			}

			// Prefer higher channel count (5.1 > stereo)
			if tr.Channels >= 6 {
				score += 10
			}
		}

		// Slight tie-breaker: earlier track order
		score -= i

		if score > bestScore {
			bestScore = score
			bestIdx = i
		}
	}

	return bestIdx
}

// GetPlayerInfo resolves stream URL, audio/subtitle tracks, and resume time for the player
func (s *TorrStreamService) GetPlayerInfo(ctx context.Context, tconst string, season int, episode int) (*PlayerInfoResponse, error) {
	log.Printf("[stream] GetPlayerInfo: tconst=%q season=%d episode=%d", tconst, season, episode)

	s.mu.RLock()
	var mountedInfo *MountedTorrentInfo
	if season > 0 {
		mountedInfo = s.mountedTorrents[fmt.Sprintf("%s:s%d", tconst, season)]
	}
	if mountedInfo == nil {
		mountedInfo = s.mountedTorrents[tconst]
	}
	s.mu.RUnlock()

	var hash, magnet, title, targetFilePath string
	var targetFileIdx int = -1

	if mountedInfo != nil {
		hash = mountedInfo.Hash
		magnet = mountedInfo.Magnet
		title = mountedInfo.Title
		if season == 0 || (season == mountedInfo.Season && episode <= 1) {
			targetFileIdx = mountedInfo.FileIndex
			targetFilePath = mountedInfo.TargetFile
		}
	} else if s.playbackStore != nil {
		// Look up in SQLite
		if season > 0 {
			if episode > 0 {
				epItem, _ := s.playbackStore.GetItemProgress(tconst, season, episode)
				if epItem != nil && epItem.TorrentHash != "" {
					hash = epItem.TorrentHash
					magnet = epItem.TorrentLink
					title = epItem.Title
					targetFileIdx = epItem.FileIndex
				}
			}
			if hash == "" {
				seasonEp, _ := s.playbackStore.GetLatestWatchedEpisodeForSeason(tconst, season)
				if seasonEp != nil && seasonEp.TorrentHash != "" {
					hash = seasonEp.TorrentHash
					magnet = seasonEp.TorrentLink
					title = seasonEp.Title
				}
			}
		}
		if hash == "" {
			lastEp, _ := s.playbackStore.GetLatestWatchedEpisode(tconst)
			if lastEp != nil && lastEp.TorrentHash != "" {
				if season == 0 || lastEp.SeasonNumber == season {
					hash = lastEp.TorrentHash
					magnet = lastEp.TorrentLink
					title = lastEp.Title
				}
			}
		}
		if hash == "" && season == 0 {
			movieItem, _ := s.playbackStore.GetItemProgress(tconst, 0, 0)
			if movieItem != nil && movieItem.TorrentHash != "" {
				hash = movieItem.TorrentHash
				magnet = movieItem.TorrentLink
				title = movieItem.Title
				targetFileIdx = movieItem.FileIndex
			}
		}
	}

	if hash == "" {
		log.Printf("[stream] No active mount or watch progress for %s (season=%d). Attempting auto-resolve...", tconst, season)
		bestTorrent, err := s.autoResolveTorrent(ctx, tconst, season)
		if err != nil || bestTorrent == nil {
			log.Printf("[stream] autoResolveTorrent failed: %v", err)
			return &PlayerInfoResponse{
				Success: false,
				Error:   "Раздача не найдена. Нажмите «Смотреть» для выбора раздачи.",
			}, nil
		}

		mountReq := MountRequest{
			Tconst:      tconst,
			Title:       bestTorrent.Title,
			Magnet:      bestTorrent.Magnet,
			Season:      season,
			Type:        func() string { if season > 0 { return "tvSeries" }; return "movie" }(),
			Tracker:     bestTorrent.Tracker,
			TorrentID:   bestTorrent.ID,
			Resolution:  bestTorrent.Resolution,
			VersionName: "Auto",
		}
		mountResp, err := s.MountTorrent(ctx, mountReq)
		if err != nil || mountResp == nil || !mountResp.Success {
			log.Printf("[stream] Auto MountTorrent failed: %v", err)
			return &PlayerInfoResponse{
				Success: false,
				Error:   "Ошибка автоматической подготовки раздачи в TorrServer.",
			}, nil
		}

		hash = ExtractHashFromMagnet(bestTorrent.Magnet)
		magnet = bestTorrent.Magnet
		title = bestTorrent.Title
	}

	// Fetch torrent from TorrServer
	rec, err := s.torrClient.GetTorrent(ctx, hash)
	if err != nil || rec == nil || len(rec.FileStats) == 0 {
		if magnet != "" {
			rec, _ = s.torrClient.AddTorrent(ctx, magnet, title)
			rec, _ = s.torrClient.WaitMetadata(ctx, hash, 5*time.Second)
		}
	}

	if rec == nil || len(rec.FileStats) == 0 {
		return &PlayerInfoResponse{
			Success: false,
			Error:   "TorrServer получает метаданные раздачи, подключаемся к пирам...",
		}, nil
	}

	if rec != nil && len(rec.FileStats) > 0 {
		matched, matchErr := s.torrClient.MatchFile(rec, season, episode)
		if matchErr == nil && matched != nil {
			targetFileIdx = matched.ID
			targetFilePath = matched.Path
		} else if season > 0 {
			log.Printf("[stream] Episode S%02dE%02d not found in current torrent %s: %v. Attempting auto-resolve for season %d...",
				season, episode, hash, matchErr, season)
			bestTorrent, autoErr := s.autoResolveTorrent(ctx, tconst, season)
			if autoErr == nil && bestTorrent != nil {
				newHash := ExtractHashFromMagnet(bestTorrent.Magnet)
				if newHash != "" && newHash != hash {
					mountReq := MountRequest{
						Tconst:      tconst,
						Title:       bestTorrent.Title,
						Magnet:      bestTorrent.Magnet,
						Season:      season,
						Type:        "tvSeries",
						Tracker:     bestTorrent.Tracker,
						TorrentID:   bestTorrent.ID,
						Resolution:  bestTorrent.Resolution,
						VersionName: "Auto",
					}
					mountResp, mErr := s.MountTorrent(ctx, mountReq)
					if mErr == nil && mountResp != nil && mountResp.Success {
						newRec, _ := s.torrClient.GetTorrent(ctx, newHash)
						if newRec == nil || len(newRec.FileStats) == 0 {
							newRec, _ = s.torrClient.WaitMetadata(ctx, newHash, 5*time.Second)
						}
						if newRec != nil && len(newRec.FileStats) > 0 {
							matchedNew, matchErrNew := s.torrClient.MatchFile(newRec, season, episode)
							if matchErrNew == nil && matchedNew != nil {
								rec = newRec
								hash = newHash
								magnet = bestTorrent.Magnet
								title = bestTorrent.Title
								targetFileIdx = matchedNew.ID
								targetFilePath = matchedNew.Path
								matchErr = nil
							}
						}
					}
				}
			}
		}

		if matchErr != nil && season > 0 && episode > 0 {
			return &PlayerInfoResponse{
				Success: false,
				Error:   fmt.Sprintf("Серия S%02dE%02d не найдена в раздаче: %v", season, episode, matchErr),
			}, nil
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

	probe := s.probeStreamFast(ctx, hash, targetFileIdx)
	if probe != nil {
		for _, tr := range probe.Tracks {
			switch strings.ToLower(tr.Type) {
			case "video":
				if strings.EqualFold(tr.Codec, "mjpeg") || strings.EqualFold(tr.Codec, "png") || strings.EqualFold(tr.Codec, "jpeg") {
					continue
				}
				if videoWidth == 0 {
					videoWidth = tr.Width
					videoHeight = tr.Height
					videoCodec = cleanVideoCodec(tr.Codec)
				}
			case "audio":
				lang := strings.TrimSpace(tr.Language)
				if lang == "" || strings.EqualFold(lang, "und") {
					lang = "rus"
				}
				trackTitle := strings.TrimSpace(tr.Title)
				if trackTitle == "" {
					langLower := strings.ToLower(lang)
					if langLower == "ru" || langLower == "rus" || langLower == "russian" {
						trackTitle = fmt.Sprintf("Русский (Аудио #%d)", len(audioTracks)+1)
					} else if langLower == "en" || langLower == "eng" || langLower == "english" {
						trackTitle = fmt.Sprintf("English (Аудио #%d)", len(audioTracks)+1)
					} else {
						trackTitle = fmt.Sprintf("Аудио #%d (%s)", len(audioTracks)+1, strings.ToUpper(lang))
					}
				}
				audioTracks = append(audioTracks, AudioTrack{
					Index:     len(audioTracks),
					Title:     trackTitle,
					Language:  lang,
					Codec:     cleanAudioCodec(tr.Codec),
					Channels:  tr.Channels,
					IsDefault: false,
				})
			case "subtitle":
				lang := strings.TrimSpace(tr.Language)
				if lang == "" || strings.EqualFold(lang, "und") {
					lang = "rus"
				}
				trackTitle := strings.TrimSpace(tr.Title)
				if trackTitle == "" {
					langLower := strings.ToLower(lang)
					if langLower == "ru" || langLower == "rus" || langLower == "russian" {
						trackTitle = fmt.Sprintf("Русские (Субтитры #%d)", len(subtitleTracks)+1)
					} else if langLower == "en" || langLower == "eng" || langLower == "english" {
						trackTitle = fmt.Sprintf("English (Субтитры #%d)", len(subtitleTracks)+1)
					} else {
						trackTitle = fmt.Sprintf("Субтитры #%d (%s)", len(subtitleTracks)+1, strings.ToUpper(lang))
					}
				}
				subtitleTracks = append(subtitleTracks, SubtitleTrack{
					Index:     len(subtitleTracks),
					Title:     trackTitle,
					Language:  lang,
					Codec:     tr.Codec,
					IsDefault: len(subtitleTracks) == 0,
				})
			}
		}
	}

	// Fallback video parameters if probe not available yet
	if videoWidth == 0 {
		videoWidth = 1920
		videoHeight = 1080
		videoCodec = "H.264"
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
	} else {
		var audioPref *playback.AudioPreference
		if s.playbackStore != nil && tconst != "" {
			audioPref, _ = s.playbackStore.GetAudioPreference(tconst)
		}
		defIdx := selectDefaultAudioTrackWithPreference(audioTracks, audioPref)
		for i := range audioTracks {
			audioTracks[i].IsDefault = (i == defIdx)
		}
	}

	defaultAudioIdx := 0
	for _, a := range audioTracks {
		if a.IsDefault {
			defaultAudioIdx = a.Index
			break
		}
	}

	// Stream URLs:
	// directStreamURL points directly to TorrServer native HTTP piece streaming (for external players)
	encodedFilename := "video.mkv"
	if targetFilePath != "" {
		encodedFilename = url.PathEscape(filepath.Base(targetFilePath))
	}
	directStreamURL := fmt.Sprintf("/torr/stream/%s?link=%s&index=%d&play", encodedFilename, hash, targetFileIdx)

	// streamURL points to TorrServer GStreamer HLS master playlist for seamless web playback & audio track switching
	streamURL := fmt.Sprintf("/gst/%s/master.m3u8?id=%d&audio=%d", hash, targetFileIdx, defaultAudioIdx)

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
	if meta := s.fetchIndexerMeta(ctx, tconst); meta != nil && meta.RuntimeMinutes != nil && *meta.RuntimeMinutes > 0 {
		durationSeconds = float64(*meta.RuntimeMinutes * 60)
	}
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
		DirectStreamURL: directStreamURL,
		MediaSourceId:   hash,
		AudioTracks:     audioTracks,
		Subtitles:       subtitleTracks,
		Episodes:        episodesList,
		CurrentSeason:   season,
		CurrentEpisode:  episode,
		HasNextEpisode:  hasNextEpisode,
		NextEpisode:     nextEpisode,
		Width:             videoWidth,
		Height:            videoHeight,
		VideoCodec:         videoCodec,
		TargetFileIdx:      targetFileIdx,
		TranscodeProfiles:  transcode.AvailableProfiles(),
		TranscodeStreamURL: fmt.Sprintf("/api/stream/transcode/%s/master.m3u8?profile=1080p&file_idx=%d&audio=%d&duration=%.2f", hash, targetFileIdx, defaultAudioIdx, durationSeconds),
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
	if s.playbackStore == nil || req == nil {
		return &PlaybackActionResponse{Success: true}, nil
	}
	tconst, season, episode := parseItemId(req.ItemId)
	if tconst != "" {
		if req.AudioTitle != "" {
			_ = s.playbackStore.SaveAudioPreference(tconst, req.AudioTitle, req.AudioStreamIndex)
		}
		existing, _ := s.playbackStore.GetItemProgress(tconst, season, episode)
		if existing == nil {
			s.saveProgressInternal(ctx, tconst, season, episode, 0, 0, false)
		}
	}
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

	if req.AudioTitle != "" {
		_ = s.playbackStore.SaveAudioPreference(tconst, req.AudioTitle, req.AudioStreamIndex)
	}

	s.saveProgressInternal(ctx, tconst, season, episode, req.PositionSeconds, req.DurationSeconds, false)
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

	s.saveProgressInternal(ctx, tconst, season, episode, req.PositionSeconds, req.DurationSeconds, req.IsPlayed)
	return &PlaybackActionResponse{Success: true}, nil
}

func (s *TorrStreamService) saveProgressInternal(ctx context.Context, tconst string, season, episode int, pos, dur float64, isPlayed bool) {
	if s.playbackStore == nil || tconst == "" {
		return
	}

	s.mu.RLock()
	mountedKey := tconst
	if season > 0 {
		mountedKey = fmt.Sprintf("%s:s%d", tconst, season)
	}
	mounted := s.mountedTorrents[mountedKey]
	if mounted == nil {
		mounted = s.mountedTorrents[tconst]
	}
	s.mu.RUnlock()

	var title, hash, magnet string
	var fileIdx int = -1

	if mounted != nil {
		title = mounted.RuTitle
		if title == "" {
			title = mounted.Title
		}
		hash = mounted.Hash
		magnet = mounted.Magnet
		fileIdx = mounted.FileIndex
	}

	mediaType := "movie"
	if season > 0 || episode > 0 {
		mediaType = "tv"
	}

	// Fetch existing item to preserve existing poster/backdrop and clean title
	existingItem, _ := s.playbackStore.GetItemProgress(tconst, season, episode)
	posterPath := ""
	backdropPath := ""
	epTitle := ""
	epStill := ""
	if existingItem != nil {
		if title == "" || isRawTorrentTitle(title) {
			title = existingItem.Title
		}
		posterPath = existingItem.PosterPath
		backdropPath = existingItem.BackdropPath
		epTitle = existingItem.EpisodeTitle
		epStill = existingItem.EpisodeStillPath
		if hash == "" {
			hash = existingItem.TorrentHash
		}
		if magnet == "" {
			magnet = existingItem.TorrentLink
		}
		if fileIdx < 0 {
			fileIdx = existingItem.FileIndex
		}
	}

	if title == "" || isRawTorrentTitle(title) || (posterPath == "" && backdropPath == "") {
		meta := s.fetchIndexerMeta(ctx, tconst)
		if meta != nil {
			if title == "" || isRawTorrentTitle(title) {
				if meta.Title != "" {
					title = meta.Title
				}
			}
			if posterPath == "" {
				posterPath = meta.PosterPath
			}
			if backdropPath == "" {
				backdropPath = meta.BackdropPath
			}
		}
	}

	if mediaType == "tv" && s.nextUpService != nil && (epTitle == "" || epStill == "") {
		allEps, _ := s.nextUpService.FetchSeriesEpisodes(tconst)
		for _, ep := range allEps {
			if ep.SeasonNumber == season && ep.EpisodeNumber == episode {
				if epTitle == "" && ep.Name != "" {
					epTitle = ep.Name
				}
				if epStill == "" && ep.StillPath != "" {
					epStill = ep.StillPath
				}
				break
			}
		}
	}

	pct := 0.0
	if isPlayed {
		pct = 100.0
	} else if dur > 0 {
		pct = (pos / dur) * 100.0
	}

	err := s.playbackStore.SaveProgress(&playback.WatchProgressItem{
		ImdbID:           tconst,
		MediaType:        mediaType,
		Title:            title,
		PosterPath:       posterPath,
		BackdropPath:     backdropPath,
		SeasonNumber:     season,
		EpisodeNumber:    episode,
		EpisodeTitle:     epTitle,
		EpisodeStillPath: epStill,
		PositionSeconds:  pos,
		DurationSeconds:  dur,
		TorrentHash:      hash,
		TorrentLink:      magnet,
		FileIndex:        fileIdx,
		IsCompleted:      isPlayed,
		PlaybackPercent:  pct,
	})

	if err != nil {
		log.Printf("[stream] SaveProgress error: %v", err)
	}
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
		needsUpdate := false

		// 1. Check if title or poster/backdrop is missing or raw
		if ip.Title == "" || isRawTorrentTitle(ip.Title) || (ip.PosterPath == "" && ip.BackdropPath == "") {
			meta := s.fetchIndexerMeta(ctx, ip.ImdbID)
			if meta != nil {
				if (ip.Title == "" || isRawTorrentTitle(ip.Title)) && meta.Title != "" {
					ip.Title = meta.Title
					needsUpdate = true
				}
				if ip.PosterPath == "" && meta.PosterPath != "" {
					ip.PosterPath = meta.PosterPath
					needsUpdate = true
				}
				if ip.BackdropPath == "" && meta.BackdropPath != "" {
					ip.BackdropPath = meta.BackdropPath
					needsUpdate = true
				}
			}
		}

		// 2. If it's a TV episode, check if episode title or still is missing
		if ip.MediaType == "tv" && s.nextUpService != nil && (ip.EpisodeTitle == "" || ip.EpisodeStillPath == "") {
			allEps, _ := s.nextUpService.FetchSeriesEpisodes(ip.ImdbID)
			for _, ep := range allEps {
				if ep.SeasonNumber == ip.SeasonNumber && ep.EpisodeNumber == ip.EpisodeNumber {
					if ip.EpisodeTitle == "" && ep.Name != "" {
						ip.EpisodeTitle = ep.Name
						needsUpdate = true
					}
					if ip.EpisodeStillPath == "" && ep.StillPath != "" {
						ip.EpisodeStillPath = ep.StillPath
						needsUpdate = true
					}
					break
				}
			}
		}

		// Save updated record back to DB so subsequent reads are instant
		if needsUpdate {
			_ = s.playbackStore.SaveProgress(&ip)
		}

		// Choose the best image (prefer episode still, then backdrop for 16:9, then poster)
		imgURL := ip.EpisodeStillPath
		if imgURL == "" {
			imgURL = ip.BackdropPath
		}
		if imgURL == "" {
			imgURL = ip.PosterPath
		}

		// Format TMDB relative path
		if imgURL != "" {
			if !strings.HasPrefix(imgURL, "http://") && !strings.HasPrefix(imgURL, "https://") && !strings.HasPrefix(imgURL, "/poster/") {
				if strings.HasPrefix(imgURL, "/") {
					imgURL = "https://image.tmdb.org/t/p/w780" + imgURL
				}
			}
		}

		// Universal fallback to poster proxy
		if imgURL == "" && ip.ImdbID != "" {
			imgURL = fmt.Sprintf("/poster/%s?size=w500", ip.ImdbID)
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
				title := nu.Title
				meta := s.fetchIndexerMeta(ctx, nu.ImdbID)
				if meta != nil {
					if title == "" || isRawTorrentTitle(title) {
						if meta.Title != "" {
							title = meta.Title
						}
					}
					if nu.BackdropPath == "" {
						nu.BackdropPath = meta.BackdropPath
					}
					if nu.PosterPath == "" {
						nu.PosterPath = meta.PosterPath
					}
				}

				imgURL := nu.EpisodeStillPath
				if imgURL == "" {
					imgURL = nu.BackdropPath
				}
				if imgURL == "" {
					imgURL = nu.PosterPath
				}
				if imgURL != "" {
					if !strings.HasPrefix(imgURL, "http://") && !strings.HasPrefix(imgURL, "https://") && !strings.HasPrefix(imgURL, "/poster/") {
						if strings.HasPrefix(imgURL, "/") {
							imgURL = "https://image.tmdb.org/t/p/w780" + imgURL
						}
					}
				}
				if imgURL == "" && nu.ImdbID != "" {
					imgURL = fmt.Sprintf("/poster/%s?size=w500", nu.ImdbID)
				}

				itemId := fmt.Sprintf("%s_s%d_e%d", nu.ImdbID, nu.SeasonNumber, nu.EpisodeNumber)
				result = append(result, ResumeItem{
					ItemId:           itemId,
					Tconst:           nu.ImdbID,
					Title:            title,
					SeriesName:       title,
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

type DeleteResumeRequest struct {
	ItemId   string `json:"item_id"`
	Tconst   string `json:"tconst,omitempty"`
	Season   int    `json:"season,omitempty"`
	Episode  int    `json:"episode,omitempty"`
	IsNextUp bool   `json:"is_next_up,omitempty"`
	All      bool   `json:"all,omitempty"`
}

// DeleteResumeItem removes watch progress for a movie, episode, or entire show
func (s *TorrStreamService) DeleteResumeItem(ctx context.Context, req *DeleteResumeRequest) error {
	if s.playbackStore == nil || req == nil {
		return nil
	}

	tconst := req.Tconst
	season := req.Season
	episode := req.Episode

	if tconst == "" && req.ItemId != "" {
		tconst, season, episode = parseItemId(req.ItemId)
	}

	if tconst == "" {
		return fmt.Errorf("missing tconst or item_id")
	}

	// If all requested, or if Next Up, or if movie: remove all progress for this show/movie
	if req.All || req.IsNextUp || (season == 0 && episode == 0) {
		return s.playbackStore.DeleteShowProgress(tconst)
	}

	// Delete specific episode record
	return s.playbackStore.DeleteItemProgress(tconst, season, episode)
}

func cleanVideoCodec(c string) string {
	lc := strings.ToLower(c)
	if strings.Contains(lc, "h264") || strings.Contains(lc, "avc") {
		return "H.264"
	}
	if strings.Contains(lc, "h265") || strings.Contains(lc, "hevc") {
		return "HEVC"
	}
	if strings.Contains(lc, "av1") {
		return "AV1"
	}
	if strings.Contains(lc, "vp9") {
		return "VP9"
	}
	if strings.Contains(lc, "mpeg4") || strings.Contains(lc, "xvid") {
		return "MPEG-4"
	}
	if idx := strings.Index(c, ","); idx > 0 {
		c = c[:idx]
	}
	c = strings.TrimPrefix(c, "video/x-")
	c = strings.TrimPrefix(c, "video/")
	return strings.ToUpper(strings.TrimSpace(c))
}

func cleanAudioCodec(c string) string {
	lc := strings.ToLower(c)
	if strings.Contains(lc, "eac3") || strings.Contains(lc, "e-ac3") {
		return "E-AC3"
	}
	if strings.Contains(lc, "ac3") || strings.Contains(lc, "ac-3") {
		return "AC3"
	}
	if strings.Contains(lc, "dts-hd") || strings.Contains(lc, "dtshd") {
		return "DTS-HD"
	}
	if strings.Contains(lc, "dts") {
		return "DTS"
	}
	if strings.Contains(lc, "truehd") {
		return "TrueHD"
	}
	if strings.Contains(lc, "flac") {
		return "FLAC"
	}
	if strings.Contains(lc, "opus") {
		return "Opus"
	}
	if strings.Contains(lc, "aac") {
		return "AAC"
	}
	if strings.Contains(lc, "mp3") {
		return "MP3"
	}
	if idx := strings.Index(c, ","); idx > 0 {
		c = c[:idx]
	}
	c = strings.TrimPrefix(c, "audio/x-")
	c = strings.TrimPrefix(c, "audio/")
	return strings.ToUpper(strings.TrimSpace(c))
}

