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
	// S01E02, S1.E02, S02.EP04, S02-E04
	reSXXEYY = regexp.MustCompile(`(?i)(?:^|[\s._\-\[(])s0*(\d{1,2})[._\-\s]*(?:e|ep)0*(\d{1,3})(?:[\s._\-\])]|$)`)

	// S02E01-E02, S02E01-02, S02.EP01-EP02
	reSXXEYYRange = regexp.MustCompile(`(?i)(?:^|[\s._\-\[(])s0*(\d{1,2})[._\-\s]*(?:e|ep)0*(\d{1,3})[._\-\s]*(?:-|до|по|–|—)[._\-\s]*(?:e|ep)?0*(\d{1,3})(?:[\s._\-\])]|$)`)

	// 1x02, 01x02
	reNxNN = regexp.MustCompile(`(?i)(?:^|[\s._\-\[(])(\d{1,2})x0*(\d{1,3})(?:[\s._\-\])]|$)`)

	// 1x01-02, 1x01-1x02
	reNxNNRange = regexp.MustCompile(`(?i)(?:^|[\s._\-\[(])(\d{1,2})x0*(\d{1,3})[._\-\s]*(?:-|до|по|–|—)[._\-\s]*(?:\d{1,2}x)?0*(\d{1,3})(?:[\s._\-\])]|$)`)

	// Season 2 Episode 4, Сезон 2 Серия 4
	reWordsSeasonEp = regexp.MustCompile(`(?i)(?:season|сезон)[._\-\s]*0*(\d{1,2})[._\-\s]*(?:episode|эпизод|серия|ep)[._\-\s]*0*(\d{1,3})`)

	// 2 сезон 4 серия
	rePrefixSeasonEp = regexp.MustCompile(`(?i)0*(\d{1,2})[._\-\s]*(?:-?(?:й|ый|ой|ий))?[._\-\s]*(?:сезон|season)[._\-\s]*0*(\d{1,3})[._\-\s]*(?:серия|серии|episode|ep)`)

	// 3-digit episode format: e.g. 204.mkv, Show.204.Commendatori.mkv
	reThreeDigit = regexp.MustCompile(`(?:^|[\s._\-\[(])([1-9])(\d{2})(?:[\s._\-\])]|$)`)

	// Directory season patterns:
	// Season 2, Season.02, Season_2, Сезон 2, Сезон.2, Сезон_2, Сезон: 2
	reDirSeasonWord = regexp.MustCompile(`(?i)(?:^|[._\-\s/\\\[(:])(?:сезон|season)[._\-\s:]*0*(\d{1,2})(?:$|[._\-\s/\\\]\),])`)

	// 1 сезон, 2 сезон, 2-й сезон, 2-ой сезон, 02 сезон
	reDirPrefixSeason = regexp.MustCompile(`(?i)(?:^|[._\-\s/\\\[(:])0*(\d{1,2})(?:-?(?:й|ый|ой|ий|th|nd|rd|st))?[._\-\s]*(?:сезон|season)(?:$|[._\-\s/\\\]\),])`)

	// S02, S2, [S02], (S02), S2_1080p, Show.S02.1080p
	reDirSXX = regexp.MustCompile(`(?i)(?:^|[._\-\s/\\\[(:])[sS]0*(\d{1,2})(?:$|[._\-\s/\\\]\),])`)

	// Roman numerals in folder: Сезон I, Сезон II, II сезон
	reDirRomanSeason = regexp.MustCompile(`(?i)(?:^|[._\-\s/\\\[(:])(?:сезон|season)[._\-\s:]*(X|IX|VIII|VII|VI|V|IV|III|II|I)(?:$|[._\-\s/\\\]\),])`)
	reDirRomanPrefix = regexp.MustCompile(`(?i)(?:^|[._\-\s/\\\[(:])(X|IX|VIII|VII|VI|V|IV|III|II|I)[._\-\s]*(?:сезон|season)(?:$|[._\-\s/\\\]\),])`)

	// Multi-season pack title detector (e.g. S01-05, 1-6 сезон)
	reMultiSeasonRange = regexp.MustCompile(`(?i)(?:s0*(\d{1,2})[._\-\s]*(?:-|до|по|–|—)[._\-\s]*s?0*(\d{1,2})|(?:сезон|season)?[._\-\s]*0*(\d{1,2})[._\-\s]*(?:-|до|по|–|—)[._\-\s]*0*(\d{1,2})[._\-\s]*(?:сезон|season))`)

	// Episode in filename:
	reEpisodeRange      = regexp.MustCompile(`(?i)(?:^|[\s._\-\[(])(?:e|ep|сери[яи]|эпизод)?[._\-\s]*0*(\d{1,3})[._\-\s]*(?:-|до|по|–|—)[._\-\s]*(?:e|ep|сери[яи]|эпизод)?0*(\d{1,3})(?:[\s._\-\])]|$)`)
	reEpisodeWord       = regexp.MustCompile(`(?i)(?:серия|серии|эпизод|ep|episode)[._\s-]*0*(\d{1,3})`)
	reEpisodeWordBefore = regexp.MustCompile(`(?i)0*([1-9]\d{0,2})[._\-\s-]*(?:серия|серии|эпизод|выпуск)`)
	reBracketEp         = regexp.MustCompile(`[\[\(]0*([1-9]\d{0,2})[\]\)]`)
	reSingleE           = regexp.MustCompile(`(?i)[._\-\s\[(]e0*([1-9]\d{0,2})(?:[._\-\s\])]|$)`)
	reLeadingEp         = regexp.MustCompile(`^0*([1-9]\d{0,2})(?:[\s._\-]|$)`)
	reTrailingEp        = regexp.MustCompile(`[\s._\-]0*([1-9]\d{0,2})$`)
	reMiddleEp          = regexp.MustCompile(`[._\-\s]0*([1-9]\d{0,2})[._\-\s]`)
)

