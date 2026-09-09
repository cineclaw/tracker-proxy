package api

import (
	"encoding/xml"
	"net/http"
	"strconv"
	"time"

	"tracker-proxy/pkg/models"
)

type Caps struct {
	XMLName    xml.Name   `xml:"caps"`
	Server     CapsServer `xml:"server"`
	Searching  Searching  `xml:"searching"`
	Categories Categories `xml:"categories"`
}

type CapsServer struct {
	Version string `xml:"version,attr"`
	Title   string `xml:"title,attr"`
}

type Searching struct {
	Search      SearchParam `xml:"search"`
	TvSearch    SearchParam `xml:"tv-search"`
	MovieSearch SearchParam `xml:"movie-search"`
}

type SearchParam struct {
	Available       string `xml:"available,attr"`
	SupportedParams string `xml:"supportedParams,attr"`
}

type Categories struct {
	Category []Category `xml:"category"`
}

type Category struct {
	ID   string `xml:"id,attr"`
	Name string `xml:"name,attr"`
}

type RSSFeed struct {
	XMLName  xml.Name   `xml:"rss"`
	Version  string     `xml:"version,attr"`
	AtomNS   string     `xml:"xmlns:atom,attr"`
	Torznab  string     `xml:"xmlns:torznab,attr"`
	Channel  RSSChannel `xml:"channel"`
}

type RSSChannel struct {
	Title       string    `xml:"title"`
	Description string    `xml:"description"`
	Link        string    `xml:"link"`
	Items       []RSSItem `xml:"item"`
}

type RSSItem struct {
	Title       string        `xml:"title"`
	Guid        string        `xml:"guid"`
	Link        string        `xml:"link"`
	Comments    string        `xml:"comments,omitempty"`
	PubDate     string        `xml:"pubDate"`
	Size        int64         `xml:"size"`
	Enclosure   *RSSEnclosure `xml:"enclosure,omitempty"`
	TorznabAttr []TorznabAttr `xml:"torznab:attr"`
}

type RSSEnclosure struct {
	URL    string `xml:"url,attr"`
	Length int64  `xml:"length,attr"`
	Type   string `xml:"type,attr"`
}

type TorznabAttr struct {
	Name  string `xml:"name,attr"`
	Value string `xml:"value,attr"`
}

func RenderCaps(w http.ResponseWriter) {
	caps := Caps{
		Server: CapsServer{
			Version: "1.0",
			Title:   "tracker-proxy",
		},
		Searching: Searching{
			Search:      SearchParam{Available: "yes", SupportedParams: "q"},
			TvSearch:    SearchParam{Available: "yes", SupportedParams: "q,season,ep"},
			MovieSearch: SearchParam{Available: "yes", SupportedParams: "q,imdbid"},
		},
		Categories: Categories{
			Category: []Category{
				{ID: "2000", Name: "Movies"},
				{ID: "5000", Name: "TV"},
			},
		},
	}

	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(xml.Header))
	_ = xml.NewEncoder(w).Encode(caps)
}

func RenderTorznabFeed(w http.ResponseWriter, results []models.TorrentResult) {
	items := make([]RSSItem, 0, len(results))

	for _, res := range results {
		downloadLink := res.DownloadURL
		if downloadLink == "" {
			downloadLink = res.Magnet
		}

		item := RSSItem{
			Title:    fmtTitleWithTracker(res),
			Guid:     res.DetailsURL,
			Link:     downloadLink,
			Comments: res.DetailsURL,
			PubDate:  res.PublishDate.Format(time.RFC1123Z),
			Size:     res.Size,
			TorznabAttr: []TorznabAttr{
				{Name: "seeders", Value: strconv.Itoa(res.Seeds)},
				{Name: "peers", Value: strconv.Itoa(res.Seeds + res.Leeches)},
				{Name: "category", Value: "2000"},
			},
		}

		if downloadLink != "" {
			item.Enclosure = &RSSEnclosure{
				URL:    downloadLink,
				Length: res.Size,
				Type:   "application/x-bittorrent",
			}
		}

		if res.InfoHash != "" {
			item.TorznabAttr = append(item.TorznabAttr, TorznabAttr{
				Name:  "infohash",
				Value: res.InfoHash,
			})
		}

		if res.Magnet != "" {
			item.TorznabAttr = append(item.TorznabAttr, TorznabAttr{
				Name:  "magneturl",
				Value: res.Magnet,
			})
		}

		items = append(items, item)
	}

	feed := RSSFeed{
		Version: "2.0",
		AtomNS:  "http://www.w3.org/2005/Atom",
		Torznab: "http://torznab.com/schemas/2015/feed",
		Channel: RSSChannel{
			Title:       "tracker-proxy Torznab Feed",
			Description: "Aggregated torrent feed",
			Link:        "http://localhost:9118",
			Items:       items,
		},
	}

	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(xml.Header))
	_ = xml.NewEncoder(w).Encode(feed)
}

func fmtTitleWithTracker(res models.TorrentResult) string {
	return "[" + res.Tracker + "] " + res.Title
}
