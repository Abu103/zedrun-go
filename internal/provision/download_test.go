// Tests for the HTTP progress bar and its integration with Download.
package provision

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestProgressBarRendersPercent is deterministic: no real network, no sleeping.
func TestProgressBarRendersPercent(t *testing.T) {
	var buf bytes.Buffer
	pb := NewProgressBar(&buf, 100)
	if _, err := pb.Write(make([]byte, 25)); err != nil {
		t.Fatalf("Write: %v", err)
	}
	pb.Finish()
	out := buf.String()
	if !strings.Contains(out, "25%") {
		t.Fatalf("expected a 25%% render, got %q", out)
	}
	if !strings.Contains(out, "25 B") {
		t.Fatalf("expected a byte count, got %q", out)
	}
	if !strings.Contains(out, "\n") {
		t.Fatalf("Finish must end the bar on its own line: %q", out)
	}
}

// TestProgressBarUnknownLength hides the fraction until the header reports a
// size.
func TestProgressBarUnknownLength(t *testing.T) {
	var buf bytes.Buffer
	pb := NewProgressBar(&buf, 0)
	pb.Write(make([]byte, 512))
	pb.Finish()
	if !strings.Contains(buf.String(), "512 B") {
		t.Fatalf("expected a byte count, got %q", buf.String())
	}
}

// TestDownload is an end-to-end check: a local server serves a known body and
// Download streams it to a file while the progress bar renders. No sleep: the
// body is small, so the bar reaches 100% on Finish and the written bytes match
// the served content exactly.
func TestDownload(t *testing.T) {
	body := make([]byte, 1<<20) // 1 MiB
	for i := range body {
		body[i] = byte(i%251 + 1)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(body)))
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write(body); err != nil {
			t.Fatalf("server Write: %v", err)
		}
	}))
	defer server.Close()

	var bar bytes.Buffer
	dir := t.TempDir()
	dest := filepath.Join(dir, "downloaded.bin")
	n, err := Download(context.Background(), nil, server.URL, dest, &bar)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if n != int64(len(body)) {
		t.Fatalf("Download wrote %d, want %d", n, len(body))
	}
	data, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read dest: %v", err)
	}
	if len(data) != len(body) {
		t.Fatalf("dest size %d, want %d", len(data), len(body))
	}
	if !bytes.Equal(data, body) {
		t.Fatalf("dest content differs from served body")
	}
	if !strings.Contains(bar.String(), "100%") {
		t.Fatalf("progress bar did not reach 100%%: %q", bar.String())
	}
}

// TestDownloadShortTransportError makes the server reply 500, and Download
// must fail without writing a truncated file.
func TestDownloadShortTransportError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusMethodNotAllowed)
	}))
	defer server.Close()

	var bar bytes.Buffer
	dir := t.TempDir()
	_, err := Download(context.Background(), nil, server.URL, filepath.Join(dir, "nope"), &bar)
	if err == nil {
		t.Fatalf("Download on a 405 should fail")
	}
	if _, err := os.Stat(filepath.Join(dir, "nope")); !os.IsNotExist(err) {
		// lstat on the dest path is allowed only when the file does not exist.
		if _, statErr := os.Stat(filepath.Join(dir, "nope")); statErr == nil {
			t.Fatalf("download left a file on an error")
		}
	}
}
