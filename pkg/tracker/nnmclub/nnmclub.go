package nnmclub

import (
	"context"
	"fmt"
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
	"tracker-proxy/pkg/models"
)

var (
	digitsRegex   = regexp.MustCompile(`\d+`)
	topicIDRegex  = regexp.MustCompile(`[?&]t=(\d+)`)
	dlIDRegex     = regexp.MustCompile(`[?&]id=(\d+)`)
	infoHashRegex = regexp.MustCompile(`(?i)urn:btih:([a-f0-9]{40}|[a-z2-7]{32})`)
)

type Tracker struct {
	baseURL   string
	username  string
	password  string
	cookie    string
	client    *http.Client
	jar       *cookiejar.Jar
	enabled   bool
	loginMu   sync.Mutex
	lastLogin time.Time
}

func New(baseURL, username, password, rawCookie string) *Tracker {
	if baseURL == "" {
		baseURL = "https://nnmclub.to"
	}
	jar, _ := cookiejar.New(nil)
	t := &Tracker{
		baseURL:  strings.TrimRight(baseURL, "/"),
		username: username,
		password: password,
		cookie:   rawCookie,
		jar:      jar,
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

func (t *Tracker) Name() string {
	return "nnmclub"
}

func (t *Tracker) IsEnabled() bool {
	return t.enabled
}

func (t *Tracker) SetEnabled(enabled bool) {
	t.enabled = enabled
}

func (t *Tracker) Login(ctx context.Context) error {
	t.loginMu.Lock()
	defer t.loginMu.Unlock()

	if t.username == "" || t.password == "" {
		return nil // no credentials provided, proceed with guest search
	}

	// Don't re-login if we logged in within the last 15 minutes
	if time.Since(t.lastLogin) < 15*time.Minute {
		return nil
	}

	loginURL := fmt.Sprintf("%s/forum/login.php", t.baseURL)
	formData := url.Values{
		"username":  {t.username},
		"password":  {t.password},
		"autologin": {"1"},
		"login":     {"Вход"},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, loginURL, strings.NewReader(formData.Encode()))
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36")

	resp, err := t.client.Do(req)
	if err != nil {
		return fmt.Errorf("nnmclub login request failed: %w", err)
	}
	defer resp.Body.Close()

	t.lastLogin = time.Now()
	return nil
}

var (
	nnmMovieForums = []int{
		954, 219, 1296, 227, 882, 225, 221, 1177, 912, 909, 884, 1150,
		1345, 1346, 891, 889, 682, 694, 1299, 1313, 1312, 1330, 1332,
		1337, 1339, 620, 624, 628,
	}

	nnmSeriesForums = []int{
		768, 769, 1219, 1221, 1220, 1344, 1265, 784, 774, 770, 780,
		781, 1300, 1322, 658, 232, 620, 624, 628,
	}
)

func buildNNMForumFilter(mediaType string) string {
	var targetForums []int
	lower := strings.ToLower(strings.TrimSpace(mediaType))
	switch lower {
	case "movie":
		targetForums = nnmMovieForums
	case "tv", "tvseries", "series":
		targetForums = nnmSeriesForums
	default:
		seen := make(map[int]bool)
		for _, f := range nnmMovieForums {
			if !seen[f] {
				seen[f] = true
				targetForums = append(targetForums, f)
			}
		}
		for _, f := range nnmSeriesForums {
			if !seen[f] {
				seen[f] = true
				targetForums = append(targetForums, f)
			}
		}
	}

	var sb strings.Builder
	for _, f := range targetForums {
		sb.WriteString(fmt.Sprintf("&f%%5B%%5D=%d", f))
	}
	return sb.String()
}

func (t *Tracker) Search(ctx context.Context, query models.SearchQuery) ([]models.TorrentResult, error) {
	if !t.enabled {
		return nil, nil
	}

	// Ensure login if credentials provided
	if t.username != "" && t.password != "" && t.cookie == "" {
		_ = t.Login(ctx)
	}

	// Encode search query in Windows-1251
	queryEncoded, err := encoding.URLEncodeCP1251(query.Query)
	if err != nil {
		return nil, fmt.Errorf("failed to encode query to CP1251: %w", err)
	}

	forumFilter := buildNNMForumFilter(query.Type)
	searchURL := fmt.Sprintf("%s/forum/tracker.php", t.baseURL)
	postBody := fmt.Sprintf("o=1&s=2&tm=-1&shf=1&sha=1&ta=-1&sns=-1&sds=-1%s&nm=%s&submit=%%CF%%EE%%E8%%F1%%EA", forumFilter, queryEncoded)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, searchURL, strings.NewReader(postBody))
	if err != nil {
		return nil, err
	}


	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "ru,en;q=0.9")

	resp, err := t.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("nnmclub search request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("nnmclub returned status: %d", resp.StatusCode)
	}

	// Wrap in CP1251 reader to get UTF-8 for HTML parser
	utf8Reader := encoding.NewCP1251Reader(resp.Body)
	doc, err := goquery.NewDocumentFromReader(utf8Reader)
	if err != nil {
		return nil, fmt.Errorf("failed to parse nnmclub html: %w", err)
	}

	var results []models.TorrentResult

	doc.Find("table.tablesorter tbody tr").Each(func(i int, s *goquery.Selection) {
		titleElem := s.Find("a.topictitle")
		if titleElem.Length() == 0 {
			return
		}

		title := strings.TrimSpace(titleElem.Text())
		topicHref, _ := titleElem.Attr("href")
		detailsURL := t.baseURL + "/forum/" + strings.TrimPrefix(topicHref, "/")

		// Extract topic ID
		id := ""
		if m := topicIDRegex.FindStringSubmatch(topicHref); len(m) > 1 {
			id = m[1]
		}

		// Download link
		downloadURL := ""
		dlElem := s.Find("a[href^='download.php?id=']")
		if dlHref, exists := dlElem.Attr("href"); exists {
			downloadURL = t.baseURL + "/forum/" + strings.TrimPrefix(dlHref, "/")
		}

		// Category
		category := strings.TrimSpace(s.Find("td:nth-child(2) a.gen").Text())

		// Size in bytes from <u> inside size column
		sizeBytes := int64(0)
		sizeHuman := ""
		sizeCol := s.Find("td:nth-child(6)")
		if uTag := sizeCol.Find("u"); uTag.Length() > 0 {
			sizeBytes, _ = strconv.ParseInt(strings.TrimSpace(uTag.Text()), 10, 64)
			// Text outside <u> is human readable size
			sizeHuman = strings.TrimSpace(strings.TrimPrefix(sizeCol.Text(), uTag.Text()))
		}

		// Seeds
		seeds := 0
		if seedText := strings.TrimSpace(s.Find("td.seedmed").First().Text()); seedText != "" {
			if num := digitsRegex.FindString(seedText); num != "" {
				seeds, _ = strconv.Atoi(num)
			}
		}

		// Leeches
		leeches := 0
		if leechText := strings.TrimSpace(s.Find("td.leechmed").First().Text()); leechText != "" {
			if num := digitsRegex.FindString(leechText); num != "" {
				leeches, _ = strconv.Atoi(num)
			}
		}

		// Added timestamp
		pubDate := time.Now()
		dateCol := s.Find("td:nth-child(10)")
		if uTag := dateCol.Find("u"); uTag.Length() > 0 {
			if ts, err := strconv.ParseInt(strings.TrimSpace(uTag.Text()), 10, 64); err == nil && ts > 0 {
				pubDate = time.Unix(ts, 0)
			}
		}

		// Magnet link if available
		magnet := ""
		if magElem := s.Find("a[href^='magnet:?']"); magElem.Length() > 0 {
			magnet, _ = magElem.Attr("href")
		}

		results = append(results, models.TorrentResult{
			Tracker:     "nnmclub",
			ID:          id,
			Title:       title,
			Category:    category,
			Size:        sizeBytes,
			SizeHuman:   sizeHuman,
			Seeds:       seeds,
			Leeches:     leeches,
			DownloadURL: downloadURL,
			DetailsURL:  detailsURL,
			Magnet:      magnet,
			PublishDate: pubDate,
		})

		if query.Limit > 0 && len(results) >= query.Limit {
			return
		}
	})

	return results, nil
}

