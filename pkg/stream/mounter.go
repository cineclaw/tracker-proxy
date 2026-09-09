package stream

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type HashResolver interface {
	ResolveInfoHash(ctx context.Context, trackerName, id string) (string, error)
}

type MountRequest struct {
	Tconst      string `json:"tconst"`
	Title       string `json:"title"`
	RuTitle     string `json:"ru_title,omitempty"`
	Year        string `json:"year,omitempty"`
	Type        string `json:"type,omitempty"` // "movie" or "tvSeries"
	Magnet      string `json:"magnet,omitempty"`
	Season      int    `json:"season,omitempty"`
	Tracker     string `json:"tracker,omitempty"`
	TorrentID   string `json:"torrent_id,omitempty"`
	DetailsURL  string `json:"details_url,omitempty"`
	Mode        string `json:"mode,omitempty"`         // "add", "replace", "add_version"
	VersionName string `json:"version_name,omitempty"` // e.g. "4K UHD", "1080p", "Remux"
	Resolution  string `json:"resolution,omitempty"`   // e.g. "4k", "1080p", "lq"
	FolderName  string `json:"folder_name,omitempty"`
}

type MountResponse struct {
	Success      bool     `json:"success"`
	Message      string   `json:"message"`
	JellyfinURL  string   `json:"jellyfin_url"`
	MountedFiles []string `json:"mounted_files"`
}

type UnmountRequest struct {
	Tconst     string `json:"tconst,omitempty"`
	Type       string `json:"type,omitempty"` // "movie" or "series" / "tvSeries"
	FolderName string `json:"folder_name,omitempty"`
}

type UnmountResponse struct {
	Success       bool     `json:"success"`
	Message       string   `json:"message"`
	RemovedFiles  []string `json:"removed_files,omitempty"`
	RemovedSwarms []string `json:"removed_swarms,omitempty"`
}

type MountedStatusResponse struct {
	Mounted      bool     `json:"mounted"`
	Tconst       string   `json:"tconst,omitempty"`
	Type         string   `json:"type,omitempty"`
	FolderName   string   `json:"folder_name,omitempty"`
	LibraryPath  string   `json:"library_path,omitempty"`
	SourcePath   string   `json:"source_path,omitempty"`
	FileCount    int      `json:"file_count"`
	MountedFiles []string `json:"mounted_files,omitempty"`
	Seasons      []int    `json:"seasons,omitempty"`
	Versions     []string `json:"versions,omitempty"`
}

type JellyfinDeletedWebhook struct {
	Event      string `json:"event"`
	ItemId     string `json:"itemId,omitempty"`
	Name       string `json:"name,omitempty"`
	SeriesName string `json:"seriesName,omitempty"`
	ItemType   string `json:"itemType,omitempty"`
	Path       string `json:"path,omitempty"`
	ImdbId     string `json:"imdbId,omitempty"`
}

type Mounter struct {
	gostormURL        string
	jellyfinURL       string
	jellyfinAPIKey    string
	mediaSourcePath   string
	mediaLibraryPath  string
	mediaVirtualPath  string
	jellyfinMediaPath string
	indexerURL        string
	client            *http.Client
	hashResolver      HashResolver
}

func NewMounter(resolver HashResolver) *Mounter {
	gostorm := os.Getenv("TIRAMISU_URL")
	if gostorm == "" {
		gostorm = "http://tiramisu:8090"
	}
	jellyfin := os.Getenv("JELLYFIN_URL")
	if jellyfin == "" {
		jellyfin = "http://jellyfin:8096"
	}
	apiKey := os.Getenv("JELLYFIN_API_KEY")
	sourcePath := os.Getenv("MEDIA_SOURCE_PATH")
	if sourcePath == "" {
		sourcePath = "/media/source"
	}
	libraryPath := os.Getenv("MEDIA_LIBRARY_PATH")
	if libraryPath == "" {
		libraryPath = "/media/library"
	}
	virtualPath := os.Getenv("MEDIA_VIRTUAL_PATH")
	if virtualPath == "" {
		virtualPath = "/media/virtual"
	}
	mediaPath := os.Getenv("JELLYFIN_MEDIA_PATH")
	if mediaPath == "" {
		mediaPath = "/media"
	}
	indexer := os.Getenv("IMDB_INDEXER_URL")
	if indexer == "" {
		indexer = "http://host.docker.internal:8090"
	}

	return &Mounter{
		gostormURL:        strings.TrimRight(gostorm, "/"),
		jellyfinURL:       strings.TrimRight(jellyfin, "/"),
		jellyfinAPIKey:    apiKey,
		mediaSourcePath:   strings.TrimRight(sourcePath, "/"),
		mediaLibraryPath:  strings.TrimRight(libraryPath, "/"),
		mediaVirtualPath:  strings.TrimRight(virtualPath, "/"),
		jellyfinMediaPath: strings.TrimRight(mediaPath, "/"),
		indexerURL:        strings.TrimRight(indexer, "/"),
		client:            &http.Client{Timeout: 30 * time.Second},
		hashResolver:      resolver,
	}
}

type FileStat struct {
	ID     int    `json:"id"`
	Path   string `json:"path"`
	Length int64  `json:"length"`
}

type TorrentDetails struct {
	Hash      string     `json:"hash"`
	Title     string     `json:"title"`
	Name      string     `json:"name"`
	Stat      int        `json:"stat"`
	FileStats []FileStat `json:"file_stats"`
}

type ShowSeasonMeta struct {
	SeasonNumber int    `json:"season_number"`
	Name         string `json:"name"`
	PosterPath   string `json:"poster_path"`
}

type ShowMeta struct {
	Tconst       string           `json:"tconst"`
	TMDBID       int              `json:"tmdb_id"`
	Name         string           `json:"name"`
	OriginalName string           `json:"original_name"`
	Overview     string           `json:"overview"`
	Premiered    string           `json:"premiered"`
	Rating       float64          `json:"rating"`
	Genres       []string         `json:"genres"`
	Studio       string           `json:"studio"`
	Status       string           `json:"status"`
	PosterPath   string           `json:"poster_path"`
	BackdropPath string           `json:"backdrop_path"`
	LogoPath     string           `json:"logo_path"`
	Seasons      []ShowSeasonMeta `json:"seasons"`
}

type MovieMeta struct {
	Tconst        string   `json:"tconst"`
	TMDBID        int      `json:"tmdb_id"`
	Title         string   `json:"title"`
	OriginalTitle string   `json:"original_title"`
	Overview      string   `json:"overview"`
	Premiered     string   `json:"premiered"`
	Year          int      `json:"year"`
	Rating        float64  `json:"rating"`
	Genres        []string `json:"genres"`
	PosterPath    string   `json:"poster_path"`
	BackdropPath  string   `json:"backdrop_path"`
	LogoPath      string   `json:"logo_path"`
}

