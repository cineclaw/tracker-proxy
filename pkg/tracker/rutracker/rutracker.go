package rutracker

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/PuerkitoBio/goquery"
	"tracker-proxy/pkg/encoding"
	"tracker-proxy/pkg/flaresolverr"
	"tracker-proxy/pkg/models"
)

var (
	digitsRegex   = regexp.MustCompile(`\d+`)
	topicIDRegex  = regexp.MustCompile(`[?&]t=(\d+)`)
	infoHashRegex = regexp.MustCompile(`(?i)urn:btih:([a-f0-9]{40}|[a-z2-7]{32})`)
)

type Tracker struct {
	baseURL            string
	username           string
	password           string
	cookie             string
	userAgent          string
	flaresolverrClient *flaresolverr.Client
	client             *http.Client
	jar                *cookiejar.Jar
	enabled            bool

	stateMu    sync.RWMutex
	solveMu    sync.Mutex
	lastSolved time.Time
	lastLogin  time.Time
}

func New(baseURL, username, password, rawCookie, userAgent string, fsClient *flaresolverr.Client) *Tracker {
	if baseURL == "" {
		baseURL = "https://rutracker.org"
	}
	if userAgent == "" {
		userAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"
	}

	jar, _ := cookiejar.New(nil)
	t := &Tracker{
		baseURL:            strings.TrimRight(baseURL, "/"),
		username:           username,
		password:           password,
		cookie:             rawCookie,
		userAgent:          userAgent,
		flaresolverrClient: fsClient,
		jar:                jar,
		client: &http.Client{
			Jar:     jar,
			Timeout: 15 * time.Second,
		},
		enabled: true,
	}

	if rawCookie != "" {
		t.applyRawCookie(rawCookie)
	}

	return t
}

func (t *Tracker) applyRawCookie(raw string) {
	u, err := url.Parse(t.baseURL)
	if err != nil {
		return
	}
	parts := strings.Split(raw, ";")
	var cookies []*http.Cookie
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		kv := strings.SplitN(part, "=", 2)
		if len(kv) == 2 {
			cookies = append(cookies, &http.Cookie{
				Name:  strings.TrimSpace(kv[0]),
				Value: strings.TrimSpace(kv[1]),
			})
		}
	}
	t.jar.SetCookies(u, cookies)
}

func (t *Tracker) setOrReplaceCookie(name, value string) {
	u, _ := url.Parse(t.baseURL)
	if u != nil {
		t.jar.SetCookies(u, []*http.Cookie{
			{
				Name:  name,
				Value: value,
				Path:  "/",
			},
		})
	}

	// Also update t.cookie string representation
	prefix := name + "="
	parts := strings.Split(t.cookie, ";")
	found := false
	var newParts []string
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if strings.HasPrefix(part, prefix) {
			newParts = append(newParts, fmt.Sprintf("%s=%s", name, value))
			found = true
		} else {
			newParts = append(newParts, part)
		}
	}
	if !found {
		newParts = append(newParts, fmt.Sprintf("%s=%s", name, value))
	}
	t.cookie = strings.Join(newParts, "; ")
}

func (t *Tracker) Name() string {
	return "rutracker"
}

func (t *Tracker) IsEnabled() bool {
	return t.enabled
}

func (t *Tracker) SetEnabled(enabled bool) {
	t.enabled = enabled
}

func (t *Tracker) EnsureClearance(ctx context.Context) error {
	if t.flaresolverrClient == nil {
		return fmt.Errorf("flaresolverr client not configured")
	}

	t.solveMu.Lock()
	defer t.solveMu.Unlock()

	t.stateMu.RLock()
	recent := time.Since(t.lastSolved) < 30*time.Second && !t.lastSolved.IsZero()
	t.stateMu.RUnlock()
	if recent {
		return nil
	}

	log.Printf("[rutracker] Requesting Cloudflare clearance from FlareSolverr...")
	targetURL := fmt.Sprintf("%s/forum/tracker.php", t.baseURL)
	solution, err := t.flaresolverrClient.Solve(ctx, targetURL)
	if err != nil {
		return fmt.Errorf("flaresolverr failed to solve clearance: %w", err)
	}

	t.stateMu.Lock()
	if solution.UserAgent != "" {
		t.userAgent = solution.UserAgent
	}

	for _, c := range solution.Cookies {
		t.setOrReplaceCookie(c.Name, c.Value)
	}
	t.lastSolved = time.Now()
	t.stateMu.Unlock()

	log.Printf("[rutracker] Cloudflare clearance successfully acquired via FlareSolverr (User-Agent: %s)", solution.UserAgent)
	return nil
}

func (t *Tracker) Login(ctx context.Context) error {
	t.stateMu.RLock()
	user := t.username
	pass := t.password
	ua := t.userAgent
	last := t.lastLogin
	t.stateMu.RUnlock()

	if user == "" || pass == "" {
		return nil
	}

	if time.Since(last) < 15*time.Minute && !last.IsZero() {
		return nil
	}

	loginURL := fmt.Sprintf("%s/forum/login.php", t.baseURL)
	formData := url.Values{
		"login_username": {user},
		"login_password": {pass},
		"login":          {"Вход"},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, loginURL, strings.NewReader(formData.Encode()))
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", ua)

	resp, err := t.client.Do(req)
	if err != nil {
		return fmt.Errorf("rutracker login request failed: %w", err)
	}
	defer resp.Body.Close()

	t.stateMu.Lock()
	t.lastLogin = time.Now()
	t.stateMu.Unlock()
	return nil
}

