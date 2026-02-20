package omnipulse

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"runtime/pprof"
	"strconv"
	"time"
)

type Profiler struct {
	client       *Client
	profilingCtx context.Context
	cancel       context.CancelFunc
	isRunning    bool
}

func newProfiler(c *Client) *Profiler {
	return &Profiler{
		client: c,
	}
}

// Start begins continuous CPU profiling, sending dumps every 60 seconds.
func (p *Profiler) Start() error {
	if p.isRunning {
		return fmt.Errorf("profiler is already running")
	}

	ctx, cancel := context.WithCancel(p.client.ctx)
	p.profilingCtx = ctx
	p.cancel = cancel
	p.isRunning = true

	go p.worker(60 * time.Second)
	return nil
}

// Stop ends continuous profiling.
func (p *Profiler) Stop() {
	if !p.isRunning {
		return
	}
	p.cancel()
	p.isRunning = false
}

func (p *Profiler) worker(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	var buf bytes.Buffer
	for {
		// Attempt to start CPU profile
		buf.Reset()
		if err := pprof.StartCPUProfile(&buf); err != nil {
			if p.client.config.Debug {
				fmt.Printf("[omnipulse profiler] failed to start CPU profile: %v\n", err)
			}
		}

		select {
		case <-ticker.C:
			pprof.StopCPUProfile()
			p.sendProfile(buf.Bytes(), "cpu", int(interval.Seconds()))
		case <-p.profilingCtx.Done():
			pprof.StopCPUProfile()
			p.sendProfile(buf.Bytes(), "cpu", int(interval.Seconds()))
			return
		}
	}
}

func (p *Profiler) sendProfile(data []byte, profileType string, durationSecs int) {
	if len(data) == 0 {
		return
	}

	payloadBody := &bytes.Buffer{}
	writer := multipart.NewWriter(payloadBody)

	// Create physical file attachment
	part, err := writer.CreateFormFile("profile", fmt.Sprintf("profile_%s_%d.pb.gz", profileType, time.Now().Unix()))
	if err == nil {
		_, _ = io.Copy(part, bytes.NewReader(data))
	}

	// Attach Metadata
	_ = writer.WriteField("env", p.client.config.Environment)
	_ = writer.WriteField("profile_type", profileType)
	_ = writer.WriteField("duration_seconds", strconv.Itoa(durationSecs))

	hostname, _ := os.Hostname()
	if hostname == "" {
		hostname = "unknown"
	}
	_ = writer.WriteField("instance_hash", hostname)

	err = writer.Close()
	if err != nil {
		if p.client.config.Debug {
			fmt.Printf("[omnipulse profiler] failed to close multipart writer: %v\n", err)
		}
		return
	}

	req, err := http.NewRequestWithContext(p.client.ctx, "POST", p.client.config.APIUrl+"/api/ingest/app-profiles", payloadBody)
	if err != nil {
		if p.client.config.Debug {
			fmt.Printf("[omnipulse profiler] failed to construct request: %v\n", err)
		}
		return
	}

	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("X-Ingest-Key", p.client.config.IngestKey)
	req.Header.Set("User-Agent", fmt.Sprintf("omnipulse-go-sdk/%s", Version))

	resp, err := p.client.httpClient.Do(req)
	if err != nil {
		if p.client.config.Debug {
			fmt.Printf("[omnipulse profiler] HTTP post failed: %v\n", err)
		}
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 && p.client.config.Debug {
		fmt.Printf("[omnipulse profiler] backend returned status %d\n", resp.StatusCode)
	}
}