func (m *Mounter) MountTorrent(ctx context.Context, req MountRequest) (*MountResponse, error) {
	log.Printf("[mounter] MountTorrent requested: title=%q tconst=%q type=%q season=%d mode=%q version=%q folder=%q",
		req.Title, req.Tconst, req.Type, req.Season, req.Mode, req.VersionName, req.FolderName)

	// 0. Resolve missing magnet link if torrent_id and tracker are provided
	if req.Magnet == "" && req.TorrentID != "" {
		if m.hashResolver != nil && req.Tracker != "" {
			hash, err := m.hashResolver.ResolveInfoHash(ctx, req.Tracker, req.TorrentID)
			if err != nil {
				log.Printf("[mounter] failed to resolve info_hash for %s:%s: %v", req.Tracker, req.TorrentID, err)
			} else if hash != "" {
				req.Magnet = fmt.Sprintf("magnet:?xt=urn:btih:%s&dn=%s", hash, url.QueryEscape(req.Title))
			}
		}
	}

	if req.Magnet == "" {
		return nil, fmt.Errorf("magnet link is required (could not resolve info_hash for %s:%s)", req.Tracker, req.TorrentID)
	}
	if req.Title == "" {
		req.Title = req.Tconst
	}

	// 1. Add torrent to GoStorm
	hash, err := m.addTorrentToGoStorm(ctx, req.Magnet, req.Title)
	if err != nil {
		return nil, fmt.Errorf("gostorm add failed: %w", err)
	}

	// 2. Poll until metadata & file_stats are ready (up to 15s)
	details, err := m.waitForTorrentFiles(ctx, hash, 15*time.Second)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch torrent metadata: %w", err)
	}

	// 3. Filter video files (and exclude samples/featurettes/trailers)
	var allVideos []FileStat
	var maxVideoLen int64
	for _, f := range details.FileStats {
		ext := strings.ToLower(filepath.Ext(f.Path))
		if ext == ".mkv" || ext == ".mp4" || ext == ".avi" || ext == ".mov" || ext == ".m4v" || ext == ".ts" {
			allVideos = append(allVideos, f)
			if f.Length > maxVideoLen {
				maxVideoLen = f.Length
			}
		}
	}

	if len(allVideos) == 0 {
		return nil, fmt.Errorf("no video files found in torrent")
	}

	var videoFiles []FileStat
	for _, f := range allVideos {
		if !isSampleFile(f.Path, f.Length, maxVideoLen) {
			videoFiles = append(videoFiles, f)
		}
	}
	if len(videoFiles) == 0 {
		videoFiles = allVideos
	}

	cleanTitle := sanitizeFilename(req.Title)
	yearSuffix := ""
	if req.Year != "" {
		yearSuffix = fmt.Sprintf(" (%s)", req.Year)
	}
	idSuffix := ""
	if req.Tconst != "" {
		idSuffix = fmt.Sprintf(" [imdbid-%s]", req.Tconst)
	}

	var createdRelativePaths []string
	isMovie := strings.EqualFold(req.Type, "movie")
	isExplicitSeries := strings.EqualFold(req.Type, "tvSeries") || strings.EqualFold(req.Type, "series") || strings.EqualFold(req.Type, "tvMiniSeries") || req.Season > 0

	isSeries := false
	if isMovie {
		isSeries = false
	} else if isExplicitSeries {
		isSeries = true
	} else if req.Type == "" {
		// Heuristic fallback only when type was not provided by client
		if len(videoFiles) > 1 {
			isSeries = true
		}
	}

	targetType := "movies"
	if isSeries {
		targetType = "shows"
	}

	// Safeguard: if already mounted under shows, ensure isSeries is true ONLY if client did not explicitly specify movie
	if !isSeries && !isMovie && req.Tconst != "" {
		if folder, _ := m.findExistingFolder("shows", req.Tconst); folder != "" {
			isSeries = true
			targetType = "shows"
		}
	}

	// Canonical folder & title resolution:
	// Prioritize explicitly provided FolderName or existing on-disk folder matching [imdbid-<tconst>]
	var folderName string
	if req.FolderName != "" {
		folderName = req.FolderName
		if prefix := extractTitlePrefixFromFolder(folderName); prefix != "" {
			cleanTitle = prefix
		}
	} else if existingFolder, existingTitle := m.findExistingFolder(targetType, req.Tconst); existingFolder != "" {
		folderName = existingFolder
		if existingTitle != "" {
			cleanTitle = existingTitle
		}
	} else {
		folderName = fmt.Sprintf("%s%s%s", cleanTitle, yearSuffix, idSuffix)
	}

	if !isSeries {
		// Movie: select largest video file
		var mainFile FileStat
		for _, f := range videoFiles {
			if f.Length > mainFile.Length {
				mainFile = f
			}
		}

		// 1) Source directory for Tiramisu VFS stub
		sourceDir := filepath.Join(m.mediaSourcePath, "movies", folderName)
		if err := os.MkdirAll(sourceDir, 0755); err != nil {
			return nil, fmt.Errorf("create movie source directory: %w", err)
		}

		// 2) Library directory for Jellyfin (symlinks, NFO, poster)
		libDir := filepath.Join(m.mediaLibraryPath, "movies", folderName)
		if err := os.MkdirAll(libDir, 0755); err != nil {
			return nil, fmt.Errorf("create movie library directory: %w", err)
		}

		// Handle replace mode
		if strings.EqualFold(req.Mode, "replace") {
			oldHashes := m.extractHashesFromDirectory(sourceDir)
			for _, h := range oldHashes {
				if h != hash {
					_ = m.removeTorrentFromGoStorm(ctx, h)
				}
			}
			_ = filepath.Walk(sourceDir, func(p string, fi os.FileInfo, err error) error {
				if err == nil && !fi.IsDir() && strings.HasSuffix(strings.ToLower(fi.Name()), ".mkv") {
					_ = os.Remove(p)
				}
				return nil
			})
			_ = filepath.Walk(libDir, func(p string, fi os.FileInfo, err error) error {
				if err == nil && !fi.IsDir() && strings.HasSuffix(strings.ToLower(fi.Name()), ".mkv") {
					_ = os.Remove(p)
				}
				return nil
			})
		}

		// Check existing video files
		var existingStubs []string
		if entries, err := os.ReadDir(sourceDir); err == nil {
			for _, entry := range entries {
				if !entry.IsDir() && strings.HasSuffix(strings.ToLower(entry.Name()), ".mkv") {
					existingStubs = append(existingStubs, entry.Name())
				}
			}
		}

		versionLabel := determineVersionLabel(req, "1080p")
		mkvName := fmt.Sprintf("%s - %s.mkv", folderName, versionLabel)

		// Migrate any existing single file if it lacks the "<folderName> - " prefix
		for _, oldName := range existingStubs {
			if !strings.HasPrefix(oldName, folderName+" - ") {
				oldLabel := "Default"
				if strings.Contains(strings.ToLower(oldName), "4k") || strings.Contains(strings.ToLower(oldName), "2160p") {
					oldLabel = "4K UHD"
				} else if strings.Contains(strings.ToLower(oldName), "1080p") {
					oldLabel = "1080p"
				}
				migratedName := fmt.Sprintf("%s - %s.mkv", folderName, oldLabel)
				if migratedName != oldName && migratedName != mkvName {
					oldSource := filepath.Join(sourceDir, oldName)
					newSource := filepath.Join(sourceDir, migratedName)
					_ = os.Rename(oldSource, newSource)

					oldLib := filepath.Join(libDir, oldName)
					newLib := filepath.Join(libDir, migratedName)
					_ = os.Remove(oldLib)
					_ = os.Symlink(filepath.Join(m.mediaVirtualPath, "movies", folderName, migratedName), newLib)
					log.Printf("[mounter] Migrated movie version file: %s -> %s", oldName, migratedName)
				}
			}
		}

		stubPath := filepath.Join(sourceDir, mkvName)
		streamURL := fmt.Sprintf("http://127.0.0.1:8090/stream?link=%s&index=%d", hash, mainFile.ID)

		if err := writeMkvStub(stubPath, streamURL, mainFile.Length, req.Magnet, req.Tconst); err != nil {
			return nil, fmt.Errorf("write stub: %w", err)
		}

		// Create symlink in library pointing to virtual FUSE path
		symlinkPath := filepath.Join(libDir, mkvName)
		virtualTarget := filepath.Join(m.mediaVirtualPath, "movies", folderName, mkvName)
		_ = os.Remove(symlinkPath)
		if err := os.Symlink(virtualTarget, symlinkPath); err != nil {
			log.Printf("[mounter] warning: failed to create symlink %s -> %s: %v", symlinkPath, virtualTarget, err)
		}

		movieMeta := m.fetchMovieMetadata(ctx, req.Tconst)
		if movieMeta != nil {
			if req.Title == "" && movieMeta.Title != "" {
				req.Title = movieMeta.Title
			}
		}

		// Write .nfo in library directory
		nfoPath := filepath.Join(libDir, fmt.Sprintf("%s%s.nfo", cleanTitle, yearSuffix))
		writeMovieNFO(nfoPath, req.Title, req.Year, req.Tconst, movieMeta)
		writeMovieNFO(filepath.Join(libDir, "movie.nfo"), req.Title, req.Year, req.Tconst, movieMeta)

		// Download poster, backdrop, logo
		var imgTasks []imageDownloadTask
		if movieMeta != nil && movieMeta.BackdropPath != "" {
			imgTasks = append(imgTasks, imageDownloadTask{
				pathOrURL: movieMeta.BackdropPath,
				destPath:  filepath.Join(libDir, "backdrop.jpg"),
				size:      "w1280",
			})
		}
		if movieMeta != nil && movieMeta.LogoPath != "" {
			imgTasks = append(imgTasks, imageDownloadTask{
				pathOrURL: movieMeta.LogoPath,
				destPath:  filepath.Join(libDir, "logo.png"),
				size:      "original",
			})
		}
		m.downloadPoster(ctx, req.Tconst, filepath.Join(libDir, "poster.jpg"))
		m.downloadImagesParallel(ctx, imgTasks)

		if _, err := os.Stat(filepath.Join(libDir, "backdrop.jpg")); err == nil {
			_ = os.Remove(filepath.Join(libDir, "fanart.jpg"))
			_ = os.Symlink("backdrop.jpg", filepath.Join(libDir, "fanart.jpg"))
		}
		if _, err := os.Stat(filepath.Join(libDir, "logo.png")); err == nil {
			_ = os.Remove(filepath.Join(libDir, "clearlogo.png"))
			_ = os.Symlink("logo.png", filepath.Join(libDir, "clearlogo.png"))
		}

		relJellyfinFile := fmt.Sprintf("%s/movies/%s/%s", m.jellyfinMediaPath, folderName, mkvName)
		createdRelativePaths = append(createdRelativePaths, relJellyfinFile)

		go func() {
			m.notifyJellyfin(false, folderName, req.Tconst)
		}()
	} else {
		// Series: map each episode across ALL seasons
		// 1) Source directory for Tiramisu VFS stubs
		seriesSourceDir := filepath.Join(m.mediaSourcePath, "shows", folderName)
		if err := os.MkdirAll(seriesSourceDir, 0755); err != nil {
			return nil, fmt.Errorf("create series source directory: %w", err)
		}

		// 2) Library directory for Jellyfin (symlinks, NFO, poster)
		seriesLibDir := filepath.Join(m.mediaLibraryPath, "shows", folderName)
		if err := os.MkdirAll(seriesLibDir, 0755); err != nil {
			return nil, fmt.Errorf("create series library directory: %w", err)
		}

		// Fetch show metadata from indexer
		showMeta := m.fetchShowMetadata(ctx, req.Tconst)
		origTitle := ""
		if showMeta != nil {
			origTitle = showMeta.OriginalName
			if req.Title == "" && showMeta.Name != "" {
				req.Title = showMeta.Name
			}
		}

		// Write tvshow.nfo in library directory
		tvshowNFO := filepath.Join(seriesLibDir, "tvshow.nfo")
		writeShowNFO(tvshowNFO, req.Title, origTitle, req.Year, req.Tconst, showMeta)

		var imgTasks []imageDownloadTask

		// Series backdrop, logo, and poster
		if showMeta != nil && showMeta.BackdropPath != "" {
			imgTasks = append(imgTasks, imageDownloadTask{
				pathOrURL: showMeta.BackdropPath,
				destPath:  filepath.Join(seriesLibDir, "backdrop.jpg"),
				size:      "w1280",
			})
		}
		if showMeta != nil && showMeta.LogoPath != "" {
			imgTasks = append(imgTasks, imageDownloadTask{
				pathOrURL: showMeta.LogoPath,
				destPath:  filepath.Join(seriesLibDir, "logo.png"),
				size:      "original",
			})
		}
		if showMeta != nil && showMeta.PosterPath != "" {
			imgTasks = append(imgTasks, imageDownloadTask{
				pathOrURL: showMeta.PosterPath,
				destPath:  filepath.Join(seriesLibDir, "poster.jpg"),
				size:      "w500",
			})
		}

		// Map season posters and names from showMeta
		seasonPosters := make(map[int]string)
		seasonNames := make(map[int]string)
		if showMeta != nil {
			for _, s := range showMeta.Seasons {
				if s.PosterPath != "" {
					seasonPosters[s.SeasonNumber] = s.PosterPath
				}
				if s.Name != "" {
					seasonNames[s.SeasonNumber] = s.Name
				}
			}
		}

		// Handle replace mode
		if strings.EqualFold(req.Mode, "replace") {
			if req.Season > 0 {
				seasonSourceDir := filepath.Join(seriesSourceDir, fmt.Sprintf("Season %02d", req.Season))
				seasonLibDir := filepath.Join(seriesLibDir, fmt.Sprintf("Season %02d", req.Season))
				oldHashes := m.extractHashesFromDirectory(seasonSourceDir)
				for _, h := range oldHashes {
					if h != hash {
						_ = m.removeTorrentFromGoStorm(ctx, h)
					}
				}
				_ = filepath.Walk(seasonSourceDir, func(p string, fi os.FileInfo, err error) error {
					if err == nil && !fi.IsDir() && strings.HasSuffix(strings.ToLower(fi.Name()), ".mkv") {
						_ = os.Remove(p)
					}
					return nil
				})
				_ = filepath.Walk(seasonLibDir, func(p string, fi os.FileInfo, err error) error {
					if err == nil && !fi.IsDir() && strings.HasSuffix(strings.ToLower(fi.Name()), ".mkv") {
						_ = os.Remove(p)
					}
					return nil
				})
			} else {
				oldHashes := m.extractHashesFromDirectory(seriesSourceDir)
				for _, h := range oldHashes {
					if h != hash {
						_ = m.removeTorrentFromGoStorm(ctx, h)
					}
				}
				_ = filepath.Walk(seriesSourceDir, func(p string, fi os.FileInfo, err error) error {
					if err == nil && !fi.IsDir() && strings.HasSuffix(strings.ToLower(fi.Name()), ".mkv") {
						_ = os.Remove(p)
					}
					return nil
				})
				_ = filepath.Walk(seriesLibDir, func(p string, fi os.FileInfo, err error) error {
					if err == nil && !fi.IsDir() && strings.HasSuffix(strings.ToLower(fi.Name()), ".mkv") {
						_ = os.Remove(p)
					}
					return nil
				})
			}
		}

		// Fetch episode metadata from indexer
		episodesMeta := m.fetchEpisodesMetadata(ctx, req.Tconst)

		seasonEpCounter := make(map[int]int)
		seenSeasons := make(map[int]bool)

		versionLabel := ""
		if strings.EqualFold(req.Mode, "add_version") || req.VersionName != "" {
			versionLabel = determineVersionLabel(req, "1080p")
		}

		for _, f := range videoFiles {
			sNum, epNum := parseSeasonEpisode(f.Path)

			// If season could not be parsed from path:
			// If single-season request was explicit (req.Season > 0), use it
			if sNum == 0 {
				if req.Season > 0 {
					sNum = req.Season
				} else {
					sNum = 1
				}
			}

			// If episode could not be parsed:
			// Increment per-season counter
			if epNum == 0 {
				seasonEpCounter[sNum]++
				epNum = seasonEpCounter[sNum]
			} else {
				if epNum > seasonEpCounter[sNum] {
					seasonEpCounter[sNum] = epNum
				}
			}

			// Source season directory & stub
			seasonSourceDir := filepath.Join(seriesSourceDir, fmt.Sprintf("Season %02d", sNum))
			_ = os.MkdirAll(seasonSourceDir, 0755)

			var epName string
			if versionLabel != "" {
				epName = fmt.Sprintf("%s - S%02dE%02d - %s.mkv", cleanTitle, sNum, epNum, versionLabel)
			} else {
				epName = fmt.Sprintf("%s - S%02dE%02d.mkv", cleanTitle, sNum, epNum)
			}
			stubPath := filepath.Join(seasonSourceDir, epName)
			streamURL := fmt.Sprintf("http://127.0.0.1:8090/stream?link=%s&index=%d", hash, f.ID)

			if err := writeMkvStub(stubPath, streamURL, f.Length, req.Magnet, req.Tconst); err != nil {
				log.Printf("Warning: failed to write stub for %s: %v", epName, err)
				continue
			}

			// Library season directory & symlink
			seasonLibDir := filepath.Join(seriesLibDir, fmt.Sprintf("Season %02d", sNum))
			_ = os.MkdirAll(seasonLibDir, 0755)

			// If first time seeing this season:
			if !seenSeasons[sNum] {
				seenSeasons[sNum] = true
				sTitle := fmt.Sprintf("Сезон %d", sNum)
				if name, ok := seasonNames[sNum]; ok && name != "" {
					sTitle = name
				}
				writeSeasonNFO(filepath.Join(seasonLibDir, "season.nfo"), sTitle, sNum)

				if posterPath, ok := seasonPosters[sNum]; ok && posterPath != "" {
					destPoster := filepath.Join(seasonLibDir, "poster.jpg")
					imgTasks = append(imgTasks, imageDownloadTask{
						pathOrURL: posterPath,
						destPath:  destPoster,
						size:      "w500",
					})
				}
			}

			epSymlink := filepath.Join(seasonLibDir, epName)
			virtualTarget := filepath.Join(m.mediaVirtualPath, "shows", folderName, fmt.Sprintf("Season %02d", sNum), epName)
			_ = os.Remove(epSymlink)
			if err := os.Symlink(virtualTarget, epSymlink); err != nil {
				log.Printf("[mounter] warning: failed to create symlink %s -> %s: %v", epSymlink, virtualTarget, err)
			}

			// Write accompanying episode .nfo for instant Jellyfin metadata binding
			nfoName := fmt.Sprintf("%s - S%02dE%02d.nfo", cleanTitle, sNum, epNum)
			nfoPath := filepath.Join(seasonLibDir, nfoName)
			epTitle := fmt.Sprintf("Серия %d", epNum)
			epPlot := ""
			epAired := ""
			stillPath := ""
			if sMap, ok := episodesMeta[sNum]; ok {
				if meta, ok := sMap[epNum]; ok {
					if meta.Name != "" {
						epTitle = meta.Name
					}
					epPlot = meta.Overview
					epAired = meta.AirDate
					stillPath = meta.StillPath
				}
			}
			thumbFileName := fmt.Sprintf("%s - S%02dE%02d-thumb.jpg", cleanTitle, sNum, epNum)
			writeEpisodeNFO(nfoPath, epTitle, sNum, epNum, epPlot, epAired, thumbFileName)

			// Queue episode thumb if stillPath is available
			if stillPath != "" {
				imgTasks = append(imgTasks, imageDownloadTask{
					pathOrURL: stillPath,
					destPath:  filepath.Join(seasonLibDir, thumbFileName),
					size:      "w500",
				})
			}

			relJellyfinPath := fmt.Sprintf("%s/shows/%s/Season %02d/%s", m.jellyfinMediaPath, folderName, sNum, epName)
			createdRelativePaths = append(createdRelativePaths, relJellyfinPath)
		}

		// Download main series poster if not already queued or present
		m.downloadPoster(ctx, req.Tconst, filepath.Join(seriesLibDir, "poster.jpg"))

		// Download all queued images (backdrop, logo, season posters, episode thumbs) in parallel
		m.downloadImagesParallel(ctx, imgTasks)

		// Create alternate symlinks fanart.jpg and clearlogo.png
		if _, err := os.Stat(filepath.Join(seriesLibDir, "backdrop.jpg")); err == nil {
			_ = os.Remove(filepath.Join(seriesLibDir, "fanart.jpg"))
			_ = os.Symlink("backdrop.jpg", filepath.Join(seriesLibDir, "fanart.jpg"))
		}
		if _, err := os.Stat(filepath.Join(seriesLibDir, "logo.png")); err == nil {
			_ = os.Remove(filepath.Join(seriesLibDir, "clearlogo.png"))
			_ = os.Symlink("logo.png", filepath.Join(seriesLibDir, "clearlogo.png"))
		}
		// Also create seasonXX-poster.jpg symlinks in series folder
		for sNum := range seenSeasons {
			sPoster := filepath.Join(seriesLibDir, fmt.Sprintf("Season %02d", sNum), "poster.jpg")
			if _, err := os.Stat(sPoster); err == nil {
				targetName := fmt.Sprintf("season%02d-poster.jpg", sNum)
				_ = os.Remove(filepath.Join(seriesLibDir, targetName))
				_ = os.Symlink(filepath.Join(fmt.Sprintf("Season %02d", sNum), "poster.jpg"), filepath.Join(seriesLibDir, targetName))
			}
		}

		isAddVersion := strings.EqualFold(req.Mode, "add_version")
		go func() {
			m.notifyJellyfin(true, folderName, req.Tconst)
			if isAddVersion {
				m.mergeSeriesEpisodesInJellyfin(folderName, req.Tconst)
			}
		}()
	}

	return &MountResponse{
		Success:      true,
		Message:      fmt.Sprintf("Successfully mounted %d video file(s)", len(createdRelativePaths)),
		JellyfinURL:  m.jellyfinURL,
		MountedFiles: createdRelativePaths,
	}, nil
}

