package hotlist

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/PuerkitoBio/goquery"
)

var (
	sizeRegex     = regexp.MustCompile(`([\d\.]+)\s*([KMGTP]B)`)
	infoHashRegex = regexp.MustCompile(`(?i)urn:btih:([a-f0-9]{40}|[a-z2-7]{32})`)
	digitsRegex   = regexp.MustCompile(`\d+`)
)

type Scraper struct {
	baseURL string
	client  *http.Client
	sem     chan struct{}
}

func NewScraper(baseURL string) *Scraper {
	if baseURL == "" {
		baseURL = "https://rutor.info"
	}
	return &Scraper{
		baseURL: strings.TrimRight(baseURL, "/"),
		client: &http.Client{
			Timeout: 20 * time.Second,
		},
		sem: make(chan struct{}, 2), // Strictly limit concurrency to 2 requests per tracker
	}
}

// ScrapeMovies fetches top-seeded movies from RuTor across 10 pages per category
func (s *Scraper) ScrapeMovies(ctx context.Context, limit int) ([]RawTorrent, error) {
	var urls []string
	// Categories: 1 = Зарубежные фильмы, 5 = Наши фильмы, 7 = Мультипликация
	// 10 pages per category: page 0 through 9
	categories := []int{1, 5, 7}
	for _, cat := range categories {
		for page := 0; page < 10; page++ {
			urls = append(urls, fmt.Sprintf("%s/browse/%d/%d/0/2", s.baseURL, page, cat))
		}
	}

	return s.scrapeUrlsConcurrently(ctx, urls, "movie", limit)
}

// ScrapeSeries fetches top-seeded TV series from RuTor across 10 pages per category
func (s *Scraper) ScrapeSeries(ctx context.Context, limit int) ([]RawTorrent, error) {
	var urls []string
	// Categories: 4 = Зарубежные сериалы, 16 = Наши сериалы
	// 10 pages per category: page 0 through 9
	categories := []int{4, 16}
	for _, cat := range categories {
		for page := 0; page < 10; page++ {
			urls = append(urls, fmt.Sprintf("%s/browse/%d/%d/0/2", s.baseURL, page, cat))
		}
	}

	return s.scrapeUrlsConcurrently(ctx, urls, "tv", limit)
}

// ScrapeAnime fetches top-seeded anime releases from RuTor category 10
func (s *Scraper) ScrapeAnime(ctx context.Context, limit int) ([]RawTorrent, error) {
	var urls []string
	for page := 0; page < 10; page++ {
		urls = append(urls, fmt.Sprintf("%s/browse/%d/10/0/2", s.baseURL, page))
	}
	return s.scrapeUrlsConcurrently(ctx, urls, "tv", limit)
}

// ScrapeDocumentaries fetches top-seeded documentary releases from RuTor category 12
func (s *Scraper) ScrapeDocumentaries(ctx context.Context, limit int) ([]RawTorrent, error) {
	var urls []string
	for page := 0; page < 10; page++ {
		urls = append(urls, fmt.Sprintf("%s/browse/%d/12/0/2", s.baseURL, page))
	}
	return s.scrapeUrlsConcurrently(ctx, urls, "movie", limit)
}

// ScrapeUHD fetches high-resolution 4K/2160p releases specifically
func (s *Scraper) ScrapeUHD(ctx context.Context, mediaType string, limit int) ([]RawTorrent, error) {
	var urls []string
	cat := 1
	mType := "movie"
	if mediaType == "tv" {
		cat = 4
		mType = "tv"
	}
	for page := 0; page < 5; page++ {
		urls = append(urls, fmt.Sprintf("%s/search/%d/%d/0/2/2160p", s.baseURL, page, cat))
		urls = append(urls, fmt.Sprintf("%s/search/%d/%d/0/2/UHD", s.baseURL, page, cat))
	}
	return s.scrapeUrlsConcurrently(ctx, urls, mType, limit)
}


