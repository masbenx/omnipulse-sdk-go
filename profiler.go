package omnipulse

import (
	"bytes"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"runtime/pprof"
	"strconv"
	"time"
)

func (c *Client) startProfiler() {
	defer c.wg.Done()
	
	// Collect profile every 60 seconds
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()

	hostname, _ := os.Hostname()
	instanceHash := fmt.Sprintf("%s-%d", hostname, os.Getpid())

	var buf bytes.Buffer

	// Start initial profile
	err := pprof.StartCPUProfile(&buf)
	if err != nil {
		if c.config.Debug {
			fmt.Printf("[omnipulse] failed to start CPU profiler: %v\n", err)
		}
		return // Cannot profile
	}

	for {
		select {
		case <-ticker.C:
			pprof.StopCPUProfile()

			// Send collected profile buffer
			profileData := make([]byte, buf.Len())
			copy(profileData, buf.Bytes())
			
			go c.sendProfile(profileData, instanceHash, "cpu", 60)

			// Reset buffer and restart
			buf.Reset()
			err := pprof.StartCPUProfile(&buf)
			if err != nil {
				if c.config.Debug {
					fmt.Printf("[omnipulse] failed to restart CPU profiler: %v\n", err)
				}
				return
			}

		case <-c.ctx.Done():
			pprof.StopCPUProfile()
			return
		}
	}
}

func (c *Client) sendProfile(data []byte, instanceHash, profileType string, durationSecs int) {
	if len(data) == 0 {
		return
	}

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	part, err := writer.CreateFormFile("profile", "cpu.pb.gz")
	if err == nil {
		part.Write(data)
	}

	writer.WriteField("instance_hash", instanceHash)
	writer.WriteField("env", c.config.Environment)
	writer.WriteField("profile_type", profileType)
	writer.WriteField("duration_seconds", strconv.Itoa(durationSecs))
	writer.Close()

	req, err := http.NewRequestWithContext(c.ctx, "POST", c.config.APIUrl+"/api/ingest/app-profiles", body)
	if err != nil {
		return
	}

	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("X-Ingest-Key", c.config.IngestKey)
	req.Header.Set("User-Agent", fmt.Sprintf("omnipulse-go-sdk/%s", Version))

	resp, err := c.httpClient.Do(req)
	if err != nil {
		if c.config.Debug {
			fmt.Printf("[omnipulse] failed to send profile: %v\n", err)
		}
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		if c.config.Debug {
			b, _ := io.ReadAll(resp.Body)
			fmt.Printf("[omnipulse] backend rejected profile (status %d): %s\n", resp.StatusCode, string(b))
		}
	}
}