var romanNumerals = map[string]int{
	"I": 1, "II": 2, "III": 3, "IV": 4, "V": 5,
	"VI": 6, "VII": 7, "VIII": 8, "IX": 9, "X": 10,
}

var videoExtensions = map[string]bool{
	".mkv":  true,
	".mp4":  true,
	".avi":  true,
	".mov":  true,
	".ts":   true,
	".m2ts": true,
	".webm": true,
	".m4v":  true,
}

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
		client:  &http.Client{Timeout: 35 * time.Second},
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

type parsedVideo struct {
	stat    TorrentFileStat
	season  int
	epStart int
	epEnd   int
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

	// Case 1: Movie request (season == 0 && episode == 0)
	if season == 0 && episode == 0 {
		sort.Slice(videos, func(i, j int) bool {
			return videos[i].Length > videos[j].Length
		})
		return &videos[0], nil
	}

	// Case 2: Series episode requested (season > 0, episode > 0)
	parsed := make([]parsedVideo, len(videos))
	detectedSeasons := make(map[int]bool)
	for i, v := range videos {
		s, eStart, eEnd := ParseSeasonEpisodeRange(v.Path)
		parsed[i] = parsedVideo{
			stat:    v,
			season:  s,
			epStart: eStart,
			epEnd:   eEnd,
		}
		if s > 0 {
			detectedSeasons[s] = true
		}
	}

	isMultiSeason := len(detectedSeasons) > 1

	titleSeason := extractSingleSeasonFromTitle(record.Title)

	// If no season detected in files and this is not a multi-season pack, check torrent title
	if len(detectedSeasons) == 0 && titleSeason > 0 {
		for i := range parsed {
			if parsed[i].season == 0 {
				parsed[i].season = titleSeason
			}
		}
		detectedSeasons[titleSeason] = true
	}

	// Pass 1: Exact season and episode range match
	for _, pv := range parsed {
		if pv.season == season && episode >= pv.epStart && episode <= pv.epEnd && pv.epStart > 0 {
			return &pv.stat, nil
		}
	}

	// Pass 2: In-season files matching
	var seasonVideos []parsedVideo
	for _, pv := range parsed {
		if pv.season == season {
			seasonVideos = append(seasonVideos, pv)
		}
	}

	if len(seasonVideos) > 0 {
		// Pass 2a: Fuzzy episode token match within season files
		epPadded := fmt.Sprintf("%02d", episode)
		epRaw := fmt.Sprintf("%d", episode)
		for _, sv := range seasonVideos {
			base := strings.ToLower(filepath.Base(sv.stat.Path))
			if strings.Contains(base, "e"+epPadded) ||
				strings.Contains(base, "ep"+epPadded) ||
				strings.Contains(base, "e"+epRaw) ||
				strings.Contains(base, "серия "+epRaw) ||
				strings.Contains(base, "серия "+epPadded) ||
				strings.Contains(base, epRaw+" серия") ||
				strings.Contains(base, epPadded+" серия") {
				return &sv.stat, nil
			}
		}

		// Pass 2b: Continuous / absolute episode numbering offset
		// e.g. Season 2 has files 14.mkv..26.mkv, so minEp=14, target absolute episode is minEp + episode - 1
		minEp := 999999
		hasNumberedEps := false
		for _, sv := range seasonVideos {
			if sv.epStart > 0 {
				hasNumberedEps = true
				if sv.epStart < minEp {
					minEp = sv.epStart
				}
			}
		}
		if hasNumberedEps && minEp > 1 {
			targetAbsEp := minEp + episode - 1
			for _, sv := range seasonVideos {
				if targetAbsEp >= sv.epStart && targetAbsEp <= sv.epEnd {
					return &sv.stat, nil
				}
			}
		}

		// Pass 2c: Natural sorting fallback within the season folder
		sort.Slice(seasonVideos, func(i, j int) bool {
			return naturalLess(seasonVideos[i].stat.Path, seasonVideos[j].stat.Path)
		})
		if episode >= 1 && episode <= len(seasonVideos) {
			picked := seasonVideos[episode-1]
			log.Printf("[torrclient] Matched S%02dE%02d by natural order (%d/%d) in season %d: %s",
				season, episode, episode, len(seasonVideos), season, picked.stat.Path)
			return &picked.stat, nil
		}
	}

	// CRITICAL GUARD: If this is a multi-season pack, NEVER allow cross-season fallback!
	if isMultiSeason {
		seasonsList := make([]int, 0, len(detectedSeasons))
		for s := range detectedSeasons {
			seasonsList = append(seasonsList, s)
		}
		sort.Ints(seasonsList)
		return nil, fmt.Errorf("episode S%02dE%02d not found in multi-season pack %q (available seasons: %v)",
			season, episode, record.Title, seasonsList)
	}

	// Pass 3: Single-season torrent fallback (ONLY if not multi-season and no other season detected)
	// If season > 1, we only allow fallback if torrent title verified this season
	if season > 1 && titleSeason != season {
		return nil, fmt.Errorf("episode S%02dE%02d not found in torrent %q (season %d not verified)",
			season, episode, record.Title, season)
	}

	for _, pv := range parsed {
		if (pv.season == 0 || pv.season == season) && episode >= pv.epStart && episode <= pv.epEnd && pv.epStart > 0 {
			return &pv.stat, nil
		}
	}

	// Pass 3b: Fuzzy token on all videos if single season
	epPadded := fmt.Sprintf("%02d", episode)
	epRaw := fmt.Sprintf("%d", episode)
	for _, v := range videos {
		base := strings.ToLower(filepath.Base(v.Path))
		if strings.Contains(base, "e"+epPadded) || strings.Contains(base, "ep"+epPadded) || strings.Contains(base, "e"+epRaw) {
			return &v, nil
		}
	}

	// Pass 3c: Natural sort order for single season pack (only if multiple episodes exist or title verified season)
	if len(videos) > 1 || titleSeason > 0 {
		sort.Slice(videos, func(i, j int) bool {
			return naturalLess(videos[i].Path, videos[j].Path)
		})
		if episode >= 1 && episode <= len(videos) {
			picked := videos[episode-1]
			log.Printf("[torrclient] Matched S%02dE%02d by natural order (%d/%d) in single-season torrent: %s",
				season, episode, episode, len(videos), picked.Path)
			return &picked, nil
		}
	}

	return nil, fmt.Errorf("episode S%02dE%02d not found in torrent %q", season, episode, record.Title)
}