func (s *Scraper) scrapeUrlsConcurrently(ctx context.Context, urls []string, mediaType string, limit int) ([]RawTorrent, error) {
	type pageResult struct {
		torrents []RawTorrent
		err      error
	}

	resChan := make(chan pageResult, len(urls))
	var wg sync.WaitGroup

	for _, u := range urls {
		wg.Add(1)
		go func(pageURL string) {
			defer wg.Done()

			// Enforce concurrency limit of 2 via semaphore
			select {
			case s.sem <- struct{}{}:
			case <-ctx.Done():
				resChan <- pageResult{err: ctx.Err()}
				return
			}
			defer func() { <-s.sem }()

			// Gentle inter-request delay to prevent IP bans
			time.Sleep(50 * time.Millisecond)

			torrents, err := s.scrapePage(ctx, pageURL, mediaType)
			resChan <- pageResult{torrents: torrents, err: err}
		}(u)
	}

	wg.Wait()
	close(resChan)

	var allTorrents []RawTorrent
	seenHashes := make(map[string]bool)

	for res := range resChan {
		if res.err != nil {
			continue
		}
		for _, t := range res.torrents {
			if t.InfoHash != "" && seenHashes[t.InfoHash] {
				continue
			}
			if t.InfoHash != "" {
				seenHashes[t.InfoHash] = true
			}
			allTorrents = append(allTorrents, t)
			if limit > 0 && len(allTorrents) >= limit {
				return allTorrents, nil
			}
		}
	}

	return allTorrents, nil
}

func (s *Scraper) scrapePage(ctx context.Context, pageURL, mediaType string) ([]RawTorrent, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pageURL, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "ru,en;q=0.9")

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("rutor request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("rutor status: %d", resp.StatusCode)
	}

	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to parse rutor html: %w", err)
	}

	var torrents []RawTorrent

	doc.Find("div#index table tr").Each(func(_ int, sel *goquery.Selection) {
		titleLink := sel.Find("a[href^='/torrent/']")
		if titleLink.Length() == 0 {
			return // header or separator
		}

		rawTitle := strings.TrimSpace(titleLink.Text())
		href, _ := titleLink.Attr("href")

		id := ""
		parts := strings.Split(strings.TrimPrefix(href, "/torrent/"), "/")
		if len(parts) > 0 {
			id = parts[0]
		}

		magnet, _ := sel.Find("a[href^='magnet:?']").Attr("href")
		infoHash := ""
		if m := infoHashRegex.FindStringSubmatch(magnet); len(m) > 1 {
			infoHash = strings.ToLower(m[1])
		}

		sizeBytes := int64(0)
		sizeHuman := ""
		sel.Find("td[align='right']").Each(func(_ int, td *goquery.Selection) {
			text := strings.ReplaceAll(td.Text(), "\u00a0", " ")
			if m := sizeRegex.FindStringSubmatch(text); len(m) == 3 {
				sizeHuman = m[0]
				sizeBytes = parseSizeBytes(m[1], m[2])
			}
		})

		seeds := 0
		if seedText := strings.TrimSpace(sel.Find("span.green").Text()); seedText != "" {
			if num := digitsRegex.FindString(seedText); num != "" {
				seeds, _ = strconv.Atoi(num)
			}
		}

		leeches := 0
		if leechText := strings.TrimSpace(sel.Find("span.red").Text()); leechText != "" {
			if num := digitsRegex.FindString(leechText); num != "" {
				leeches, _ = strconv.Atoi(num)
			}
		}

		ru, orig, year, season, quality := ParseReleaseTitle(rawTitle)

		torrents = append(torrents, RawTorrent{
			Tracker:       "rutor",
			ID:            id,
			RawTitle:      rawTitle,
			RussianTitle:  ru,
			OriginalTitle: orig,
			Year:          year,
			Season:        season,
			Quality:       quality,
			SizeBytes:     sizeBytes,
			SizeHuman:     sizeHuman,
			Seeds:         seeds,
			Leeches:       leeches,
			Magnet:        magnet,
			InfoHash:      infoHash,
			PublishDate:   time.Now(),
			MediaType:     mediaType,
		})
	})

	return torrents, nil
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