// FetchInfoHash fetches the topic page from NNM-Club and extracts the BitTorrent InfoHash.
func (t *Tracker) FetchInfoHash(ctx context.Context, id string) (string, error) {
	if id == "" {
		return "", fmt.Errorf("empty topic id")
	}

	topicURL := fmt.Sprintf("%s/forum/viewtopic.php?t=%s", t.baseURL, id)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, topicURL, nil)
	if err != nil {
		return "", err
	}

	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")

	resp, err := t.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("nnmclub viewtopic request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("nnmclub viewtopic returned status: %d", resp.StatusCode)
	}

	utf8Reader := encoding.NewCP1251Reader(resp.Body)
	doc, err := goquery.NewDocumentFromReader(utf8Reader)
	if err != nil {
		return "", fmt.Errorf("failed to parse nnmclub viewtopic html: %w", err)
	}

	var foundHash string
	doc.Find("a[href^='magnet:?']").Each(func(i int, s *goquery.Selection) {
		if foundHash != "" {
			return
		}
		href, _ := s.Attr("href")
		if m := infoHashRegex.FindStringSubmatch(href); len(m) > 1 {
			foundHash = strings.ToLower(m[1])
		}
	})

	if foundHash == "" {
		return "", fmt.Errorf("no info_hash found on nnmclub topic %s", id)
	}

	return foundHash, nil
}

