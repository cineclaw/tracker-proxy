package transcode

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

var (
	ErrSessionClosed = errors.New("transcode session is closed")
	reSegmentFile    = regexp.MustCompile(`seg_(\d+)\.ts`)
)

// TranscodeSession manages an active FFmpeg transcode pipeline with seekable on-demand segments.
type TranscodeSession struct {
	ID          string
	Hash        string
	FileIdx     int
	Profile     Profile
	AudioIdx    int
	DurationSec float64
	OutputDir   string
	SourceURL   string

	mu                     sync.Mutex
	cmd                    *exec.Cmd
	ctx                    context.Context
	cancel                 context.CancelFunc
	activeStartSegment     int
	highestSegmentProduced int
	lastSegmentRequested   int
	isPaused               bool
	closed                 bool
	ready                  bool
	startErr               error
	lastAccess             time.Time
	createdAt              time.Time
}

// NewSession creates and initializes a transcode session.
func NewSession(
	id string,
	hash string,
	fileIdx int,
	profile Profile,
	audioIdx int,
	startSec float64,
	durationSec float64,
	sourceURL string,
	baseOutputDir string,
) (*TranscodeSession, error) {
	if baseOutputDir == "" {
		baseOutputDir = filepath.Join(os.TempDir(), "cineclaw_transcode")
	}
	outDir := filepath.Join(baseOutputDir, id)
	if err := os.MkdirAll(outDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create transcode dir: %w", err)
	}

	startSegment := int(startSec / 3.0)
	if startSegment > 0 {
		startSegment-- // Start 1 segment earlier so preceding boundary/keyframe segment is ready
	}
	workerStartSec := float64(startSegment) * 3.0

	sess := &TranscodeSession{
		ID:                     id,
		Hash:                   hash,
		FileIdx:                fileIdx,
		Profile:                profile,
		AudioIdx:               audioIdx,
		DurationSec:            durationSec,
		OutputDir:              outDir,
		SourceURL:              sourceURL,
		activeStartSegment:     startSegment,
		highestSegmentProduced: startSegment - 1,
		lastSegmentRequested:   startSegment,
		lastAccess:             time.Now(),
		createdAt:              time.Now(),
	}

	if err := sess.startWorkerLocked(startSegment, workerStartSec); err != nil {
		_ = os.RemoveAll(outDir)
		return nil, err
	}

	return sess, nil
}

// startWorkerLocked launches a child FFmpeg process starting at startSegment and startSec.
// s.mu must NOT be held by the caller if it's called externally, or s.mu must be held if called internally.
func (s *TranscodeSession) startWorkerLocked(startSegment int, startSec float64) error {
	if s.cancel != nil {
		if s.isPaused && runtime.GOOS != "windows" && s.cmd != nil && s.cmd.Process != nil {
			_ = s.cmd.Process.Signal(syscall.SIGCONT)
		}
		s.cancel()
		if s.cmd != nil && s.cmd.Process != nil {
			_ = s.cmd.Process.Kill()
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	outputM3U8 := filepath.Join(s.OutputDir, "index.m3u8")
	segmentPattern := filepath.Join(s.OutputDir, "seg_%04d.ts")

	args := BuildFFmpegArgs(s.SourceURL, s.AudioIdx, startSec, startSegment, s.Profile, outputM3U8, segmentPattern)
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)

	stderr, err := cmd.StderrPipe()
	if err != nil {
		cancel()
		return fmt.Errorf("failed to open stderr: %w", err)
	}

	if err := cmd.Start(); err != nil {
		cancel()
		return fmt.Errorf("failed to start ffmpeg: %w", err)
	}

	s.cmd = cmd
	s.ctx = ctx
	s.cancel = cancel
	s.isPaused = false
	s.activeStartSegment = startSegment
	if startSegment > s.highestSegmentProduced {
		s.highestSegmentProduced = startSegment - 1
	}

	log.Printf("[Transcode] Started worker session=%s (pid=%d, seg=%d, start=%.1fs, profile=%s)",
		s.ID, cmd.Process.Pid, startSegment, startSec, s.Profile.ID)

	go s.monitorStderr(stderr, cmd)
	return nil
}

// Start waits until the initial segment is produced.
func (s *TranscodeSession) Start(timeout time.Duration) error {
	segFile := fmt.Sprintf("seg_%04d.ts", s.activeStartSegment)
	segPath := filepath.Join(s.OutputDir, segFile)
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		s.mu.Lock()
		closed := s.closed
		startErr := s.startErr
		s.mu.Unlock()

		if closed || startErr != nil {
			if startErr != nil {
				return startErr
			}
			return ErrSessionClosed
		}

		if fi, err := os.Stat(segPath); err == nil && fi.Size() > 0 {
			s.mu.Lock()
			s.ready = true
			s.lastAccess = time.Now()
			s.mu.Unlock()
			return nil
		}

		time.Sleep(50 * time.Millisecond)
	}

	s.Stop()
	return fmt.Errorf("timeout waiting for initial transcode segment (%v)", timeout)
}

