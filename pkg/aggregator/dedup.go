package aggregator

import (
	"context"
	"log"
	"math"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"tracker-proxy/pkg/cache"
	"tracker-proxy/pkg/models"
	"tracker-proxy/pkg/tracker"
)

// SizeToleranceGB defines the maximum allowed difference in GB between two torrents
// to consider them duplicate candidates.
const SizeToleranceGB = 0.105

// Magnet announce tracker regex / extraction helper
func extractTrackersFromMagnet(rawMagnet string) []string {
	if rawMagnet == "" {
		return nil
	}
	u, err := url.Parse(rawMagnet)
	if err != nil {
		return nil
	}
	values := u.Query()
	return values["tr"]
}

var trackerKnownAnnounces = map[string][]string{
	"rutracker": {
		"http://bt.t-ru.org/ann?magnet",
		"http://bt2.t-ru.org/ann?magnet",
		"http://bt3.t-ru.org/ann?magnet",
		"http://bt4.t-ru.org/ann?magnet",
	},
	"nnmclub": {
		"http://bt.searchtor.to/announce",
		"http://bt02.nnm-club.cc:2710/announce",
		"http://bt02.nnm-club.to:2710/announce",
	},
	"rutor": {
		"udp://opentor.net:6969",
		"udp://opentor.org:2710",
		"http://retracker.local/announce",
	},
}

var globalOpenTrackers = []string{
	"udp://tracker.opentrackr.org:1337/announce",
	"udp://open.stealth.si:80/announce",
	"udp://tracker.torrent.eu.org:451/announce",
}

// BuildMultiTrackerMagnet constructs a unified magnet link combining announce trackers from all sources.
func BuildMultiTrackerMagnet(infoHash, title string, existingMagnets []string, activeTrackers []string) string {
	if infoHash == "" {
		return ""
	}

	trSet := make(map[string]struct{})
	var orderedTrackers []string

	addTracker := func(tr string) {
		trClean := strings.TrimSpace(tr)
		if trClean == "" {
			return
		}
		if _, exists := trSet[trClean]; !exists {
			trSet[trClean] = struct{}{}
			orderedTrackers = append(orderedTrackers, trClean)
		}
	}

	// 1. Extract trackers from existing magnet links
	for _, m := range existingMagnets {
		for _, tr := range extractTrackersFromMagnet(m) {
			addTracker(tr)
		}
	}

	// 2. Add known tracker announces for each active tracker
	for _, trackerName := range activeTrackers {
		if announces, ok := trackerKnownAnnounces[strings.ToLower(trackerName)]; ok {
			for _, tr := range announces {
				addTracker(tr)
			}
		}
	}

	// 3. Add reliable global open trackers for DHT/PEX acceleration
	for _, tr := range globalOpenTrackers {
		addTracker(tr)
	}

	var sb strings.Builder
	sb.WriteString("magnet:?xt=urn:btih:")
	sb.WriteString(strings.ToLower(infoHash))
	if title != "" {
		sb.WriteString("&dn=")
		sb.WriteString(url.QueryEscape(title))
	}
	for _, tr := range orderedTrackers {
		sb.WriteString("&tr=")
		sb.WriteString(url.QueryEscape(tr))
	}

	return sb.String()
}

// findCandidateIndices checks whether item i has at least one other item j from a different tracker
// within the size tolerance window.
func findCandidateIndices(results []models.TorrentResult) map[int]bool {
	candidates := make(map[int]bool)
	n := len(results)
	if n < 2 {
		return candidates
	}

	sizesGB := make([]float64, n)
	for i := range results {
		sizesGB[i] = float64(results[i].Size) / (1024.0 * 1024.0 * 1024.0)
	}

	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			if results[i].Tracker == results[j].Tracker {
				continue
			}
			if math.Abs(sizesGB[i]-sizesGB[j]) <= SizeToleranceGB {
				candidates[i] = true
				candidates[j] = true
			}
		}
	}

	return candidates
}