func (m *Mounter) addTorrentToGoStorm(ctx context.Context, magnet, title string) (string, error) {
	payload := map[string]string{
		"action": "add",
		"link":   magnet,
		"title":  title,
	}
	body, _ := json.Marshal(payload)

	req, err := http.NewRequestWithContext(ctx, "POST", m.gostormURL+"/torrents", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := m.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var res struct {
		Hash string `json:"hash"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return "", err
	}
	if res.Hash == "" {
		// extract from magnet
		re := regexp.MustCompile(`(?i)urn:btih:([a-f0-9]{40})`)
		if matches := re.FindStringSubmatch(magnet); len(matches) > 1 {
			return strings.ToLower(matches[1]), nil
		}
		return "", fmt.Errorf("no hash returned by gostorm and unable to parse from magnet")
	}
	return strings.ToLower(res.Hash), nil
}

func (m *Mounter) waitForTorrentFiles(ctx context.Context, hash string, timeout time.Duration) (*TorrentDetails, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		payload := map[string]string{
			"action": "get",
			"hash":   hash,
		}
		body, _ := json.Marshal(payload)

		req, err := http.NewRequestWithContext(ctx, "POST", m.gostormURL+"/torrents", bytes.NewReader(body))
		if err == nil {
			req.Header.Set("Content-Type", "application/json")
			resp, err := m.client.Do(req)
			if err == nil && resp.StatusCode == http.StatusOK {
				var det TorrentDetails
				if err := json.NewDecoder(resp.Body).Decode(&det); err == nil {
					resp.Body.Close()
					if len(det.FileStats) > 0 {
						return &det, nil
					}
				} else {
					resp.Body.Close()
				}
			} else if resp != nil {
				resp.Body.Close()
			}
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(750 * time.Millisecond):
		}
	}
	return nil, fmt.Errorf("timed out waiting for torrent files from swarm")
}

func writeMkvStub(path, streamURL string, size int64, magnet, imdbID string) error {
	data := map[string]interface{}{
		"url":    streamURL,
		"size":   size,
		"magnet": magnet,
		"imdb":   imdbID,
	}
	jsonData, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, jsonData, 0644)
}

func writeMovieNFO(path, title, year, imdbID string, meta *MovieMeta) {
	origTag := ""
	tmdbTag := ""
	plotTag := ""
	ratingTag := ""
	premieredTag := ""
	genresTag := ""

	if meta != nil {
		if meta.TMDBID > 0 {
			tmdbTag = fmt.Sprintf("\n  <tmdbid>%d</tmdbid>", meta.TMDBID)
		}
		if meta.OriginalTitle != "" && meta.OriginalTitle != title {
			origTag = fmt.Sprintf("\n  <originaltitle>%s</originaltitle>", escapeXML(meta.OriginalTitle))
		}
		if meta.Overview != "" {
			plotTag = fmt.Sprintf("\n  <plot>%s</plot>", escapeXML(meta.Overview))
		}
		if meta.Rating > 0 {
			ratingTag = fmt.Sprintf("\n  <rating>%.1f</rating>", meta.Rating)
		}
		if meta.Premiered != "" {
			premieredTag = fmt.Sprintf("\n  <premiered>%s</premiered>", escapeXML(meta.Premiered))
		}
		for _, g := range meta.Genres {
			genresTag += fmt.Sprintf("\n  <genre>%s</genre>", escapeXML(g))
		}
	}

	content := fmt.Sprintf(`<?xml version="1.0" encoding="utf-8" standalone="yes"?>
<movie>
  <title>%s</title>%s
  <year>%s</year>%s%s%s%s%s
  <imdbid>%s</imdbid>
</movie>`, escapeXML(title), origTag, year, premieredTag, ratingTag, plotTag, genresTag, tmdbTag, imdbID)
	_ = os.WriteFile(path, []byte(content), 0644)
}

func writeShowNFO(path, title, origTitle, year, imdbID string, meta *ShowMeta) {
	origTag := ""
	if origTitle != "" && origTitle != title {
		origTag = fmt.Sprintf("\n  <originaltitle>%s</originaltitle>", escapeXML(origTitle))
	}
	tmdbTag := ""
	plotTag := ""
	ratingTag := ""
	premieredTag := ""
	studioTag := ""
	statusTag := ""
	genresTag := ""

	if meta != nil {
		if meta.TMDBID > 0 {
			tmdbTag = fmt.Sprintf("\n  <tmdbid>%d</tmdbid>", meta.TMDBID)
		}
		if meta.Overview != "" {
			plotTag = fmt.Sprintf("\n  <plot>%s</plot>", escapeXML(meta.Overview))
		}
		if meta.Rating > 0 {
			ratingTag = fmt.Sprintf("\n  <rating>%.1f</rating>", meta.Rating)
		}
		if meta.Premiered != "" {
			premieredTag = fmt.Sprintf("\n  <premiered>%s</premiered>", escapeXML(meta.Premiered))
		}
		if meta.Studio != "" {
			studioTag = fmt.Sprintf("\n  <studio>%s</studio>", escapeXML(meta.Studio))
		}
		if meta.Status != "" {
			statusTag = fmt.Sprintf("\n  <status>%s</status>", escapeXML(meta.Status))
		}
		for _, g := range meta.Genres {
			genresTag += fmt.Sprintf("\n  <genre>%s</genre>", escapeXML(g))
		}
	}

	content := fmt.Sprintf(`<?xml version="1.0" encoding="utf-8" standalone="yes"?>
<tvshow>
  <title>%s</title>%s
  <year>%s</year>%s%s%s%s%s%s%s
  <imdbid>%s</imdbid>
</tvshow>`, escapeXML(title), origTag, year, premieredTag, ratingTag, plotTag, genresTag, studioTag, statusTag, tmdbTag, imdbID)
	_ = os.WriteFile(path, []byte(content), 0644)
}

func writeEpisodeNFO(path, title string, season, episode int, plot, aired, thumb string) {
	thumbTag := ""
	if thumb != "" {
		thumbTag = fmt.Sprintf("\n  <thumb>%s</thumb>", escapeXML(thumb))
	}
	content := fmt.Sprintf(`<?xml version="1.0" encoding="utf-8" standalone="yes"?>
<episodedetails>
  <title>%s</title>
  <season>%d</season>
  <episode>%d</episode>
  <aired>%s</aired>
  <plot>%s</plot>%s
</episodedetails>`, escapeXML(title), season, episode, escapeXML(aired), escapeXML(plot), thumbTag)
	_ = os.WriteFile(path, []byte(content), 0644)
}

func writeSeasonNFO(path, title string, seasonNumber int) {
	content := fmt.Sprintf(`<?xml version="1.0" encoding="utf-8" standalone="yes"?>
<season>
  <title>%s</title>
  <seasonnumber>%d</seasonnumber>
</season>`, escapeXML(title), seasonNumber)
	_ = os.WriteFile(path, []byte(content), 0644)
}

type EpisodeMeta struct {
	SeasonNumber  int    `json:"season_number"`
	EpisodeNumber int    `json:"episode_number"`
	Name          string `json:"name"`
	Overview      string `json:"overview"`
	AirDate       string `json:"air_date"`
	StillPath     string `json:"still_path"`
}

func (m *Mounter) fetchShowMetadata(ctx context.Context, tconst string) *ShowMeta {
	if tconst == "" || m.indexerURL == "" {
		return nil
	}
	reqURL := fmt.Sprintf("%s/series/%s/seasons", m.indexerURL, tconst)
	req, err := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
	if err != nil {
		return nil
	}
	resp, err := m.client.Do(req)
	if err != nil {
		log.Printf("[mounter] failed to fetch show metadata from %s: %v", reqURL, err)
		return nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil
	}

	var meta ShowMeta
	if err := json.NewDecoder(resp.Body).Decode(&meta); err != nil {
		log.Printf("[mounter] failed to decode show metadata: %v", err)
		return nil
	}
	return &meta
}

func (m *Mounter) fetchMovieMetadata(ctx context.Context, tconst string) *MovieMeta {
	if tconst == "" || m.indexerURL == "" {
		return nil
	}
	reqURL := fmt.Sprintf("%s/movie/%s/metadata", m.indexerURL, tconst)
	req, err := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
	if err != nil {
		return nil
	}
	resp, err := m.client.Do(req)
	if err != nil {
		log.Printf("[mounter] failed to fetch movie metadata from %s: %v", reqURL, err)
		return nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil
	}

	var meta MovieMeta
	if err := json.NewDecoder(resp.Body).Decode(&meta); err != nil {
		log.Printf("[mounter] failed to decode movie metadata: %v", err)
		return nil
	}
	return &meta
}

func (m *Mounter) fetchEpisodesMetadata(ctx context.Context, tconst string) map[int]map[int]EpisodeMeta {
	result := make(map[int]map[int]EpisodeMeta)
	if tconst == "" || m.indexerURL == "" {
		return result
	}

	reqURL := fmt.Sprintf("%s/series/%s/episodes", m.indexerURL, tconst)
	req, err := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
	if err != nil {
		return result
	}

	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("[mounter] failed to fetch episode metadata from %s: %v", reqURL, err)
		return result
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return result
	}

	var episodes []EpisodeMeta
	if err := json.NewDecoder(resp.Body).Decode(&episodes); err != nil {
		log.Printf("[mounter] failed to decode episode metadata: %v", err)
		return result
	}

	for _, ep := range episodes {
		if _, ok := result[ep.SeasonNumber]; !ok {
			result[ep.SeasonNumber] = make(map[int]EpisodeMeta)
		}
		result[ep.SeasonNumber][ep.EpisodeNumber] = ep
	}

	return result
}

type imageDownloadTask struct {
	pathOrURL string
	destPath  string
	size      string
}

func (m *Mounter) downloadImage(ctx context.Context, pathOrURL, destPath, defaultSize string) error {
	if _, err := os.Stat(destPath); err == nil {
		return nil
	}
	if pathOrURL == "" {
		return nil
	}

	var downloadURL string
	if strings.HasPrefix(pathOrURL, "http://") || strings.HasPrefix(pathOrURL, "https://") {
		downloadURL = pathOrURL
	} else {
		cleanPath := strings.TrimPrefix(pathOrURL, "/")
		if defaultSize == "" {
			defaultSize = "w500"
		}
		downloadURL = fmt.Sprintf("https://image.tmdb.org/t/p/%s/%s", defaultSize, cleanPath)
	}

	req, err := http.NewRequestWithContext(ctx, "GET", downloadURL, nil)
	if err != nil {
		return err
	}

	resp, err := m.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d from %s", resp.StatusCode, downloadURL)
	}

	if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
		return err
	}

	out, err := os.Create(destPath)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, resp.Body)
	return err
}

func (m *Mounter) downloadImagesParallel(ctx context.Context, tasks []imageDownloadTask) {
	if len(tasks) == 0 {
		return
	}
	taskChan := make(chan imageDownloadTask, len(tasks))
	for _, t := range tasks {
		taskChan <- t
	}
	close(taskChan)

	numWorkers := 10
	if len(tasks) < numWorkers {
		numWorkers = len(tasks)
	}

	var wg sync.WaitGroup
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for t := range taskChan {
				if err := m.downloadImage(ctx, t.pathOrURL, t.destPath, t.size); err != nil {
					log.Printf("[mounter] failed downloading image %s -> %s: %v", t.pathOrURL, t.destPath, err)
				}
			}
		}()
	}
	wg.Wait()
}

func (m *Mounter) downloadPoster(ctx context.Context, tconst, destPath string) {
	if _, err := os.Stat(destPath); err == nil {
		// poster already exists
		return
	}
	if tconst == "" || m.indexerURL == "" {
		return
	}
	posterURL := fmt.Sprintf("%s/poster/%s?size=w500", m.indexerURL, tconst)
	req, err := http.NewRequestWithContext(ctx, "GET", posterURL, nil)
	if err != nil {
		log.Printf("[mounter] failed to create poster request for %s: %v", tconst, err)
		return
	}
	resp, err := m.client.Do(req)
	if err != nil {
		log.Printf("[mounter] failed to download poster for %s: %v", tconst, err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Printf("[mounter] poster endpoint %s returned status %d", posterURL, resp.StatusCode)
		return
	}

	out, err := os.Create(destPath)
	if err != nil {
		log.Printf("[mounter] failed to create poster file %s: %v", destPath, err)
		return
	}
	defer out.Close()

	if _, err := io.Copy(out, resp.Body); err != nil {
		log.Printf("[mounter] failed to save poster to %s: %v", destPath, err)
	} else {
		log.Printf("[mounter] successfully saved poster for %s to %s", tconst, destPath)
	}
}

func isSampleFile(path string, length int64, maxLen int64) bool {
	lower := strings.ToLower(filepath.ToSlash(path))
	base := strings.ToLower(filepath.Base(path))

	// If file is small (< 250 MB) and main video is significantly larger (> 500 MB)
	if length < 250*1024*1024 && maxLen > 500*1024*1024 {
		if strings.Contains(lower, "/sample") || strings.Contains(lower, "\\sample") ||
			strings.Contains(base, "sample") || strings.Contains(base, "trailer") ||
			strings.Contains(base, "featurette") || strings.Contains(base, "bonus") {
			return true
		}
	}

	// Strict directory check
	if strings.Contains(lower, "/sample/") || strings.Contains(lower, "/samples/") ||
		strings.HasPrefix(lower, "sample/") || strings.HasPrefix(lower, "samples/") {
		if length < 300*1024*1024 && maxLen > 500*1024*1024 {
			return true
		}
	}

	return false
}

func (m *Mounter) notifyJellyfin(isSeries bool, folderName, tconst string) {
	if m.jellyfinAPIKey == "" {
		log.Printf("Notice: JELLYFIN_API_KEY is not set, skipping Jellyfin notification")
		return
	}

	// 1. Discover the target library VirtualFolder ItemId
	targetFolderType := "movies"
	if isSeries {
		targetFolderType = "tvshows"
	}

	var libraryFolderID string
	vfReq, err := http.NewRequest("GET", m.jellyfinURL+"/Library/VirtualFolders", nil)
	if err == nil {
		vfReq.Header.Set("Authorization", fmt.Sprintf("MediaBrowser Token=\"%s\"", m.jellyfinAPIKey))
		if resp, err := m.client.Do(vfReq); err == nil {
			var vFolders []struct {
				CollectionType string `json:"CollectionType"`
				ItemId         string `json:"ItemId"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&vFolders); err == nil {
				for _, vf := range vFolders {
					if strings.EqualFold(vf.CollectionType, targetFolderType) {
						libraryFolderID = vf.ItemId
						break
					}
				}
			}
			resp.Body.Close()
		}
	}

	if libraryFolderID == "" {
		log.Printf("[mounter] Virtual folder for %s not found, skipping immediate refresh", targetFolderType)
		return
	}

	// 2. Immediately trigger refresh of the parent library (instant folder discovery without recursive deep-probe)
	refreshReq, err := http.NewRequest("POST", fmt.Sprintf("%s/Items/%s/Refresh", m.jellyfinURL, libraryFolderID), nil)
	if err == nil {
		refreshReq.Header.Set("Authorization", fmt.Sprintf("MediaBrowser Token=\"%s\"", m.jellyfinAPIKey))
		if resp, err := m.client.Do(refreshReq); err == nil {
			resp.Body.Close()
			log.Printf("[mounter] Triggered targeted refresh for library %s (%s)", targetFolderType, libraryFolderID)
		}
	}

	if folderName == "" && tconst == "" {
		return
	}

	// 3. Wait briefly for folder discovery, locate the item ID, and trigger targeted refresh
	// This ensures all NFO metadata is immediately bound with titles and overviews ONLY for this item.
	go func() {
		imdbPattern := ""
		if tconst != "" {
			imdbPattern = fmt.Sprintf("[imdbid-%s]", tconst)
		}

		itemLabel := "Movie"
		if isSeries {
			itemLabel = "Series"
		}

		for attempt := 1; attempt <= 10; attempt++ {
			time.Sleep(500 * time.Millisecond)

			itemsURL := fmt.Sprintf("%s/Items?parentId=%s&fields=Path", m.jellyfinURL, libraryFolderID)
			itemReq, err := http.NewRequest("GET", itemsURL, nil)
			if err != nil {
				continue
			}
			itemReq.Header.Set("Authorization", fmt.Sprintf("MediaBrowser Token=\"%s\"", m.jellyfinAPIKey))
			resp, err := m.client.Do(itemReq)
			if err != nil {
				continue
			}

			var itemsResp struct {
				Items []struct {
					ID   string `json:"Id"`
					Name string `json:"Name"`
					Path string `json:"Path"`
				} `json:"Items"`
			}
			decodeErr := json.NewDecoder(resp.Body).Decode(&itemsResp)
			resp.Body.Close()
			if decodeErr != nil {
				continue
			}

			var matchedItemID string
			for _, item := range itemsResp.Items {
				if (folderName != "" && strings.Contains(item.Path, folderName)) || (imdbPattern != "" && strings.Contains(item.Path, imdbPattern)) {
					matchedItemID = item.ID
					break
				}
			}

			if matchedItemID != "" {
				seriesRefreshURL := fmt.Sprintf("%s/Items/%s/Refresh?MetadataRefreshMode=FullRefresh&ReplaceAllMetadata=true&ImageRefreshMode=FullRefresh&ReplaceAllImages=true&Recursive=true", m.jellyfinURL, matchedItemID)
				srReq, err := http.NewRequest("POST", seriesRefreshURL, nil)
				if err == nil {
					srReq.Header.Set("Authorization", fmt.Sprintf("MediaBrowser Token=\"%s\"", m.jellyfinAPIKey))
					if sResp, err := m.client.Do(srReq); err == nil {
						sResp.Body.Close()
						log.Printf("[mounter] Triggered recursive refresh for %s item %s (%s)", itemLabel, folderName, matchedItemID)
					}
				}
				return
			}
		}
		log.Printf("[mounter] %s item for %s not discovered in Jellyfin after 5s polling", itemLabel, folderName)
	}()
}