// monitorStderr reads FFmpeg stderr, detects new segments, and applies CPU throttling.
func (s *TranscodeSession) monitorStderr(r interface{ Read([]byte) (int, error) }, cmd *exec.Cmd) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Text()

		// Detect segment creation: e.g. "Opening '...seg_0003.ts' for writing"
		if matches := reSegmentFile.FindStringSubmatch(line); len(matches) > 1 {
			if num, err := strconv.Atoi(matches[1]); err == nil {
				s.mu.Lock()
				if num > s.highestSegmentProduced {
					s.highestSegmentProduced = num
				}
				lead := s.highestSegmentProduced - s.lastSegmentRequested
				// If FFmpeg is > 35 segments (~105s) ahead of player, pause it
				if lead > 35 && !s.isPaused && !s.closed {
					s.pauseProcess()
				}
				s.mu.Unlock()
			}
		}
	}

	// When stderr finishes, wait for process exit
	err := cmd.Wait()
	s.mu.Lock()
	if !s.closed && err != nil && !strings.Contains(err.Error(), "killed") {
		log.Printf("[Transcode] FFmpeg worker session %s exited: %v", s.ID, err)
		s.startErr = err
	}
	s.mu.Unlock()
}

// Touch updates last access timestamp and resumes transcoding if buffer was low.
func (s *TranscodeSession) Touch(segNum int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.lastAccess = time.Now()
	if segNum > s.lastSegmentRequested {
		s.lastSegmentRequested = segNum
	}

	// If player is catching up (< 15 segments buffer ahead), resume FFmpeg
	lead := s.highestSegmentProduced - s.lastSegmentRequested
	if lead <= 15 && s.isPaused && !s.closed {
		s.resumeProcess()
	}
}

