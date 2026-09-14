package transcode

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Profile represents a predefined video transcoding profile.
type Profile struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Description string `json:"description"`
	MaxHeight   int    `json:"max_height"`
	BitrateKbps int    `json:"bitrate_kbps"`
	MaxRateKbps int    `json:"maxrate_kbps"`
	BufSizeKbps int    `json:"bufsize_kbps"`
	AudioKbps   int    `json:"audio_kbps"`
	IsDirect    bool   `json:"is_direct"`
}

// AvailableProfiles returns the list of supported transcoding profiles in priority order.
func AvailableProfiles() []Profile {
	return []Profile{
		{
			ID:          "direct",
			Label:       "⚡ Исходный (HLS Remux)",
			Description: "Прямой поток / Remux HLS (максимальное качество, все аудиодорожки)",
			MaxHeight:   0,
			BitrateKbps: 0,
			MaxRateKbps: 0,
			BufSizeKbps: 0,
			AudioKbps:   0,
			IsDirect:    true,
		},
		{
			ID:          "http_direct",
			Label:       "🚀 Прямой HTTP (без сегментов)",
			Description: "Прямой Range-поток TorrServer напрямую в HTML5 <video> (как в TorrServer web)",
			MaxHeight:   0,
			BitrateKbps: 0,
			MaxRateKbps: 0,
			BufSizeKbps: 0,
			AudioKbps:   0,
			IsDirect:    true,
		},
		{
			ID:          "1080p_high",
			Label:       "📺 1080p HQ (10 Мбит/с)",
			Description: "Максимальная четкость для быстрых сетей и домашних кинотеатров",
			MaxHeight:   1080,
			BitrateKbps: 10000,
			MaxRateKbps: 11000,
			BufSizeKbps: 20000,
			AudioKbps:   256,
			IsDirect:    false,
		},
		{
			ID:          "1080p",
			Label:       "📱 1080p Full HD (6 Мбит/с)",
			Description: "Для скоростного Wi-Fi и больших экранов",
			MaxHeight:   1080,
			BitrateKbps: 6000,
			MaxRateKbps: 6500,
			BufSizeKbps: 12000,
			AudioKbps:   192,
			IsDirect:    false,
		},
		{
			ID:          "720p",
			Label:       "📱 720p HD (3 Мбит/с)",
			Description: "Рекомендуется для мобильного интернета (LTE/5G)",
			MaxHeight:   720,
			BitrateKbps: 2800,
			MaxRateKbps: 3200,
			BufSizeKbps: 6000,
			AudioKbps:   128,
			IsDirect:    false,
		},
		{
			ID:          "480p",
			Label:       "📶 480p SD (1.4 Мбит/с)",
			Description: "Экономия трафика при слабом сигнале",
			MaxHeight:   480,
			BitrateKbps: 1300,
			MaxRateKbps: 1600,
			BufSizeKbps: 3000,
			AudioKbps:   96,
			IsDirect:    false,
		},
		{
			ID:          "360p",
			Label:       "🔋 360p Эконом (700 Кбит/с)",
			Description: "Минимальный битрейт для роуминга или 3G",
			MaxHeight:   360,
			BitrateKbps: 600,
			MaxRateKbps: 800,
			BufSizeKbps: 1500,
			AudioKbps:   64,
			IsDirect:    false,
		},
	}
}

// GetProfile finds a profile by its ID, defaulting to "720p" if not found or empty.
func GetProfile(id string) Profile {
	if id == "" {
		id = "720p"
	}
	for _, p := range AvailableProfiles() {
		if p.ID == id {
			return p
		}
	}
	// Fallback to 720p
	for _, p := range AvailableProfiles() {
		if p.ID == "720p" {
			return p
		}
	}
	return AvailableProfiles()[0]
}