// ParseSeasonEpisode parses season and episode number from path
func ParseSeasonEpisode(path string) (int, int) {
	s, eStart, _ := ParseSeasonEpisodeRange(path)
	return s, eStart
}

// ParseSeasonEpisodeRange parses season, episode start and episode end from path
func ParseSeasonEpisodeRange(path string) (int, int, int) {
	cleanPath := filepath.ToSlash(path)
	base := filepath.Base(cleanPath)

	// 1. SXXEYY Range in base filename: S02E01-E02, S02E01-02
	if m := reSXXEYYRange.FindStringSubmatch(base); len(m) >= 4 {
		s, _ := strconv.Atoi(m[1])
		e1, _ := strconv.Atoi(m[2])
		e2, _ := strconv.Atoi(m[3])
		if e2 < e1 {
			e1, e2 = e2, e1
		}
		return s, e1, e2
	}

	// 2. SXXEYY in base filename: S01E02, S02.EP04
	if m := reSXXEYY.FindStringSubmatch(base); len(m) >= 3 {
		s, _ := strconv.Atoi(m[1])
		e, _ := strconv.Atoi(m[2])
		return s, e, e
	}

	// 3. NxNN Range in base filename: 1x01-02, 1x01-1x02
	if m := reNxNNRange.FindStringSubmatch(base); len(m) >= 4 {
		s, _ := strconv.Atoi(m[1])
		e1, _ := strconv.Atoi(m[2])
		e2, _ := strconv.Atoi(m[3])
		if e2 < e1 {
			e1, e2 = e2, e1
		}
		return s, e1, e2
	}

	// 4. 1x02 in base filename
	if m := reNxNN.FindStringSubmatch(base); len(m) >= 3 {
		s, _ := strconv.Atoi(m[1])
		e, _ := strconv.Atoi(m[2])
		return s, e, e
	}

	// 5. Words Season + Ep: Season 2 Episode 4, Сезон 2 Серия 4
	if m := reWordsSeasonEp.FindStringSubmatch(base); len(m) >= 3 {
		s, _ := strconv.Atoi(m[1])
		e, _ := strconv.Atoi(m[2])
		return s, e, e
	}

	// 6. Prefix Season + Ep: 2 сезон 4 серия
	if m := rePrefixSeasonEp.FindStringSubmatch(base); len(m) >= 3 {
		s, _ := strconv.Atoi(m[1])
		e, _ := strconv.Atoi(m[2])
		return s, e, e
	}

	// 7. Check Directory for Season
	s := parseSeasonFromDir(filepath.Dir(cleanPath))
	if s == 0 {
		// Try parsing from cleanPath itself
		s = parseSeasonFromDir(cleanPath)
	}

	// 8. 3-digit episode format (e.g. 204.mkv) if season not found in directory
	if s == 0 {
		if m := reThreeDigit.FindStringSubmatch(base); len(m) >= 3 {
			fullNum, _ := strconv.Atoi(m[1] + m[2])
			if fullNum != 264 && fullNum != 265 && fullNum != 480 && fullNum != 576 && fullNum != 720 {
				sNum, _ := strconv.Atoi(m[1])
				eNum, _ := strconv.Atoi(m[2])
				if sNum > 0 && sNum <= 30 && eNum > 0 && eNum <= 50 {
					return sNum, eNum, eNum
				}
			}
		}
	}

	// 9. Parse episode from base filename
	// Check episode range first: 01-02.mkv, 01-02 серии.mkv
	if em := reEpisodeRange.FindStringSubmatch(base); len(em) >= 3 {
		e1, _ := strconv.Atoi(em[1])
		e2, _ := strconv.Atoi(em[2])
		if e1 > 0 && e2 > 0 {
			if e2 < e1 {
				e1, e2 = e2, e1
			}
			return s, e1, e2
		}
	}

	e := 0
	if em := reEpisodeWord.FindStringSubmatch(base); len(em) >= 2 {
		e, _ = strconv.Atoi(em[1])
	} else if em := reEpisodeWordBefore.FindStringSubmatch(base); len(em) >= 2 {
		e, _ = strconv.Atoi(em[1])
	} else if em := reSingleE.FindStringSubmatch(base); len(em) >= 2 {
		e, _ = strconv.Atoi(em[1])
	} else if em := reBracketEp.FindStringSubmatch(base); len(em) >= 2 {
		epNum, _ := strconv.Atoi(em[1])
		if epNum > 0 && epNum < 300 {
			e = epNum
		}
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
		} else if em := reMiddleEp.FindStringSubmatch(noExt); len(em) >= 2 {
			epNum, _ := strconv.Atoi(em[1])
			isCodecOrRes := epNum == 264 || epNum == 265 || epNum == 720 || epNum == 1080 || epNum == 2160 || epNum == 480 || epNum == 576
			if !isCodecOrRes {
				e = epNum
			}
		}
	}

	return s, e, e
}