var (
	rutrackerMovieForums = []int{
		1457, 1940, 271, 313, 312, 2339, 252, 1950, 2200, 941,
		1666, 124, 352, 4, 1105, 1936, 314, 46,
	}

	rutrackerSeriesForums = []int{
		119, 1171, 2366, 1803, 842, 812, 81, 920, 921, 1106, 315,
	}
)

func buildRuTrackerForumFilter(mediaType string) string {
	var targetForums []int
	lower := strings.ToLower(strings.TrimSpace(mediaType))
	switch lower {
	case "movie":
		targetForums = rutrackerMovieForums
	case "tv", "tvseries", "series":
		targetForums = rutrackerSeriesForums
	default:
		seen := make(map[int]bool)
		for _, f := range rutrackerMovieForums {
			if !seen[f] {
				seen[f] = true
				targetForums = append(targetForums, f)
			}
		}
		for _, f := range rutrackerSeriesForums {
			if !seen[f] {
				seen[f] = true
				targetForums = append(targetForums, f)
			}
		}
	}

	var sb strings.Builder
	for _, f := range targetForums {
		sb.WriteString(fmt.Sprintf("&f[]=%d", f))
	}
	return sb.String()
}

func (t *Tracker) doSearch(ctx context.Context, queryEncoded, forumFilter string) (*http.Response, error) {
	searchURL := fmt.Sprintf("%s/forum/tracker.php?nm=%s%s", t.baseURL, queryEncoded, forumFilter)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, searchURL, nil)
	if err != nil {
		return nil, err
	}

	t.stateMu.RLock()
	ua := t.userAgent
	cookie := t.cookie
	t.stateMu.RUnlock()

	req.Header.Set("User-Agent", ua)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "ru,en;q=0.9")

	if cookie != "" {
		req.Header.Set("Cookie", cookie)
	}

	return t.client.Do(req)
}