// BuildFFmpegArgs constructs command line arguments for FFmpeg transcode.
func BuildFFmpegArgs(
	sourceURL string,
	audioIdx int,
	startSeconds float64,
	startSegment int,
	profile Profile,
	outputM3U8 string,
	segmentPattern string,
) []string {
	args := []string{
		"-hide_banner",
		"-loglevel", "info",
		"-y",
		"-fflags", "+genpts+discardcorrupt",
	}

	// Fast keyframe input seek via HTTP range before -i
	if startSeconds > 0 {
		args = append(args, "-ss", fmt.Sprintf("%.3f", startSeconds))
	}

	args = append(args,
		"-i", sourceURL,
		"-map", "0:v:0",
	)

	// Audio track selection
	if audioIdx >= 0 {
		args = append(args, "-map", fmt.Sprintf("0:a:%d?", audioIdx))
	} else {
		args = append(args, "-map", "0:a:0?")
	}

	// Align PTS timestamps with startSeconds so that segment timestamps strictly match VOD timeline
	if startSeconds > 0 {
		args = append(args, "-output_ts_offset", fmt.Sprintf("%.3f", startSeconds))
	}

	// Video scaling and encoding:
	// - Closed GOP (+cgop) eliminates any cross-segment reference decoding glitches.
	// - Force keyframes at exact 3-second boundaries (75 frames at 25fps).
	// - Veryfast preset with zerolatency tuning for instant initial frame delivery.
	scaleFilter := fmt.Sprintf("scale=w=-2:h=min(%d\\,ih)", profile.MaxHeight)
	args = append(args,
		"-c:v", "libx264",
		"-preset", "veryfast",
		"-tune", "zerolatency",
		"-profile:v", "main",
		"-level", "4.1",
		"-pix_fmt", "yuv420p",
		"-flags", "+cgop",
		"-g", "75",
		"-keyint_min", "75",
		"-force_key_frames", "expr:gte(t,n_forced*3)",
		"-vf", scaleFilter,
		"-b:v", fmt.Sprintf("%dk", profile.BitrateKbps),
		"-maxrate", fmt.Sprintf("%dk", profile.MaxRateKbps),
		"-bufsize", fmt.Sprintf("%dk", profile.BufSizeKbps),
	)

	// Audio encoding: stereo downmix with AAC and sample-accurate timestamp sync (no clicks/pops)
	args = append(args,
		"-c:a", "aac",
		"-b:a", fmt.Sprintf("%dk", profile.AudioKbps),
		"-ac", "2",
		"-af", "aresample=async=1000",
	)

	// HLS muxer options
	hlsArgs := []string{
		"-f", "hls",
		"-hls_time", "3",
		"-hls_list_size", "0",
		"-hls_flags", "independent_segments+temp_file",
		"-hls_segment_type", "mpegts",
	}

	if startSegment > 0 {
		hlsArgs = append(hlsArgs, "-start_number", strconv.Itoa(startSegment))
	}

	hlsArgs = append(hlsArgs,
		"-hls_segment_filename", segmentPattern,
		outputM3U8,
	)

	return append(args, hlsArgs...)
}

// GenerateVODPlaylist constructs a full RFC 8216 compliant VOD HLS playlist.
// Providing a full VOD playlist ensures iPhone AVPlayer and desktop browsers see the entire
// movie duration, eliminate the growing timeline bug, and enable native fullscreen scrubbing.
func GenerateVODPlaylist(sessionID string, durationSec float64, startSec float64) string {
	if durationSec <= 0 {
		durationSec = 7200.0 // 2 hours default fallback
	}

	const segDuration = 3.0
	totalSegments := int(math.Ceil(durationSec / segDuration))
	if totalSegments < 1 {
		totalSegments = 1
	}

	var sb strings.Builder
	sb.WriteString("#EXTM3U\n")
	sb.WriteString("#EXT-X-VERSION:3\n")
	sb.WriteString("#EXT-X-TARGETDURATION:4\n")
	sb.WriteString("#EXT-X-MEDIA-SEQUENCE:0\n")
	sb.WriteString("#EXT-X-PLAYLIST-TYPE:VOD\n")

	if startSec > 0 && startSec < durationSec {
		sb.WriteString(fmt.Sprintf("#EXT-X-START:TIME-OFFSET=%.3f,PRECISE=YES\n", startSec))
	}

	remaining := durationSec
	for i := 0; i < totalSegments; i++ {
		dur := segDuration
		if remaining < segDuration {
			dur = remaining
		}
		if dur < 0.1 {
			dur = 0.1
		}
		sb.WriteString(fmt.Sprintf("#EXTINF:%.6f,\n", dur))
		sb.WriteString(fmt.Sprintf("/api/stream/transcode/seg/%s/seg_%04d.ts\n", sessionID, i))
		remaining -= segDuration
	}

	sb.WriteString("#EXT-X-ENDLIST\n")
	return sb.String()
}
