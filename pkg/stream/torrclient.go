package stream

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

var (
	reSXXEYY      = regexp.MustCompile(`(?i)[sS](\d{1,2})[eE](\d{1,3})`)
	reNxNN        = regexp.MustCompile(`(?i)(?:^|[\s._\-\[(])(\d{1,2})x(\d{1,3})(?:[\s._\-\])]|$)`)
	reSeasonDir   = regexp.MustCompile(`(?i)(?:сезон|season)\s*0*(\d{1,2})|(?:^|[/\\_\s\-])[sS]0*(\d{1,2})(?:[/\\_\s\-]|\b)`)
	reEpisodeWord = regexp.MustCompile(`(?i)(?:серия|эпизод|ep|episode)[._\s-]*0*(\d{1,3})`)
	reLeadingEp   = regexp.MustCompile(`^0*([1-9]\d{0,2})(?:[\s._\-]|$)`)
	reTrailingEp  = regexp.MustCompile(`[\s._\-]0*([1-9]\d{0,2})$`)

	videoExtensions = map[string]bool{
		".mkv":  true,
		".mp4":  true,
		".avi":  true,
		".mov":  true,
		".ts":   true,
		".m2ts": true,
		".webm": true,
		".m4v":  true,
	}
)

type TorrentFileStat struct {
	ID     int    `json:"id"`
	Path   string `json:"path"`
	Length int64  `json:"length"`
}

type TorrentRecord struct {
	Title            string            `json:"title"`
	Hash             string            `json:"hash"`
	TorrentURL       string            `json:"torrent_url,omitempty"`
	FileStats        []TorrentFileStat `json:"file_stats,omitempty"`
	Stat             int               `json:"stat,omitempty"`
	StatString       string            `json:"stat_string,omitempty"`
	LoadedSize       int64             `json:"loaded_size,omitempty"`
	TorrentSize      int64             `json:"torrent_size,omitempty"`
	DownloadSpeed    float64           `json:"download_speed,omitempty"`
	ConnectedSeeders int               `json:"connected_seeders,omitempty"`
}

type ProbeTrack struct {
	Index        int    `json:"Index"`
	Type         string `json:"Type"` // "video", "audio", "subtitle"
	Codec        string `json:"Codec"`
	Language     string `json:"Language,omitempty"`
	Title        string `json:"Title,omitempty"`
	Channels     int    `json:"Channels,omitempty"`
	Width        int    `json:"Width,omitempty"`
	Height       int    `json:"Height,omitempty"`
	FrameRateNum int    `json:"FrameRateNum,omitempty"`
	FrameRateDen int    `json:"FrameRateDen,omitempty"`
}

type ProbeInfo struct {
	Container  string       `json:"Container"`
	DurationNS int64        `json:"DurationNS"`
	FileSize   int64        `json:"FileSize"`
	Tracks     []ProbeTrack `json:"Tracks"`
}

type TorrClient struct {
	baseURL string
	client  *http.Client
}

func NewTorrClient(baseURL string) *TorrClient {
	if baseURL == "" {
		baseURL = "http://127.0.0.1:8092"
	}
	baseURL = strings.TrimRight(baseURL, "/")
	return &TorrClient{
		baseURL: baseURL,
		client:  &http.Client{Timeout: 10 * time.Second},
	}
}

// AddTorrent adds or retrieves a torrent from TorrServer
func (c *TorrClient) AddTorrent(ctx context.Context, link, title string) (*TorrentRecord, error) {
	reqBody := map[string]interface{}{
		"action":     "add",
		"link":       link,
		"title":      title,
		"save_to_db": true,
	}
	data, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/torrents", bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("torrserver add error: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("torrserver returned status %d", resp.StatusCode)
	}

	var rec TorrentRecord
	if err := json.NewDecoder(resp.Body).Decode(&rec); err != nil {
		return nil, fmt.Errorf("failed to decode torrent response: %w", err)
	}

	return &rec, nil
}

// GetTorrent fetches the current status and file tree of a torrent
func (c *TorrClient) GetTorrent(ctx context.Context, hash string) (*TorrentRecord, error) {
	reqBody := map[string]interface{}{
		"action": "get",
		"hash":   hash,
	}
	data, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/torrents", bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("torrserver get error: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("torrserver get returned status %d", resp.StatusCode)
	}

	var rec TorrentRecord
	if err := json.NewDecoder(resp.Body).Decode(&rec); err != nil {
		return nil, fmt.Errorf("failed to decode torrent record: %w", err)
	}

	return &rec, nil
}

// WaitMetadata polls TorrServer until file_stats is populated
func (c *TorrClient) WaitMetadata(ctx context.Context, hash string, timeout time.Duration) (*TorrentRecord, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		rec, err := c.GetTorrent(ctx, hash)
		if err == nil && len(rec.FileStats) > 0 {
			return rec, nil
		}

		time.Sleep(500 * time.Millisecond)
	}

	return c.GetTorrent(ctx, hash)
}

// DropTorrent drops active torrent swarm from memory
func (c *TorrClient) DropTorrent(ctx context.Context, hash string) error {
	reqBody := map[string]interface{}{
		"action": "drop",
		"hash":   hash,
	}
	data, _ := json.Marshal(reqBody)
	req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/torrents", bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return nil
}