func parseSeasonFromDir(dirPath string) int {
	cleanDir := filepath.ToSlash(dirPath)
	if cleanDir == "." || cleanDir == "/" || cleanDir == "" {
		return 0
	}
	parts := strings.Split(cleanDir, "/")
	// Check from deepest directory level to highest
	for i := len(parts) - 1; i >= 0; i-- {
		part := parts[i]

		// S01, S02, [S02], Show.S02.1080p
		if sm := reDirSXX.FindStringSubmatch(part); len(sm) >= 2 {
			if s, err := strconv.Atoi(sm[1]); err == nil && s > 0 {
				return s
			}
		}

		// Season 2, Season.02, Season_2, Сезон 2, Сезон.2, Сезон_2
		if sm := reDirSeasonWord.FindStringSubmatch(part); len(sm) >= 2 {
			if s, err := strconv.Atoi(sm[1]); err == nil && s > 0 {
				return s
			}
		}

		// 1 сезон, 2 сезон, 2-й сезон, 2-ой сезон, 02 сезон
		if sm := reDirPrefixSeason.FindStringSubmatch(part); len(sm) >= 2 {
			if s, err := strconv.Atoi(sm[1]); err == nil && s > 0 {
				return s
			}
		}

		// Roman numerals: Сезон II, II сезон
		if sm := reDirRomanSeason.FindStringSubmatch(part); len(sm) >= 2 {
			if s, ok := romanNumerals[strings.ToUpper(sm[1])]; ok && s > 0 {
				return s
			}
		}
		if sm := reDirRomanPrefix.FindStringSubmatch(part); len(sm) >= 2 {
			if s, ok := romanNumerals[strings.ToUpper(sm[1])]; ok && s > 0 {
				return s
			}
		}
	}
	return 0
}