func determineVersionLabel(req MountRequest, fallback string) string {
	if req.VersionName != "" {
		cleaned := sanitizeFilename(req.VersionName)
		if cleaned != "" {
			return cleaned
		}
	}
	resLower := strings.ToLower(strings.TrimSpace(req.Resolution))
	switch resLower {
	case "4k", "2160p", "uhd":
		return "4K UHD"
	case "1080p", "fhd":
		return "1080p"
	case "720p", "hd":
		return "720p"
	case "lq", "480p", "sd":
		return "SD"
	default:
		if req.Resolution != "" {
			return sanitizeFilename(req.Resolution)
		}
	}
	if fallback != "" {
		return fallback
	}
	return "1080p"
}

func extractTitlePrefixFromFolder(folderName string) string {
	title := folderName
	if idx := strings.Index(title, " [imdbid-"); idx != -1 {
		title = title[:idx]
	}
	if idx := strings.LastIndex(title, " ("); idx != -1 {
		title = title[:idx]
	}
	return strings.TrimSpace(title)
}

func (m *Mounter) findExistingFolder(targetType, tconst string) (folderName string, titlePrefix string) {
	if tconst == "" {
		return "", ""
	}
	targetPattern := fmt.Sprintf("[imdbid-%s]", tconst)

	typesToCheck := []string{targetType}
	if targetType == "" {
		typesToCheck = []string{"movies", "shows"}
	}

	for _, t := range typesToCheck {
		bases := []string{
			filepath.Join(m.mediaLibraryPath, t),
			filepath.Join(m.mediaSourcePath, t),
		}
		for _, base := range bases {
			entries, err := os.ReadDir(base)
			if err != nil {
				continue
			}
			for _, entry := range entries {
				if !entry.IsDir() {
					continue
				}
				name := entry.Name()
				if strings.Contains(name, targetPattern) {
					return name, extractTitlePrefixFromFolder(name)
				}
			}
		}
	}
	return "", ""
}