func (t *Tracker) Search(ctx context.Context, query models.SearchQuery) ([]models.TorrentResult, error) {
	if !t.enabled {
		return nil, nil
	}

	// Encode query in Windows-1251
	queryEncoded, err := encoding.URLEncodeCP1251(query.Query)
	if err != nil {
		return nil, fmt.Errorf("failed to encode query to CP1251: %w", err)
	}

	forumFilter := buildRuTrackerForumFilter(query.Type)
	resp, err := t.doSearch(ctx, queryEncoded, forumFilter)

	if err != nil {
		return nil, fmt.Errorf("rutracker search request failed: %w", err)
	}

	// If 403 Forbidden and FlareSolverr is enabled, attempt automatic challenge resolution
	if resp.StatusCode == http.StatusForbidden && t.flaresolverrClient != nil {
		resp.Body.Close()
		log.Printf("[rutracker] Received HTTP 403 Forbidden; acquiring Cloudflare clearance via FlareSolverr...")

		solveCtx, cancel := context.WithTimeout(context.Background(), 75*time.Second)
		defer cancel()

		if solveErr := t.EnsureClearance(solveCtx); solveErr != nil {
			return nil, fmt.Errorf("failed to solve cloudflare challenge: %w", solveErr)
		}

		// Retry login if credentials exist and bb_session was not provided
		if t.username != "" && t.password != "" && !strings.Contains(t.cookie, "bb_session") {
			_ = t.Login(solveCtx)
		}

		// Retry search request with fresh clearance and User-Agent
		resp, err = t.doSearch(ctx, queryEncoded, forumFilter)
		if err != nil {
			return nil, fmt.Errorf("rutracker retry search failed: %w", err)
		}

	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusForbidden {
		return nil, fmt.Errorf("rutracker returned 403 Forbidden (Cloudflare Turnstile challenge requires valid cookie/clearance)")
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("rutracker returned HTTP %d", resp.StatusCode)
	}

	// Decode response from CP1251
	utf8Reader := encoding.NewCP1251Reader(resp.Body)
	doc, err := goquery.NewDocumentFromReader(utf8Reader)
	if err != nil {
		return nil, fmt.Errorf("failed to parse rutracker html: %w", err)
	}

	var results []models.TorrentResult

	doc.Find("table#tor-tbl tbody tr.hl-tr").Each(func(i int, s *goquery.Selection) {
		titleElem := s.Find("a.tt-text, a.t-title")
		if titleElem.Length() == 0 {
			return
		}

		title := strings.TrimSpace(titleElem.Text())
		topicHref, _ := titleElem.Attr("href")
		detailsURL := t.baseURL + "/forum/" + strings.TrimPrefix(topicHref, "/")

		// Extract topic ID
		id, _ := titleElem.Attr("data-topic_id")
		if id == "" {
			if m := topicIDRegex.FindStringSubmatch(topicHref); len(m) > 1 {
				id = m[1]
			}
		}

		// Download link (.torrent)
		downloadURL := ""
		if id != "" {
			downloadURL = fmt.Sprintf("%s/forum/dl.php?t=%s", t.baseURL, id)
		}

		// Category
		category := strings.TrimSpace(s.Find("a.f").Text())

		// Size from data-ts_text attribute
		sizeBytes := int64(0)
		sizeHuman := ""
		sizeCol := s.Find("td.tor-size")
		if tsText, exists := sizeCol.Attr("data-ts_text"); exists {
			sizeBytes, _ = strconv.ParseInt(strings.TrimSpace(tsText), 10, 64)
		}
		sizeHuman = strings.TrimSpace(sizeCol.Find("u").Text())
		if sizeHuman == "" {
			sizeHuman = strings.TrimSpace(sizeCol.Text())
		}

		// Seeds
		seeds := 0
		if seedElem := s.Find("b.seedmed, span.seedmed, td.seedmed").First(); seedElem.Length() > 0 {
			if num := digitsRegex.FindString(seedElem.Text()); num != "" {
				seeds, _ = strconv.Atoi(num)
			}
		} else if ts := s.Find("td.leechmed").Prev().AttrOr("data-ts_text", ""); ts != "" {
			seeds, _ = strconv.Atoi(ts)
		}

		// Leeches
		leeches := 0
		if leechElem := s.Find("td.leechmed, b.leechmed, span.leechmed").First(); leechElem.Length() > 0 {
			if num := digitsRegex.FindString(leechElem.Text()); num != "" {
				leeches, _ = strconv.Atoi(num)
			}
		}

		// Added date
		pubDate := time.Now()
		s.Find("td[data-ts_text]").Each(func(_ int, td *goquery.Selection) {
			if tsText, exists := td.Attr("data-ts_text"); exists && len(tsText) >= 10 {
				if ts, err := strconv.ParseInt(tsText, 10, 64); err == nil && ts > 1000000000 {
					pubDate = time.Unix(ts, 0)
				}
			}
		})

		results = append(results, models.TorrentResult{
			Tracker:     "rutracker",
			ID:          id,
			Title:       title,
			Category:    category,
			Size:        sizeBytes,
			SizeHuman:   sizeHuman,
			Seeds:       seeds,
			Leeches:     leeches,
			DownloadURL: downloadURL,
			DetailsURL:  detailsURL,
			PublishDate: pubDate,
		})

		if query.Limit > 0 && len(results) >= query.Limit {
			return
		}
	})

	return results, nil
}

// FetchInfoHash fetches the topic page from RuTracker and extracts the BitTorrent InfoHash.
func (t *Tracker) FetchInfoHash(ctx context.Context, id string) (string, error) {
	if id == "" {
		return "", fmt.Errorf("empty topic id")
	}

	topicURL := fmt.Sprintf("%s/forum/viewtopic.php?t=%s", t.baseURL, id)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, topicURL, nil)
	if err != nil {
		return "", err
	}

	t.stateMu.RLock()
	ua := t.userAgent
	cookie := t.cookie
	t.stateMu.RUnlock()

	req.Header.Set("User-Agent", ua)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "ru,en;q=0.9")
	if cookie != "" {
		req.Header.Set("Cookie", cookie)
	}

	resp, err := t.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("rutracker viewtopic request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusForbidden && t.flaresolverrClient != nil {
		_ = resp.Body.Close()
		if solveErr := t.EnsureClearance(ctx); solveErr == nil {
			t.stateMu.RLock()
			ua = t.userAgent
			cookie = t.cookie
			t.stateMu.RUnlock()

			reqRetry, err := http.NewRequestWithContext(ctx, http.MethodGet, topicURL, nil)
			if err == nil {
				reqRetry.Header.Set("User-Agent", ua)
				reqRetry.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
				reqRetry.Header.Set("Accept-Language", "ru,en;q=0.9")
				if cookie != "" {
					reqRetry.Header.Set("Cookie", cookie)
				}
				if retryResp, err := t.client.Do(reqRetry); err == nil {
					resp = retryResp
					defer resp.Body.Close()
				}
			}
		}
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("rutracker viewtopic returned status: %d", resp.StatusCode)
	}

	utf8Reader := encoding.NewCP1251Reader(resp.Body)
	doc, err := goquery.NewDocumentFromReader(utf8Reader)
	if err != nil {
		return "", fmt.Errorf("failed to parse rutracker viewtopic html: %w", err)
	}

	var foundHash string
	doc.Find("a.magnet-link, a[href^='magnet:?']").Each(func(i int, s *goquery.Selection) {
		if foundHash != "" {
			return
		}
		href, _ := s.Attr("href")
		if m := infoHashRegex.FindStringSubmatch(href); len(m) > 1 {
			foundHash = strings.ToLower(m[1])
		}
	})

	if foundHash == "" {
		return "", fmt.Errorf("no info_hash found on rutracker topic %s", id)
	}

	return foundHash, nil
}

