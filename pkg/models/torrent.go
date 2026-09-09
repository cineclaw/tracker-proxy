package models

import "time"

// TorrentSource represents an individual tracker's source for a merged release.
type TorrentSource struct {
	Tracker     string `json:"tracker"`
	ID          string `json:"id"`
	Title       string `json:"title"`
	Size        int64  `json:"size"`
	SizeHuman   string `json:"size_human"`
	Seeds       int    `json:"seeds"`
	Leeches     int    `json:"leeches"`
	Magnet      string `json:"magnet,omitempty"`
	DownloadURL string `json:"download_url,omitempty"`
	DetailsURL  string `json:"details_url"`
}

// TorrentResult represents a single torrent release found on a tracker (or merged across trackers).
type TorrentResult struct {
	Tracker     string          `json:"tracker"`
	Trackers    []string        `json:"trackers,omitempty"`
	Sources     []TorrentSource `json:"sources,omitempty"`
	ID          string          `json:"id"`
	Title       string          `json:"title"`
	Size        int64           `json:"size"`
	SizeHuman   string          `json:"size_human"`
	Seeds       int             `json:"seeds"`
	Leeches     int             `json:"leeches"`
	Magnet      string          `json:"magnet,omitempty"`
	DownloadURL string          `json:"download_url,omitempty"`
	DetailsURL  string          `json:"details_url,omitempty"`
	PublishDate time.Time       `json:"publish_date"`
	Category    string          `json:"category,omitempty"`
	InfoHash    string          `json:"info_hash,omitempty"`
	Seasons     []int           `json:"seasons,omitempty"`
	IsComplete  bool            `json:"is_complete,omitempty"`
	Resolution  string          `json:"resolution,omitempty"`
}

// SearchQuery represents the search parameters.
type SearchQuery struct {
	Query        string
	IMDbID       string
	RefreshCache bool
	Categories   []string
	Limit        int
}
