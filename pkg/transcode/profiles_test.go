package transcode

import (
	"strings"
	"testing"
)

func TestAvailableProfiles(t *testing.T) {
	profiles := AvailableProfiles()
	if len(profiles) != 7 {
		t.Fatalf("expected 7 profiles, got %d", len(profiles))
	}

	direct := profiles[0]
	if !direct.IsDirect || direct.ID != "direct" {
		t.Errorf("expected first profile to be direct, got %+v", direct)
	}

	httpDirect := profiles[1]
	if !httpDirect.IsDirect || httpDirect.ID != "http_direct" {
		t.Errorf("expected second profile to be http_direct, got %+v", httpDirect)
	}

	p720 := GetProfile("720p")
	if p720.MaxHeight != 720 || p720.BitrateKbps != 5000 {
		t.Errorf("unexpected 720p profile: %+v", p720)
	}

	fallback := GetProfile("unknown_profile")
	if fallback.ID != "720p" {
		t.Errorf("expected fallback to 720p, got %s", fallback.ID)
	}
}

func TestBuildFFmpegArgs(t *testing.T) {
	profile := GetProfile("720p")
	args := BuildFFmpegArgs(
		"http://localhost:8092/torr/stream/video.mkv?link=abc&index=1&play",
		2,
		150.5,
		50,
		profile,
		"/tmp/out/index.m3u8",
		"/tmp/out/seg_%04d.ts",
	)

	cmdStr := strings.Join(args, " ")
	if !strings.Contains(cmdStr, "-ss 150.500") {
		t.Errorf("missing seek arg: %s", cmdStr)
	}
	if !strings.Contains(cmdStr, "-output_ts_offset 150.500") {
		t.Errorf("missing output_ts_offset: %s", cmdStr)
	}
	if !strings.Contains(cmdStr, "-flags +cgop") {
		t.Errorf("missing +cgop: %s", cmdStr)
	}
	if !strings.Contains(cmdStr, "-start_number 50") {
		t.Errorf("missing start_number: %s", cmdStr)
	}
	if !strings.Contains(cmdStr, "-map 0:a:2?") {
		t.Errorf("missing audio map: %s", cmdStr)
	}
	if !strings.Contains(cmdStr, "scale=w=-2:h=min(720\\,ih)") {
		t.Errorf("missing scale filter: %s", cmdStr)
	}
	if !strings.Contains(cmdStr, "-b:v 5000k") {
		t.Errorf("missing bitrate: %s", cmdStr)
	}
	if !strings.Contains(cmdStr, "-profile:v high") {
		t.Errorf("missing high profile: %s", cmdStr)
	}
	if !strings.Contains(cmdStr, "-tune film") {
		t.Errorf("missing film tuning: %s", cmdStr)
	}
	if !strings.Contains(cmdStr, "-f hls") {
		t.Errorf("missing hls format: %s", cmdStr)
	}
}

func TestBuildFFmpegArgsDirectRemux(t *testing.T) {
	profile := GetProfile("direct")
	args := BuildFFmpegArgs(
		"http://localhost:8092/torr/stream/video.mkv?link=abc&index=1&play",
		0,
		0,
		0,
		profile,
		"/tmp/out/index.m3u8",
		"/tmp/out/seg_%04d.ts",
	)

	cmdStr := strings.Join(args, " ")
	if !strings.Contains(cmdStr, "-c:v copy") {
		t.Errorf("expected -c:v copy for direct remux profile, got: %s", cmdStr)
	}
	if strings.Contains(cmdStr, "libx264") || strings.Contains(cmdStr, "scale=") {
		t.Errorf("direct remux profile must not re-encode video: %s", cmdStr)
	}
	if !strings.Contains(cmdStr, "-c:a aac") {
		t.Errorf("expected -c:a aac, got: %s", cmdStr)
	}
	if !strings.Contains(cmdStr, "-b:a 256k") {
		t.Errorf("expected -b:a 256k, got: %s", cmdStr)
	}
}

func TestGenerateVODPlaylist(t *testing.T) {
	pl := GenerateVODPlaylist("test_sess", 10.0, 3.0)
	if !strings.Contains(pl, "#EXT-X-PLAYLIST-TYPE:VOD") {
		t.Errorf("missing VOD tag: %s", pl)
	}
	if !strings.Contains(pl, "#EXT-X-START:TIME-OFFSET=3.000,PRECISE=YES") {
		t.Errorf("missing START tag: %s", pl)
	}
	if !strings.Contains(pl, "#EXT-X-ENDLIST") {
		t.Errorf("missing ENDLIST tag: %s", pl)
	}
	if !strings.Contains(pl, "/api/stream/transcode/seg/test_sess/seg_0000.ts") {
		t.Errorf("missing seg_0000.ts: %s", pl)
	}
	if !strings.Contains(pl, "/api/stream/transcode/seg/test_sess/seg_0003.ts") {
		t.Errorf("missing seg_0003.ts: %s", pl)
	}
}