// ProbeFile queries GStreamer probe for container tracks
func (c *TorrClient) ProbeFile(ctx context.Context, hash string, fileId int) (*ProbeInfo, error) {
	url := fmt.Sprintf("%s/gst/%s/probe?id=%d", c.baseURL, hash, fileId)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("probe request error: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("probe returned status %d", resp.StatusCode)
	}

	var info ProbeInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil, fmt.Errorf("failed to decode probe info: %w", err)
	}

	return &info, nil
}

// IsVideoFile checks if file has a known video extension and is not sample
func IsVideoFile(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	if !videoExtensions[ext] {
		return false
	}
	lower := strings.ToLower(path)
	if strings.Contains(lower, "sample") && !strings.Contains(lower, "sample.") {
		// e.g. sample.mkv or /sample/ folder
		return false
	}
	return true
}

// MatchFile finds the targeted file for movie or series episode
func (c *TorrClient) MatchFile(record *TorrentRecord, season, episode int) (*TorrentFileStat, error) {
	if len(record.FileStats) == 0 {
		return nil, fmt.Errorf("torrent has no files")
	}

	// Filter video files
	var videos []TorrentFileStat
	for _, f := range record.FileStats {
		if IsVideoFile(f.Path) {
			videos = append(videos, f)
		}
	}
	if len(videos) == 0 {
		// Fallback to all files if no video extension matched
		videos = record.FileStats
	}

	// Case 1: Series episode requested (season > 0, episode > 0)
	if season > 0 && episode > 0 {
		// Exact match by parsed season and episode
		for _, v := range videos {
			s, e := ParseSeasonEpisode(v.Path)
			if s == season && e == episode {
				return &v, nil
			}
		}

		// Secondary match: if season was 0 in path (e.g. single season pack), match episode only
		for _, v := range videos {
			s, e := ParseSeasonEpisode(v.Path)
			if (s == 0 || s == season) && e == episode {
				return &v, nil
			}
		}

		// Tertiary match: check if episode number appears as a word/token in filename
		epPadded := fmt.Sprintf("%02d", episode)
		epRaw := fmt.Sprintf("%d", episode)
		for _, v := range videos {
			base := strings.ToLower(filepath.Base(v.Path))
			if strings.Contains(base, "e"+epPadded) || strings.Contains(base, "ep"+epPadded) || strings.Contains(base, "e"+epRaw) {
				return &v, nil
			}
		}

		log.Printf("[torrclient] Warning: episode S%02dE%02d not found in %d files, falling back to largest video", season, episode, len(videos))
	}

	// Case 2: Movie or series fallback: pick largest video file
	sort.Slice(videos, func(i, j int) bool {
		return videos[i].Length > videos[j].Length
	})

	return &videos[0], nil
}

// ParseSeasonEpisode parses season and episode number from path
func ParseSeasonEpisode(path string) (int, int) {
	cleanPath := filepath.ToSlash(path)
	base := filepath.Base(cleanPath)

	// 1. S01E02 in base filename
	if m := reSXXEYY.FindStringSubmatch(base); len(m) >= 3 {
		s, _ := strconv.Atoi(m[1])
		e, _ := strconv.Atoi(m[2])
		return s, e
	}

	// 2. 1x02 in base filename
	if m := reNxNN.FindStringSubmatch(base); len(m) >= 3 {
		s, _ := strconv.Atoi(m[1])
		e, _ := strconv.Atoi(m[2])
		return s, e
	}

	// 3. Check for season in directory path
	s := 0
	dir := filepath.Dir(cleanPath)
	if dir != "." && dir != "/" {
		parts := strings.Split(dir, "/")
		for i := len(parts) - 1; i >= 0; i-- {
			part := parts[i]
			if sm := reSeasonDir.FindStringSubmatch(part); len(sm) > 0 {
				for k := 1; k < len(sm); k++ {
					if sm[k] != "" {
						s, _ = strconv.Atoi(sm[k])
						break
					}
				}
				if s > 0 {
					break
				}
			}
		}
	}

	if s == 0 {
		if sm := reSeasonDir.FindStringSubmatch(cleanPath); len(sm) > 0 {
			for k := 1; k < len(sm); k++ {
				if sm[k] != "" {
					s, _ = strconv.Atoi(sm[k])
					break
				}
			}
		}
	}

	// 4. Parse episode from base filename
	e := 0
	if em := reEpisodeWord.FindStringSubmatch(base); len(em) >= 2 {
		e, _ = strconv.Atoi(em[1])
	} else if em := reLeadingEp.FindStringSubmatch(base); len(em) >= 2 {
		e, _ = strconv.Atoi(em[1])
	} else {
		noExt := strings.TrimSuffix(base, filepath.Ext(base))
		if em := reTrailingEp.FindStringSubmatch(noExt); len(em) >= 2 {
			epNum, _ := strconv.Atoi(em[1])
			isCodecOrRes := epNum == 264 || epNum == 265 || epNum == 720 || epNum == 1080 || epNum == 2160 || epNum == 480 || epNum == 576
			if !isCodecOrRes {
				e = epNum
			}
		}
	}

	return s, e
}

// ExtractHashFromMagnet parses 40-char or 32-char infohash from magnet link
func ExtractHashFromMagnet(link string) string {
	if !strings.HasPrefix(link, "magnet:") {
		// Check if it's already a 40-char hex hash
		if len(link) == 40 {
			return strings.ToLower(link)
		}
		return ""
	}

	u, err := url.Parse(link)
	if err != nil {
		return ""
	}
	xt := u.Query().Get("xt")
	if strings.HasPrefix(xt, "urn:btih:") {
		return strings.ToLower(strings.TrimPrefix(xt, "urn:btih:"))
	}
	return ""
}