// GetSegment reads a requested .ts segment file. If the requested segment is outside
// the active encoding window (a seek), it restarts the FFmpeg worker at that segment.
func (s *TranscodeSession) GetSegment(filename string) ([]byte, error) {
	filename = filepath.Base(filename)
	if !strings.HasSuffix(filename, ".ts") {
		return nil, fmt.Errorf("invalid segment extension: %s", filename)
	}

	segNum := 0
	if matches := reSegmentFile.FindStringSubmatch(filename); len(matches) > 1 {
		segNum, _ = strconv.Atoi(matches[1])
	}

	s.Touch(segNum)

	segPath := filepath.Join(s.OutputDir, filename)

	// Fast path: if segment already exists on disk and is non-empty, serve it immediately
	if data, err := os.ReadFile(segPath); err == nil && len(data) > 0 {
		return data, nil
	}

	// Not on disk: check if this is a seek outside the active encoding window
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, ErrSessionClosed
	}

	activeStart := s.activeStartSegment
	highest := s.highestSegmentProduced

	// A seek is either:
	// 1. Backward seek: requested segment is before activeStart (and not cached)
	// 2. Forward jump: requested segment is beyond the active encoding horizon
	maxForward := highest + 8
	if activeStart+20 > maxForward {
		maxForward = activeStart + 20
	}

	// Guard against demuxer probing or boundary requests: If a demuxer probes segment 0 or 1,
	// or requests the boundary segment (activeStart - 1) at session startup,
	// do NOT abort the active worker that is transcoding at activeStart.
	isDemuxerProbe := (segNum <= 1 && activeStart > 2 && time.Since(s.createdAt) < 25*time.Second)
	isBoundaryProbe := (segNum == activeStart-1 && time.Since(s.createdAt) < 25*time.Second)

	if !isDemuxerProbe && !isBoundaryProbe && (segNum < activeStart-1 || segNum > maxForward) {
		seekStartSec := float64(segNum) * 3.0
		log.Printf("[Transcode] Seek detected in session %s: requested seg %d (active=[%d..%d], horizon=%d), seeking to %.1fs",
			s.ID, segNum, activeStart, highest, maxForward, seekStartSec)
		if err := s.startWorkerLocked(segNum, seekStartSec); err != nil {
			s.mu.Unlock()
			return nil, fmt.Errorf("failed to seek transcode worker: %w", err)
		}
	}
	s.mu.Unlock()

	targetWaitFile := segPath
	if isDemuxerProbe || isBoundaryProbe {
		// Demuxer only needs any valid TS segment from this stream to probe codecs/dimensions/PID or boundary.
		// Wait for the activeStart segment that the worker is actively producing if boundary is not ready.
		if _, err := os.Stat(segPath); err != nil {
			targetWaitFile = filepath.Join(s.OutputDir, fmt.Sprintf("seg_%04d.ts", activeStart))
		}
	}

	// Wait up to 45 seconds for segment to appear on disk (cold BitTorrent seek may take ~20-30s)
	deadline := time.Now().Add(45 * time.Second)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		closed := s.closed
		startErr := s.startErr
		s.mu.Unlock()

		if closed {
			return nil, ErrSessionClosed
		}
		if startErr != nil {
			return nil, startErr
		}

		if data, err := os.ReadFile(targetWaitFile); err == nil && len(data) > 0 {
			return data, nil
		}
		time.Sleep(40 * time.Millisecond)
	}

	return nil, fmt.Errorf("timeout waiting for segment: %s", filename)
}

// IsIdle checks if the session hasn't been accessed within the duration.
func (s *TranscodeSession) IsIdle(timeout time.Duration) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return time.Since(s.lastAccess) > timeout
}

// pauseProcess sends SIGSTOP on Unix systems to pause encoding.
func (s *TranscodeSession) pauseProcess() {
	if runtime.GOOS == "windows" || s.cmd == nil || s.cmd.Process == nil {
		return
	}
	log.Printf("[Transcode] Throttling FFmpeg session %s (buffer lead=%d)",
		s.ID, s.highestSegmentProduced-s.lastSegmentRequested)
	_ = s.cmd.Process.Signal(syscall.SIGSTOP)
	s.isPaused = true
}

// resumeProcess sends SIGCONT on Unix systems to resume encoding.
func (s *TranscodeSession) resumeProcess() {
	if runtime.GOOS == "windows" || s.cmd == nil || s.cmd.Process == nil {
		return
	}
	log.Printf("[Transcode] Resuming FFmpeg session %s (buffer lead=%d)",
		s.ID, s.highestSegmentProduced-s.lastSegmentRequested)
	_ = s.cmd.Process.Signal(syscall.SIGCONT)
	s.isPaused = false
}

// Stop terminates the FFmpeg process and deletes the session's temp directory.
func (s *TranscodeSession) Stop() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	s.mu.Unlock()

	// If paused, unpause first so it can process signal
	if s.isPaused && runtime.GOOS != "windows" && s.cmd != nil && s.cmd.Process != nil {
		_ = s.cmd.Process.Signal(syscall.SIGCONT)
	}

	if s.cancel != nil {
		s.cancel()
	}

	if s.cmd != nil && s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
	}

	log.Printf("[Transcode] Cleaned up session %s", s.ID)

	// Clean up temporary output directory
	_ = os.RemoveAll(s.OutputDir)
}
