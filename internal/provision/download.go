// Package provision installs toolchains via package managers and direct URLs.
// This file adds a progress bar for zedrun's own HTTP downloads.
package provision

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// progressFPS bounds how often a ProgressBar redraws. A slow download therefore
// renders a line roughly this often instead of on every chunk, and a fast one
// only shows the final state except for Finish.
const progressFPS = 100 * time.Millisecond

const progressWidth = 20

// ProgressBar renders a compact download progress bar to dst. Feed it the
// downloaded stream with Write; it re-renders only when enough time passes, so
// a fast stream shows the bar just once. Finish prints the final state on its
// own line, ready for the next prompt. It is the reader-facing end of an http
// response body and should be the only part of a download that touches the
// terminal.
type ProgressBar struct {
	dst     io.Writer
	total   int64
	got     int64
	mu      sync.Mutex
	last    time.Time
	printed bool
}

// NewProgressBar returns a ProgressBar writing to dst. total is the content
// length; 0 leaves the percentage and fraction hidden until it is known.
func NewProgressBar(dst io.Writer, total int64) *ProgressBar {
	return &ProgressBar{dst: dst, total: total, last: time.Now()}
}

// Write feeds the downloaded stream. It returns the number of bytes written and
// any error from dst.
func (p *ProgressBar) Write(b []byte) (int, error) {
	n := len(b)
	p.mu.Lock()
	p.got += int64(n)
	if p.got > p.total && p.total > 0 {
		p.got = p.total
	}
	if time.Since(p.last) >= progressFPS || p.got >= p.total {
		p.mu.Unlock()
		p.render()
		p.mu.Lock()
		p.last = time.Now()
	}
	p.mu.Unlock()
	return n, nil
}

// Finish renders the final state and moves the cursor to the next line. If the
// download finished with a known size it reports 100%; otherwise it reports
// what was read so far.
func (p *ProgressBar) Finish() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.render()
	if !p.printed {
		fmt.Fprintln(p.dst)
		p.printed = true
	}
}

func (p *ProgressBar) render() {
	if p.total > 0 {
		pct := int64(p.got * 100 / p.total)
		fmt.Fprintf(p.dst, "\r[%s] %3d%%  %s / %s", p.bar(), pct, human(p.got), human(p.total))
	} else {
		fmt.Fprintf(p.dst, "\r[…]  %s", human(p.got))
	}
}

// bar renders the 20-cell block fill, widest first for a quick visual.
func (p *ProgressBar) bar() string {
	if p.total <= 0 {
		return strings.Repeat("?", progressWidth)
	}
	n := int64(progressWidth * p.got / p.total)
	if n > progressWidth {
		n = progressWidth
	}
	return strings.Repeat(progressBlock, int(n)) + strings.Repeat(progressEmpty, progressWidth-int(n))
}

const (
	progressBlock   = "█"
	progressEmpty   = "░"
	progressUnknown = "?"
)

// human renders a byte count in a human scale: "12 B", "4.5 KB", "1.2 GB".
func human(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	exp := 0
	for n >= unit && exp < 5 {
		n /= unit
		exp++
	}
	suffixes := []string{"KB", "MB", "GB", "TB", "PB"}
	s := fmt.Sprintf("%.1f%s", float64(n), suffixes[exp-1])
	if strings.HasSuffix(s, ".0") {
		s = s[:len(s)-2] + suffixes[exp-1]
	}
	return s
}

// Download fetches url into dest, streaming with a progress bar rendered to
// dst (typically the terminal). It returns the number of bytes written. On
// success the file at dest is a complete download, and dst ends on a fresh
// line.
//
// ctx cancels the request; a nil client uses a 15-minute default. A short
// download of a known length shows a 100% bar; a long one updates the bar with
// progressFPS spacing so a slow network is never spammed. dest is Mkdir-ed on
// demand and left in place on failure.
func Download(ctx context.Context, client *http.Client, url, dest string, dst io.Writer) (int64, error) {
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Minute}
	}
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	req = req.WithContext(ctx)
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("download %s: HTTP %s", url, resp.Status)
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return 0, err
	}
	f, err := os.Create(dest)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	pb := NewProgressBar(dst, resp.ContentLength)
	n, err := io.Copy(f, io.TeeReader(resp.Body, pb))
	if err != nil {
		return 0, err
	}
	if resp.ContentLength > 0 && n != resp.ContentLength {
		return n, fmt.Errorf("download %s: short read %d of %d", url, n, resp.ContentLength)
	}
	pb.Finish()
	return n, nil
}