// DeduplicateAndMerge merges duplicate releases across trackers by InfoHash.
func DeduplicateAndMerge(
	ctx context.Context,
	results []models.TorrentResult,
	cacheStore *cache.Store,
	resolvers map[string]tracker.InfoHashResolver,
	resolveTimeout time.Duration,
) []models.TorrentResult {
	if len(results) == 0 {
		return results
	}

	// 1. Identify candidates within size tolerance (+-0.105 GB) from different trackers
	candidateIndices := findCandidateIndices(results)

	// 2. Resolve InfoHashes for candidates and top unhashed releases
	type fetchTask struct {
		index   int
		tracker string
		id      string
	}
	var tasks []fetchTask
	taskIndices := make(map[int]bool)

	// First pass: resolve from bbolt cache (0 ms)
	for i := range results {
		res := &results[i]
		if res.InfoHash != "" {
			res.InfoHash = strings.ToLower(res.InfoHash)
			continue
		}
		if cacheStore != nil {
			if cachedHash, found, err := cacheStore.GetTopicHash(res.Tracker, res.ID); err == nil && found && cachedHash != "" {
				res.InfoHash = strings.ToLower(cachedHash)
			}
		}
	}

	// Candidates for merging get highest priority for live topic page resolution
	for idx := range candidateIndices {
		res := &results[idx]
		if res.InfoHash != "" {
			continue
		}
		if _, ok := resolvers[res.Tracker]; ok && res.ID != "" {
			tasks = append(tasks, fetchTask{
				index:   idx,
				tracker: res.Tracker,
				id:      res.ID,
			})
			taskIndices[idx] = true
		}
	}

	// If spare task capacity exists, also resolve top popular unhashed releases (e.g. multi-season packs)
	if len(tasks) < 8 {
		for i := range results {
			if len(tasks) >= 8 {
				break
			}
			res := &results[i]
			if res.InfoHash != "" || taskIndices[i] {
				continue
			}
			if _, ok := resolvers[res.Tracker]; ok && res.ID != "" {
				tasks = append(tasks, fetchTask{
					index:   i,
					tracker: res.Tracker,
					id:      res.ID,
				})
				taskIndices[i] = true
			}
		}
	}

	// Prioritize tasks by torrent seed count descending (resolve popular releases first)
	sort.Slice(tasks, func(i, j int) bool {
		return results[tasks[i].index].Seeds > results[tasks[j].index].Seeds
	})

	// Cap max uncached tasks to resolve per search to avoid tracker rate limits
	if len(tasks) > 10 {
		tasks = tasks[:10]
	}

	// Fetch missing hashes concurrently with per-tracker concurrency limits and timeout
	if len(tasks) > 0 {
		if resolveTimeout <= 0 {
			resolveTimeout = 3500 * time.Millisecond
		}
		resolveCtx, cancel := context.WithTimeout(ctx, resolveTimeout)
		defer cancel()

		// Semaphores per tracker to avoid triggering rate limit / HTTP 503
		trackerSems := map[string]chan struct{}{
			"nnmclub":   make(chan struct{}, 2),
			"rutracker": make(chan struct{}, 2),
		}

		var wg sync.WaitGroup
		for _, task := range tasks {
			resolver := resolvers[task.tracker]
			if resolver == nil {
				continue
			}

			sem, hasSem := trackerSems[task.tracker]
			if !hasSem {
				sem = make(chan struct{}, 2)
				trackerSems[task.tracker] = sem
			}

			// Add slight pacing between launches per tracker
			time.Sleep(75 * time.Millisecond)

			wg.Add(1)
			go func(t fetchTask, r tracker.InfoHashResolver, s chan struct{}) {
				defer wg.Done()
				s <- struct{}{}
				defer func() { <-s }()

				hash, err := r.FetchInfoHash(resolveCtx, t.id)
				if err != nil {
					log.Printf("[dedup] failed to resolve info_hash for %s:%s: %v", t.tracker, t.id, err)
					return
				}
				cleanHash := strings.ToLower(strings.TrimSpace(hash))
				if cleanHash != "" {
					results[t.index].InfoHash = cleanHash
					if cacheStore != nil {
						_ = cacheStore.SetTopicHash(t.tracker, t.id, cleanHash)
					}
				}
			}(task, resolver, sem)
		}
		wg.Wait()
	}

	// 3. Group by InfoHash
	// Hashes map: hash -> indices of torrents with this hash
	hashMap := make(map[string][]int)
	// Track which torrents were merged into a group
	mergedIndices := make(map[int]bool)

	for i := range results {
		hash := strings.ToLower(strings.TrimSpace(results[i].InfoHash))
		if hash != "" {
			hashMap[hash] = append(hashMap[hash], i)
		}
	}

	var finalResults []models.TorrentResult

	// Process multi-item hash groups
	for hash, indices := range hashMap {
		if len(indices) < 2 {
			continue
		}

		// Verify that at least 2 different trackers are present (or allow multi-tracker merge)
		trackersPresent := make(map[string]bool)
		for _, idx := range indices {
			trackersPresent[results[idx].Tracker] = true
		}
		if len(trackersPresent) < 2 {
			continue
		}

		// Mark all indices in this group as merged
		for _, idx := range indices {
			mergedIndices[idx] = true
		}

		// Merge items into one unified TorrentResult
		merged := mergeTorrentGroup(results, indices, hash)
		finalResults = append(finalResults, merged)
	}

	// 4. Add all non-merged items
	for i := range results {
		if !mergedIndices[i] {
			item := results[i]
			if len(item.Trackers) == 0 {
				item.Trackers = []string{item.Tracker}
			}
			if item.Magnet == "" && item.InfoHash != "" {
				item.Magnet = BuildMultiTrackerMagnet(item.InfoHash, item.Title, nil, item.Trackers)
			}
			if len(item.Sources) == 0 {
				item.Sources = []models.TorrentSource{
					{
						Tracker:     item.Tracker,
						ID:          item.ID,
						Title:       item.Title,
						Size:        item.Size,
						SizeHuman:   item.SizeHuman,
						Seeds:       item.Seeds,
						Leeches:     item.Leeches,
						Magnet:      item.Magnet,
						DownloadURL: item.DownloadURL,
						DetailsURL:  item.DetailsURL,
					},
				}
			}
			finalResults = append(finalResults, item)
		}
	}

	return finalResults
}