func (m *Mounter) mergeSeriesEpisodesInJellyfin(folderName, tconst string) {
	if m.jellyfinAPIKey == "" {
		return
	}
	imdbPattern := ""
	if tconst != "" {
		imdbPattern = fmt.Sprintf("[imdbid-%s]", tconst)
	}

	mergedKeys := make(map[string]bool)

	// Poll Jellyfin after library refresh to discover new episode versions and merge them
	for attempt := 1; attempt <= 20; attempt++ {
		time.Sleep(2 * time.Second)

		// 1. Find series ID by folderName or imdbPattern
		seriesURL := fmt.Sprintf("%s/Items?recursive=true&includeItemTypes=Series&fields=Path", m.jellyfinURL)
		req, err := http.NewRequest("GET", seriesURL, nil)
		if err != nil {
			continue
		}
		req.Header.Set("Authorization", fmt.Sprintf("MediaBrowser Token=\"%s\"", m.jellyfinAPIKey))
		resp, err := m.client.Do(req)
		if err != nil {
			continue
		}

		var seriesResp struct {
			Items []struct {
				ID   string `json:"Id"`
				Path string `json:"Path"`
			} `json:"Items"`
		}
		decodeErr := json.NewDecoder(resp.Body).Decode(&seriesResp)
		resp.Body.Close()
		if decodeErr != nil {
			continue
		}

		var seriesID string
		for _, s := range seriesResp.Items {
			if (folderName != "" && strings.Contains(s.Path, folderName)) || (imdbPattern != "" && strings.Contains(s.Path, imdbPattern)) {
				seriesID = s.ID
				break
			}
		}
		if seriesID == "" {
			continue
		}

		// 2. Fetch episodes for this series
		epURL := fmt.Sprintf("%s/Shows/%s/Episodes?fields=Path,IndexNumber,ParentIndexNumber,MediaSources", m.jellyfinURL, seriesID)
		epReq, err := http.NewRequest("GET", epURL, nil)
		if err != nil {
			continue
		}
		epReq.Header.Set("Authorization", fmt.Sprintf("MediaBrowser Token=\"%s\"", m.jellyfinAPIKey))
		epResp, err := m.client.Do(epReq)
		if err != nil {
			continue
		}

		var epList struct {
			Items []struct {
				ID                string        `json:"Id"`
				Path              string        `json:"Path"`
				IndexNumber       int           `json:"IndexNumber"`
				ParentIndexNumber int           `json:"ParentIndexNumber"`
				MediaSources      []interface{} `json:"MediaSources"`
			} `json:"Items"`
		}
		epDecodeErr := json.NewDecoder(epResp.Body).Decode(&epList)
		epResp.Body.Close()
		if epDecodeErr != nil {
			continue
		}

		// Group episodes by Season + Episode
		type epCandidate struct {
			id               string
			path             string
			isPrimary        bool
			mediaSourceCount int
		}
		groups := make(map[string][]epCandidate)
		for _, ep := range epList.Items {
			s, e := parseSeasonEpisode(ep.Path)
			if s == 0 {
				s = ep.ParentIndexNumber
			}
			if e == 0 {
				e = ep.IndexNumber
			}
			if s > 0 && e > 0 {
				key := fmt.Sprintf("S%02dE%02d", s, e)
				isPrimary := ep.IndexNumber > 0
				groups[key] = append(groups[key], epCandidate{
					id:               ep.ID,
					path:             ep.Path,
					isPrimary:        isPrimary,
					mediaSourceCount: len(ep.MediaSources),
				})
			}
		}

		for key, list := range groups {
			if mergedKeys[key] {
				continue
			}
			if len(list) > 1 {
				allAlreadyMerged := true
				for _, item := range list {
					if item.mediaSourceCount < 2 {
						allAlreadyMerged = false
						break
					}
				}
				if allAlreadyMerged {
					mergedKeys[key] = true
					continue
				}

				// Sort so primary episode is first
				sort.SliceStable(list, func(i, j int) bool {
					if list[i].isPrimary != list[j].isPrimary {
						return list[i].isPrimary
					}
					// Prefer path without version label
					hasVerI := strings.Contains(list[i].path, " - 1080p") || strings.Contains(list[i].path, " - 4k") || strings.Contains(list[i].path, " - SD") || strings.Contains(list[i].path, " - Remux")
					hasVerJ := strings.Contains(list[j].path, " - 1080p") || strings.Contains(list[j].path, " - 4k") || strings.Contains(list[j].path, " - SD") || strings.Contains(list[j].path, " - Remux")
					if !hasVerI && hasVerJ {
						return true
					}
					return false
				})

				var ids []string
				for _, item := range list {
					ids = append(ids, item.id)
				}
				mergeURL := fmt.Sprintf("%s/Videos/MergeVersions?ids=%s", m.jellyfinURL, strings.Join(ids, ","))
				mReq, err := http.NewRequest("POST", mergeURL, nil)
				if err == nil {
					mReq.Header.Set("Authorization", fmt.Sprintf("MediaBrowser Token=\"%s\"", m.jellyfinAPIKey))
					if mResp, err := m.client.Do(mReq); err == nil {
						mResp.Body.Close()
						log.Printf("[mounter] Merged %d versions for %s %s", len(ids), folderName, key)
						mergedKeys[key] = true
					}
				}
			}
		}

		if len(groups) > 0 && len(mergedKeys) == len(groups) {
			log.Printf("[mounter] All %d episode groups merged for %s on attempt %d", len(groups), folderName, attempt)
			return
		}
	}
	log.Printf("[mounter] mergeSeriesEpisodesInJellyfin finished for %s (merged %d groups)", folderName, len(mergedKeys))
}

