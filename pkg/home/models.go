package home

// HomePayload is the unified top-level response for the home screen
type HomePayload struct {
	Hero    []HomeItem  `json:"hero"`
	Shelves []HomeShelf `json:"shelves"`
}

// HomeShelf represents a single horizontal row or carousel of content
type HomeShelf struct {
	ID          string     `json:"id"`
	Title       string     `json:"title"`
	Type        string     `json:"type"` // "continue_watching", "poster", "backdrop"
	Badge       string     `json:"badge,omitempty"`
	ActionRoute string     `json:"action_route,omitempty"`
	Items       []HomeItem `json:"items"`
}

// HomeItem is the unified normalized media card model
type HomeItem struct {
	ID            string   `json:"id"`                       // Primary unique key for client
	Tconst        string   `json:"tconst,omitempty"`         // IMDb ID (tt...)
	TmdbID        int64    `json:"tmdb_id,omitempty"`        // Numeric TMDB ID if available
	MediaType     string   `json:"media_type"`               // "movie" or "tv"
	Title         string   `json:"title"`                    // Display title
	OriginalTitle string   `json:"original_title,omitempty"` // Original title
	Year          int      `json:"year,omitempty"`           // Release year
	Rating        float64  `json:"rating,omitempty"`         // Rating (e.g. 7.8)
	VoteCount     int      `json:"vote_count,omitempty"`     // Votes count
	PosterPath    string   `json:"poster_path,omitempty"`    // Relative or proxied poster path
	BackdropPath  string   `json:"backdrop_path,omitempty"`  // Relative or proxied backdrop path
	Overview      string   `json:"overview,omitempty"`       // Russian or English plot

	// Playback progress fields (for "continue_watching")
	Season          int     `json:"season,omitempty"`
	Episode         int     `json:"episode,omitempty"`
	EpisodeTitle    string  `json:"episode_title,omitempty"`
	EpisodeStill    string  `json:"episode_still,omitempty"`
	PositionSeconds int64   `json:"position_seconds,omitempty"`
	DurationSeconds int64   `json:"duration_seconds,omitempty"`
	PlaybackPercent float64 `json:"playback_percent,omitempty"`
	Timecode        string  `json:"timecode,omitempty"`
	IsNextUp        bool    `json:"is_next_up,omitempty"`

	// Swarm fields (for tracker shelves)
	Seeds        int    `json:"seeds,omitempty"`
	QualityBadge string `json:"quality_badge,omitempty"`
}