// trackerPriority ranks tracker quality/cleanliness for choosing primary title and metadata.
func trackerPriority(tr string) int {
	switch tr {
	case "rutracker":
		return 3
	case "nnmclub":
		return 2
	case "rutor":
		return 1
	default:
		return 0
	}
}

func mergeTorrentGroup(results []models.TorrentResult, indices []int, hash string) models.TorrentResult {
	// Sort indices by tracker priority (highest first) then seeds (highest first)
	sort.Slice(indices, func(a, b int) bool {
		pA := trackerPriority(results[indices[a]].Tracker)
		pB := trackerPriority(results[indices[b]].Tracker)
		if pA != pB {
			return pA > pB
		}
		return results[indices[a]].Seeds > results[indices[b]].Seeds
	})

	primary := results[indices[0]]

	var totalSeeds int
	var totalLeeches int
	var trackers []string
	trackerSeen := make(map[string]bool)
	var sources []models.TorrentSource
	var existingMagnets []string

	// Union seasons and resolution
	seasonsMap := make(map[int]bool)
	isComplete := false
	bestRes := primary.Resolution

	for _, idx := range indices {
		item := results[idx]
		totalSeeds += item.Seeds
		totalLeeches += item.Leeches

		if !trackerSeen[item.Tracker] {
			trackerSeen[item.Tracker] = true
			trackers = append(trackers, item.Tracker)
		}

		if item.Magnet != "" {
			existingMagnets = append(existingMagnets, item.Magnet)
		}

		sources = append(sources, models.TorrentSource{
			Tracker:     item.Tracker,
			ID:          item.ID,
			Title:       item.Title,
			Size:        item.Size,
			SizeHuman:   item.SizeHuman,
			Seeds:       item.Seeds,
			Leeches:     item.Leeches,
			Magnet:      item.Magnet,
			DownloadURL: item.DownloadURL,
			DetailsURL:  item.DetailsURL,
		})

		for _, s := range item.Seasons {
			seasonsMap[s] = true
		}
		if item.IsComplete {
			isComplete = true
		}
		if bestRes == "" || bestRes == "lq" {
			if item.Resolution == "4k" || item.Resolution == "1080p" {
				bestRes = item.Resolution
			}
		} else if bestRes == "1080p" && item.Resolution == "4k" {
			bestRes = "4k"
		}
	}

	var mergedSeasons []int
	for s := range seasonsMap {
		mergedSeasons = append(mergedSeasons, s)
	}
	sort.Ints(mergedSeasons)

	// Combine magnet with announce trackers from all merged sources + global trackers
	unifiedMagnet := BuildMultiTrackerMagnet(hash, primary.Title, existingMagnets, trackers)

	// Determine best exact byte size: choose source with non-zero size and highest tracker priority
	bestSize := primary.Size
	bestSizeHuman := primary.SizeHuman
	for _, src := range sources {
		if trackerPriority(src.Tracker) >= 2 && src.Size > 0 {
			bestSize = src.Size
			bestSizeHuman = src.SizeHuman
			break
		}
	}

	merged := primary
	merged.Trackers = trackers
	merged.Sources = sources
	merged.Seeds = totalSeeds
	merged.Leeches = totalLeeches
	merged.Size = bestSize
	merged.SizeHuman = bestSizeHuman
	merged.Magnet = unifiedMagnet
	merged.InfoHash = hash
	merged.Seasons = mergedSeasons
	merged.IsComplete = isComplete
	merged.Resolution = bestRes

	return merged
}
