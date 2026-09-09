package hotlist

import (
	"time"
)

// RawTorrent represents a parsed torrent entry from a tracker
type RawTorrent struct {
	Tracker       string    `json:"tracker"`
	ID            string    `json:"id"`
	RawTitle      string    `json:"raw_title"`
	RussianTitle  string    `json:"russian_title"`
	OriginalTitle string    `json:"original_title"`
	Year          int       `json:"year"`
	Season        string    `json:"season,omitempty"`
	Quality       string    `json:"quality,omitempty"`
	Voice         string    `json:"voice,omitempty"`
	SizeBytes     int64     `json:"size_bytes"`
	SizeHuman     string    `json:"size_human"`
	Seeds         int       `json:"seeds"`
	Leeches       int       `json:"leeches"`
	Magnet        string    `json:"magnet"`
	InfoHash      string    `json:"info_hash"`
	PublishDate   time.Time `json:"publish_date"`
	MediaType     string    `json:"media_type"` // "movie" or "tv"
}

// Item represents a curated hotlist card for frontend display
type Item struct {
	ID            int      `json:"id"`                     // Unique numeric ID for frontend keying
	Tconst        string   `json:"tconst,omitempty"`       // IMDb ID (tt...)
	MediaType     string   `json:"media_type"`             // "movie" or "tv"
	Title         string   `json:"title"`                  // Primary display title (Russian or Original)
	OriginalTitle string   `json:"original_title"`         // Original title
	Year          int      `json:"year"`                   // Release year
	Rating        float64  `json:"rating"`                 // IMDb rating (e.g. 8.1)
	VoteCount     int      `json:"vote_count"`             // IMDb votes count
	PosterPath    string   `json:"poster_path,omitempty"`  // Poster URL (/poster/tt...)
	BackdropPath  string   `json:"backdrop_path,omitempty"`
	Overview      string   `json:"overview,omitempty"`
	Genres        []string `json:"genres,omitempty"`
	Seeds         int      `json:"seeds"`                  // Total aggregated swarm seeds
	Leeches       int      `json:"leeches"`                // Total aggregated swarm leeches
	Tracker       string   `json:"tracker"`                // "rutor", "rutracker", "nnmclub"
	Quality       string   `json:"quality,omitempty"`      // Best available quality (e.g. "4K UHD | 1080p")
	Resolution    string   `json:"resolution,omitempty"`   // "4k", "1080p", "lq"
	TorrentCount  int      `json:"torrent_count"`          // Number of distinct releases merged
}


// Response represents a paginated shelf response matching frontend FeedShelf
type Response struct {
	ID           string `json:"id"`            // "tracker_hotlist"
	Title        string `json:"title"`         // "Популярно на трекерах"
	MediaType    string `json:"media_type"`    // "movie" or "tv"
	Page         int    `json:"page"`          // Current page
	TotalPages   int    `json:"total_pages"`   // Total pages
	TotalResults int    `json:"total_results"` // Total matched items
	Items        []Item `json:"items"`         // Cards for display
}