func sanitizeFilename(s string) string {
	re := regexp.MustCompile(`[/\\?%*:|"<>]+`)
	s = re.ReplaceAllString(s, " ")
	return strings.TrimSpace(s)
}

func escapeXML(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, "\"", "&quot;")
	return s
}

var (
	reSXXEYY         = regexp.MustCompile(`(?i)[sS](\d{1,2})[eE](\d{1,3})`)
	reNxNN           = regexp.MustCompile(`(?i)(?:^|[\s._\-\[(])(\d{1,2})x(\d{1,3})(?:[\s._\-\])]|$)`)
	reSeasonDir      = regexp.MustCompile(`(?i)(?:сезон|season)\s*0*(\d{1,2})|(?:^|[/\\_\s\-])[sS]0*(\d{1,2})(?:[/\\_\s\-]|\b)`)
	reEpisodeWord    = regexp.MustCompile(`(?i)(?:серия|эпизод|ep|episode)[._\s-]*0*(\d{1,3})`)
	reLeadingEp      = regexp.MustCompile(`^0*([1-9]\d{0,2})(?:[\s._\-]|$)`)
	reTrailingEp     = regexp.MustCompile(`[\s._\-]0*([1-9]\d{0,2})$`)
)

func parseSeasonEpisode(path string) (int, int) {
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
		// Check each directory element from closest parent upwards
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

	// If season wasn't found in directory, check the full path for Season X or SXX
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
		// Try trailing number without extension (e.g. "The Sopranos - 01.mkv")
		noExt := strings.TrimSuffix(base, filepath.Ext(base))
		if em := reTrailingEp.FindStringSubmatch(noExt); len(em) >= 2 {
			epNum, _ := strconv.Atoi(em[1])
			// Exclude video codec and resolution numbers
			isCodecOrRes := epNum == 264 || epNum == 265 || epNum == 720 || epNum == 1080 || epNum == 2160 || epNum == 480 || epNum == 576
			precededByCodec := false
			idx := strings.LastIndex(noExt, em[0])
			if idx > 0 {
				prefix := strings.ToLower(noExt[:idx])
				if strings.HasSuffix(prefix, "h") || strings.HasSuffix(prefix, "x") || strings.HasSuffix(prefix, "h.") || strings.HasSuffix(prefix, "x.") {
					precededByCodec = true
				}
			}
			if !isCodecOrRes && !precededByCodec {
				e = epNum
			}
		}
	}

	return s, e
}