func extractSingleSeasonFromTitle(title string) int {
	if title == "" {
		return 0
	}
	// If title represents multiple seasons (e.g. S01-05 or 1-6 сезон), it's not a single season!
	if reMultiSeasonRange.MatchString(title) {
		return 0
	}

	// Try S02, S2
	if m := reDirSXX.FindStringSubmatch(title); len(m) >= 2 {
		if s, err := strconv.Atoi(m[1]); err == nil && s > 0 {
			return s
		}
	}
	// Try Season 2, Сезон 2, Сезон: 2
	if m := reDirSeasonWord.FindStringSubmatch(title); len(m) >= 2 {
		if s, err := strconv.Atoi(m[1]); err == nil && s > 0 {
			return s
		}
	}
	// Try 2 сезон, 2-й сезон
	if m := reDirPrefixSeason.FindStringSubmatch(title); len(m) >= 2 {
		if s, err := strconv.Atoi(m[1]); err == nil && s > 0 {
			return s
		}
	}
	return 0
}

func naturalLess(a, b string) bool {
	aChunks := splitIntoChunks(strings.ToLower(a))
	bChunks := splitIntoChunks(strings.ToLower(b))
	minLen := len(aChunks)
	if len(bChunks) < minLen {
		minLen = len(bChunks)
	}
	for i := 0; i < minLen; i++ {
		ac, bc := aChunks[i], bChunks[i]
		if ac == bc {
			continue
		}
		aNum, aErr := strconv.Atoi(ac)
		bNum, bErr := strconv.Atoi(bc)
		if aErr == nil && bErr == nil {
			if aNum != bNum {
				return aNum < bNum
			}
		} else {
			if ac != bc {
				return ac < bc
			}
		}
	}
	return len(aChunks) < len(bChunks)
}

func splitIntoChunks(s string) []string {
	var chunks []string
	var current strings.Builder
	isDigit := false

	for i, r := range s {
		d := r >= '0' && r <= '9'
		if i == 0 {
			isDigit = d
			current.WriteRune(r)
		} else if d == isDigit {
			current.WriteRune(r)
		} else {
			chunks = append(chunks, current.String())
			current.Reset()
			isDigit = d
			current.WriteRune(r)
		}
	}
	if current.Len() > 0 {
		chunks = append(chunks, current.String())
	}
	return chunks
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
