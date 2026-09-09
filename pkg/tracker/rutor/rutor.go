package rutor

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"tracker-proxy/pkg/models"
)

var (
	sizeRegex     = regexp.MustCompile(`([\d\.]+)\s*([KMGTP]B)`)
	infoHashRegex = regexp.MustCompile(`(?i)urn:btih:([a-f0-9]{40}|[a-z2-7]{32})`)
	digitsRegex   = regexp.MustCompile(`\d+`)
)

type Tracker struct {
	baseURL string
	client  *http.Client
	enabled bool
}

func New(baseURL string) *Tracker {
	if baseURL == "" {
		baseURL = "https://rutor.info"
	}
	return &Tracker{
		baseURL: strings.TrimRight(baseURL, "/"),
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
		enabled: true,
	}
}

func (t *Tracker) Name() string {
	return "rutor"
}

func (t *Tracker) IsEnabled() bool {
	return t.enabled
}

func (t *Tracker) SetEnabled(enabled bool) {
	t.enabled = enabled
}

func (t *Tracker) Search(ctx context.Context, query models.SearchQuery) ([]models.TorrentResult, error) {
	if !t.enabled {
		return nil, nil
	}

	searchURL := fmt.Sprintf("%s/search/0/0/0/0/%s", t.baseURL, url.PathEscape(query.Query))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, searchURL, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "ru,en;q=0.9")

	resp, err := t.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("rutor request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("rutor returned status: %d", resp.StatusCode)
	}

	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to parse rutor html: %w", err)
	}

	var results []models.TorrentResult

	doc.Find("div#index table tr").Each(func(i int, s *goquery.Selection) {
		titleLink := s.Find("a[href^='/torrent/']")
		if titleLink.Length() == 0 {
			return // header or notice row
		}

		title := strings.TrimSpace(titleLink.Text())
		href, _ := titleLink.Attr("href")
		detailsURL := t.baseURL + href

		// Extract Torrent ID from /torrent/123456/...
		id := ""
		parts := strings.Split(strings.TrimPrefix(href, "/torrent/"), "/")
		if len(parts) > 0 {
			id = parts[0]
		}

		// Magnet link
		magnet, _ := s.Find("a[href^='magnet:?']").Attr("href")

		// InfoHash
		infoHash := ""
		if m := infoHashRegex.FindStringSubmatch(magnet); len(m) > 1 {
			infoHash = strings.ToLower(m[1])
		}

		// Download link
		downloadURL := ""
		if dlLink, exists := s.Find("a.downgif").Attr("href"); exists {
			if strings.HasPrefix(dlLink, "//") {
				downloadURL = "https:" + dlLink
			} else {
				downloadURL = dlLink
			}
		}

		// Size
		sizeBytes := int64(0)
		sizeHuman := ""
		s.Find("td[align='right']").Each(func(_ int, td *goquery.Selection) {
			text := strings.ReplaceAll(td.Text(), "\u00a0", " ")
			if matches := sizeRegex.FindStringSubmatch(text); len(matches) == 3 {
				sizeHuman = matches[0]
				sizeBytes = parseSizeBytes(matches[1], matches[2])
			}
		})

		// Seeds & Leeches
		seeds := 0
		if seedText := strings.TrimSpace(s.Find("span.green").Text()); seedText != "" {
			if num := digitsRegex.FindString(seedText); num != "" {
				seeds, _ = strconv.Atoi(num)
			}
		}

		leeches := 0
		if leechText := strings.TrimSpace(s.Find("span.red").Text()); leechText != "" {
			if num := digitsRegex.FindString(leechText); num != "" {
				leeches, _ = strconv.Atoi(num)
			}
		}

		results = append(results, models.TorrentResult{
			Tracker:     "rutor",
			ID:          id,
			Title:       title,
			Size:        sizeBytes,
			SizeHuman:   sizeHuman,
			Seeds:       seeds,
			Leeches:     leeches,
			Magnet:      magnet,
			DownloadURL: downloadURL,
			DetailsURL:  detailsURL,
			InfoHash:    infoHash,
			PublishDate: time.Now(), // rough timestamp
		})

		if query.Limit > 0 && len(results) >= query.Limit {
			return
		}
	})

	return results, nil
}

func parseSizeBytes(valStr, unit string) int64 {
	val, err := strconv.ParseFloat(valStr, 64)
	if err != nil {
		return 0
	}
	switch strings.ToUpper(unit) {
	case "KB":
		return int64(val * 1024)
	case "MB":
		return int64(val * 1024 * 1024)
	case "GB":
		return int64(val * 1024 * 1024 * 1024)
	case "TB":
		return int64(val * 1024 * 1024 * 1024 * 1024)
	default:
		return int64(val)
	}
}