var reHash = regexp.MustCompile(`(?i)(?:link=|urn:btih:|\"hash\":\s*\"|\"magnet\":[^\"]*urn:btih:)([a-f0-9]{40})`)

func (m *Mounter) extractHashesFromDirectory(dir string) []string {
	var hashes []string
	seen := make(map[string]bool)

	_ = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if strings.HasSuffix(strings.ToLower(info.Name()), ".mkv") {
			content, err := os.ReadFile(path)
			if err == nil {
				matches := reHash.FindAllStringSubmatch(string(content), -1)
				for _, match := range matches {
					if len(match) > 1 {
						h := strings.ToLower(match[1])
						if !seen[h] {
							seen[h] = true
							hashes = append(hashes, h)
						}
					}
				}
			}
		}
		return nil
	})

	return hashes
}

func (m *Mounter) removeTorrentFromGoStorm(ctx context.Context, hash string) error {
	payload := map[string]string{
		"action": "rem",
		"hash":   hash,
	}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, "POST", m.gostormURL+"/torrents", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := m.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return nil
}

func (m *Mounter) notifyJellyfinLibraryScan(isSeries bool) {
	if m.jellyfinAPIKey == "" {
		return
	}
	req, err := http.NewRequest("POST", m.jellyfinURL+"/Library/Refresh", nil)
	if err == nil {
		req.Header.Set("Authorization", fmt.Sprintf("MediaBrowser Token=\"%s\"", m.jellyfinAPIKey))
		if resp, err := m.client.Do(req); err == nil {
			resp.Body.Close()
			log.Printf("[mounter] Triggered Jellyfin /Library/Refresh")
		}
	}
}

func (m *Mounter) UnmountTorrent(ctx context.Context, req UnmountRequest) (*UnmountResponse, error) {
	if req.Tconst == "" && req.FolderName == "" {
		return nil, fmt.Errorf("tconst or folder_name is required")
	}

	var removedFiles []string
	var removedSwarms []string
	seenSwarms := make(map[string]bool)

	types := []string{"movies", "shows"}
	if req.Type != "" {
		if strings.EqualFold(req.Type, "movie") {
			types = []string{"movies"}
		} else if strings.EqualFold(req.Type, "series") || strings.EqualFold(req.Type, "tvSeries") || strings.EqualFold(req.Type, "tvMiniSeries") {
			types = []string{"shows"}
		}
	}

	targetPattern := ""
	if req.Tconst != "" {
		targetPattern = fmt.Sprintf("[imdbid-%s]", req.Tconst)
	}

	matchedFolders := make(map[string]string) // folderName -> mediaType

	for _, t := range types {
		sourceBase := filepath.Join(m.mediaSourcePath, t)
		if entries, err := os.ReadDir(sourceBase); err == nil {
			for _, entry := range entries {
				if !entry.IsDir() {
					continue
				}
				name := entry.Name()
				if req.FolderName != "" && name == req.FolderName {
					matchedFolders[name] = t
				} else if targetPattern != "" && strings.Contains(name, targetPattern) {
					matchedFolders[name] = t
				}
			}
		}

		libBase := filepath.Join(m.mediaLibraryPath, t)
		if entries, err := os.ReadDir(libBase); err == nil {
			for _, entry := range entries {
				if !entry.IsDir() {
					continue
				}
				name := entry.Name()
				if req.FolderName != "" && name == req.FolderName {
					matchedFolders[name] = t
				} else if targetPattern != "" && strings.Contains(name, targetPattern) {
					matchedFolders[name] = t
				}
			}
		}
	}

	if len(matchedFolders) == 0 {
		return &UnmountResponse{
			Success: true,
			Message: "Item was not mounted",
		}, nil
	}

	for folderName, mediaType := range matchedFolders {
		sourceDir := filepath.Join(m.mediaSourcePath, mediaType, folderName)
		libDir := filepath.Join(m.mediaLibraryPath, mediaType, folderName)

		// 1. Find all torrent hashes from stubs in sourceDir
		hashes := m.extractHashesFromDirectory(sourceDir)
		for _, h := range hashes {
			if !seenSwarms[h] {
				seenSwarms[h] = true
				if err := m.removeTorrentFromGoStorm(ctx, h); err == nil {
					removedSwarms = append(removedSwarms, h)
					log.Printf("[mounter] Removed torrent %s from GoStorm for %s", h, folderName)
				} else {
					log.Printf("[mounter] Warning: failed to remove torrent %s from GoStorm: %v", h, err)
				}
			}
		}

		// 2. Remove directories
		if err := os.RemoveAll(sourceDir); err == nil {
			removedFiles = append(removedFiles, sourceDir)
		} else {
			log.Printf("[mounter] Warning: failed to remove source dir %s: %v", sourceDir, err)
		}

		if err := os.RemoveAll(libDir); err == nil {
			removedFiles = append(removedFiles, libDir)
		} else {
			log.Printf("[mounter] Warning: failed to remove lib dir %s: %v", libDir, err)
		}

		// 3. Notify Jellyfin
		m.notifyJellyfinLibraryScan(mediaType == "shows")
	}

	return &UnmountResponse{
		Success:       true,
		Message:       fmt.Sprintf("Successfully unmounted %d folder(s)", len(matchedFolders)),
		RemovedFiles:  removedFiles,
		RemovedSwarms: removedSwarms,
	}, nil
}

