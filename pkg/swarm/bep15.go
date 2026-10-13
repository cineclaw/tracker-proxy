package swarm

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"
)

const (
	protocolID         = uint64(0x41727101980)
	actionConnect      = uint32(0)
	actionAnnounce     = uint32(1)
	actionScrape       = uint32(2)
	actionError        = uint32(3)
	maxHashesPerScrape = 74
)

// ScrapeStats contains seeder, completed, and leecher metrics reported by a tracker for an info_hash.
type ScrapeStats struct {
	Seeders   uint32 `json:"seeders"`
	Completed uint32 `json:"completed"`
	Leechers  uint32 `json:"leechers"`
}

// CleanTrackerAddress extracts the host:port from a UDP tracker URL.
func CleanTrackerAddress(raw string) string {
	raw = strings.TrimSpace(raw)
	if strings.Contains(raw, "://") {
		u, err := url.Parse(raw)
		if err == nil && u.Host != "" {
			return u.Host
		}
	}
	// Fallback: strip leading udp:// or trailing paths
	trimmed := strings.TrimPrefix(raw, "udp://")
	if idx := strings.Index(trimmed, "/"); idx != -1 {
		trimmed = trimmed[:idx]
	}
	return trimmed
}

// ScrapeTracker queries a single BEP 15 UDP tracker for the specified list of info_hashes.
func ScrapeTracker(ctx context.Context, trackerAddr string, hashes [][20]byte, timeout time.Duration) (map[[20]byte]ScrapeStats, error) {
	if len(hashes) == 0 {
		return make(map[[20]byte]ScrapeStats), nil
	}

	hostPort := CleanTrackerAddress(trackerAddr)
	if hostPort == "" {
		return nil, fmt.Errorf("invalid tracker address: %q", trackerAddr)
	}

	if timeout <= 0 {
		timeout = 1500 * time.Millisecond
	}

	deadline := time.Now().Add(timeout)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}

	var d net.Dialer
	conn, err := d.DialContext(ctx, "udp", hostPort)
	if err != nil {
		return nil, fmt.Errorf("udp dial to %s failed: %w", hostPort, err)
	}
	defer conn.Close()

	if err := conn.SetDeadline(deadline); err != nil {
		return nil, fmt.Errorf("failed to set deadline: %w", err)
	}

	// 1. Send Connect request
	var connectTxID uint32
	if err := binary.Read(rand.Reader, binary.BigEndian, &connectTxID); err != nil {
		return nil, fmt.Errorf("rand failed: %w", err)
	}

	connectBuf := make([]byte, 16)
	binary.BigEndian.PutUint64(connectBuf[0:8], protocolID)
	binary.BigEndian.PutUint32(connectBuf[8:12], actionConnect)
	binary.BigEndian.PutUint32(connectBuf[12:16], connectTxID)

	if _, err := conn.Write(connectBuf); err != nil {
		return nil, fmt.Errorf("connect write failed: %w", err)
	}

	respBuf := make([]byte, 2048)
	n, err := conn.Read(respBuf)
	if err != nil {
		return nil, fmt.Errorf("connect read failed: %w", err)
	}

	if n < 16 {
		return nil, fmt.Errorf("connect response too short: %d bytes", n)
	}

	resAction := binary.BigEndian.Uint32(respBuf[0:4])
	resTxID := binary.BigEndian.Uint32(respBuf[4:8])
	if resAction == actionError {
		errMsg := string(respBuf[8:n])
		return nil, fmt.Errorf("tracker returned error on connect: %s", errMsg)
	}
	if resAction != actionConnect || resTxID != connectTxID {
		return nil, fmt.Errorf("connect response mismatch: action=%d, txID=%d (expected %d)", resAction, resTxID, connectTxID)
	}

	connID := binary.BigEndian.Uint64(respBuf[8:16])

	// 2. Send Scrape requests in batches of up to maxHashesPerScrape (74)
	results := make(map[[20]byte]ScrapeStats, len(hashes))

	for start := 0; start < len(hashes); start += maxHashesPerScrape {
		end := start + maxHashesPerScrape
		if end > len(hashes) {
			end = len(hashes)
		}
		chunk := hashes[start:end]

		var scrapeTxID uint32
		if err := binary.Read(rand.Reader, binary.BigEndian, &scrapeTxID); err != nil {
			return nil, fmt.Errorf("rand failed: %w", err)
		}

		scrapeBuf := new(bytes.Buffer)
		scrapeBuf.Grow(16 + len(chunk)*20)
		_ = binary.Write(scrapeBuf, binary.BigEndian, connID)
		_ = binary.Write(scrapeBuf, binary.BigEndian, actionScrape)
		_ = binary.Write(scrapeBuf, binary.BigEndian, scrapeTxID)
		for _, h := range chunk {
			scrapeBuf.Write(h[:])
		}

		if _, err := conn.Write(scrapeBuf.Bytes()); err != nil {
			return nil, fmt.Errorf("scrape write failed: %w", err)
		}

		n, err = conn.Read(respBuf)
		if err != nil {
			return nil, fmt.Errorf("scrape read failed: %w", err)
		}

		if n < 8 {
			return nil, fmt.Errorf("scrape response too short: %d bytes", n)
		}

		resAction = binary.BigEndian.Uint32(respBuf[0:4])
		resTxID = binary.BigEndian.Uint32(respBuf[4:8])
		if resAction == actionError {
			errMsg := string(respBuf[8:n])
			return nil, fmt.Errorf("tracker returned error on scrape: %s", errMsg)
		}
		if resAction != actionScrape || resTxID != scrapeTxID {
			return nil, fmt.Errorf("scrape response mismatch: action=%d, txID=%d", resAction, resTxID)
		}

		// Read 12-byte tuples: [seeders (4), completed (4), leechers (4)]
		offset := 8
		for _, h := range chunk {
			if offset+12 > n {
				break
			}
			seeds := binary.BigEndian.Uint32(respBuf[offset : offset+4])
			completed := binary.BigEndian.Uint32(respBuf[offset+4 : offset+8])
			leeches := binary.BigEndian.Uint32(respBuf[offset+8 : offset+12])
			results[h] = ScrapeStats{
				Seeders:   seeds,
				Completed: completed,
				Leechers:  leeches,
			}
			offset += 12
		}
	}

	return results, nil
}