func (m *Mounter) ReconcileOrphanedStubs(ctx context.Context) error {
	types := []string{"movies", "shows"}
	for _, t := range types {
		sourceBase := filepath.Join(m.mediaSourcePath, t)
		libBase := filepath.Join(m.mediaLibraryPath, t)

		entries, err := os.ReadDir(sourceBase)
		if err != nil {
			continue
		}

		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			folderName := entry.Name()
			sourceDir := filepath.Join(sourceBase, folderName)
			libDir := filepath.Join(libBase, folderName)

			// Check if library folder exists
			libStat, err := os.Stat(libDir)
			if os.IsNotExist(err) {
				log.Printf("[reconciler] Detected orphaned source folder: %s/%s (missing in library)", t, folderName)
				hashes := m.extractHashesFromDirectory(sourceDir)
				for _, h := range hashes {
					_ = m.removeTorrentFromGoStorm(ctx, h)
					log.Printf("[reconciler] Removed torrent %s from GoStorm for orphaned %s", h, folderName)
				}
				_ = os.RemoveAll(sourceDir)
				continue
			}

			// If library folder exists, check if all video files inside were deleted
			if err == nil && libStat.IsDir() {
				hasVideo := false
				_ = filepath.Walk(libDir, func(path string, info os.FileInfo, err error) error {
					if err != nil || info.IsDir() {
						return nil
					}
					ext := strings.ToLower(filepath.Ext(path))
					if ext == ".mkv" || ext == ".mp4" || ext == ".avi" || ext == ".mov" || ext == ".ts" {
						hasVideo = true
						return filepath.SkipAll
					}
					return nil
				})

				if !hasVideo {
					log.Printf("[reconciler] Detected empty video folder in library: %s/%s. Purging source and library.", t, folderName)
					hashes := m.extractHashesFromDirectory(sourceDir)
					for _, h := range hashes {
						_ = m.removeTorrentFromGoStorm(ctx, h)
						log.Printf("[reconciler] Removed torrent %s from GoStorm for orphaned %s", h, folderName)
					}
					_ = os.RemoveAll(sourceDir)
					_ = os.RemoveAll(libDir)
				}
			}
		}
	}
	return nil
}

func (m *Mounter) StartReconciler(ctx context.Context, interval time.Duration) {
	go func() {
		// Run initial reconciliation on startup after brief delay
		select {
		case <-ctx.Done():
			return
		case <-time.After(5 * time.Second):
			_ = m.ReconcileOrphanedStubs(ctx)
		}

		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_ = m.ReconcileOrphanedStubs(ctx)
			}
		}
	}()
}

func (m *Mounter) GetMountedStatus(tconst string) *MountedStatusResponse {
	res := &MountedStatusResponse{
		Mounted: false,
		Tconst:  tconst,
	}
	if tconst == "" {
		return res
	}

	targetPattern := fmt.Sprintf("[imdbid-%s]", tconst)
	types := []string{"movies", "shows"}

	for _, t := range types {
		libBase := filepath.Join(m.mediaLibraryPath, t)
		entries, err := os.ReadDir(libBase)
		if err != nil {
			continue
		}

		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			if strings.Contains(entry.Name(), targetPattern) {
				folderName := entry.Name()
				fullLibPath := filepath.Join(libBase, folderName)
				fullSourcePath := filepath.Join(m.mediaSourcePath, t, folderName)

				var files []string
				_ = filepath.Walk(fullLibPath, func(path string, info os.FileInfo, err error) error {
					if err != nil || info.IsDir() {
						return nil
					}
					ext := strings.ToLower(filepath.Ext(path))
					if ext == ".mkv" || ext == ".mp4" || ext == ".avi" || ext == ".mov" || ext == ".ts" {
						rel, _ := filepath.Rel(fullLibPath, path)
						files = append(files, rel)
					}
					return nil
				})

				if len(files) > 0 {
					res.Mounted = true
					res.Type = t
					res.FolderName = folderName
					res.LibraryPath = fullLibPath
					res.SourcePath = fullSourcePath
					res.FileCount = len(files)
					res.MountedFiles = files

					seasonMap := make(map[int]bool)
					versionMap := make(map[string]bool)

					for _, f := range files {
						base := filepath.Base(f)
						// Extract season if series
						if t == "shows" {
							s, _ := parseSeasonEpisode(f)
							if s > 0 {
								seasonMap[s] = true
							}
						}

						// Extract version label
						// Pattern: <base_name> - <Version>.ext
						// For movie: <FolderName> - <Version>.ext
						// For episode: <Show> - SXXEYY - <Version>.ext
						ext := filepath.Ext(base)
						nameWithoutExt := strings.TrimSuffix(base, ext)
						idx := strings.LastIndex(nameWithoutExt, " - ")
						if idx != -1 {
							vCandidate := strings.TrimSpace(nameWithoutExt[idx+3:])
							// Make sure it's not the SXXEYY part itself (e.g. "Show - S01E01")
							if !reSXXEYY.MatchString(vCandidate) && !reNxNN.MatchString(vCandidate) && vCandidate != "" {
								versionMap[vCandidate] = true
							}
						}
					}

					var seasons []int
					for s := range seasonMap {
						seasons = append(seasons, s)
					}
					sort.Ints(seasons)
					res.Seasons = seasons

					var versions []string
					for v := range versionMap {
						versions = append(versions, v)
					}
					sort.Strings(versions)
					res.Versions = versions

					return res
				}
			}
		}
	}

	return res
}

func (m *Mounter) HandleJellyfinItemDeleted(ctx context.Context, webhook JellyfinDeletedWebhook) error {
	log.Printf("[webhook] ItemDeleted received: name=%q seriesName=%q itemType=%q path=%q imdbId=%q",
		webhook.Name, webhook.SeriesName, webhook.ItemType, webhook.Path, webhook.ImdbId)

	reqType := ""
	if strings.EqualFold(webhook.ItemType, "movie") {
		reqType = "movie"
	} else if strings.EqualFold(webhook.ItemType, "series") || strings.EqualFold(webhook.ItemType, "episode") || strings.EqualFold(webhook.ItemType, "season") {
		reqType = "series"
	}

	// 1. If path is provided (e.g. /media/movies/... or /media/shows/...)
	if webhook.Path != "" {
		rel := strings.TrimPrefix(webhook.Path, "/media/")
		rel = strings.TrimPrefix(rel, "/")
		parts := strings.Split(rel, "/")
		if len(parts) >= 2 {
			mediaType := parts[0] // "movies" or "shows"
			folderName := parts[1]
			_, _ = m.UnmountTorrent(ctx, UnmountRequest{
				Type:       mediaType,
				FolderName: folderName,
				Tconst:     webhook.ImdbId,
			})
			return m.ReconcileOrphanedStubs(ctx)
		}
	}

	// 2. If ImdbId is present without path, scope by reqType if known
	if webhook.ImdbId != "" {
		_, _ = m.UnmountTorrent(ctx, UnmountRequest{
			Tconst: webhook.ImdbId,
			Type:   reqType,
		})
	}

	// 3. Trigger reconciliation to catch any leftover stubs
	return m.ReconcileOrphanedStubs(ctx)
}
