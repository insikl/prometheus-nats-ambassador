// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
package main

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/insikl/prometheus-nats-ambassador/internal/logger"
	"github.com/prometheus/client_golang/prometheus"
)

// Read data for remote writer
func (pubsub *ProxyConn) RemoteWriteHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Only POST method is supported", http.StatusMethodNotAllowed)
		return
	}

	// Validate headers
	// https://prometheus.io/docs/specs/prw/remote_write_spec/#protocol
	// Prometheus Remote Write 1.0 spec
	// The following headers MUST be sent with the HTTP request:
	//
	// Content-Encoding: snappy
	// Content-Type: application/x-protobuf
	// User-Agent: <name & version of the sender>
	// X-Prometheus-Remote-Write-Version: 0.1.0
	enc := strings.ToLower(r.Header.Get("Content-Encoding"))
	if enc != "snappy" && enc != "zstd" {
		logger.Warn("Unexpected Content-Encoding: %s", r.Header.Get("Content-Encoding"))
		http.Error(w, "Content-Encoding must be snappy or zstd", http.StatusBadRequest)
		return
	}

	if r.Header.Get("Content-Type") != "application/x-protobuf" {
		logger.Warn("Unexpected Content-Type: %s", r.Header.Get("Content-Type"))
		http.Error(w, "Content-Type must be application/x-protobuf", http.StatusBadRequest)
		return
	}
	if r.Header.Get("X-Prometheus-Remote-Write-Version") != "0.1.0" {
		logger.Warn("Unexpected X-Prometheus-Remote-Write-Version: %s", r.Header.Get("X-Prometheus-Remote-Write-Version"))
		http.Error(w, "X-Prometheus-Remote-Write-Version must be 0.1.0", http.StatusBadRequest)
		return
	}

	// Read the raw binary body directly
	compressedData, err := io.ReadAll(r.Body)
	if err != nil {
		logger.Error("Error reading request body: %v", err)
		http.Error(w, "Failed to read request body", http.StatusInternalServerError)
		return
	}

	if len(compressedData) == 0 {
		logger.Error("Received empty request body.")
		http.Error(w, "Empty request body", http.StatusBadRequest)
		return
	}

	if showDebug {
		logger.Debug(
			"Received Prometheus remote write request (size: %d bytes). Publishing to NATS topic '%s'...",
			len(compressedData),
			topicBase,
		)
	}

	// Build subject
	subj := topicBase + ".encoding." + enc

	// Publish the raw compressed data to NATS
	err = pubsub.nc.Publish(subj, compressedData)
	if err != nil {
		logger.Error("Error publishing to NATS: %v", err)
		http.Error(w, "Failed to publish data to NATS", http.StatusInternalServerError)
		return
	}

	// Acknowledge receipt to Prometheus
	// 204 No Content is a common success response for remote write
	w.WriteHeader(http.StatusNoContent)
	if showDebug {
		logger.Debug("Successfully published data to NATS and acknowledged to Prometheus.")
	}
}

// relayPrometheusRemoteWrite forwards the raw compressed data to the remote write endpoint.
// https://prometheus.io/docs/specs/prw/remote_write_spec/#protocol
// Prometheus Remote Write 1.0 spec
// The following headers MUST be sent with the HTTP request:
//
// Content-Encoding: snappy
// Content-Type: application/x-protobuf
// User-Agent: <name & version of the sender>
// X-Prometheus-Remote-Write-Version: 0.1.0
// func relayPrometheusRemoteWrite(compressedData []byte, w http.ResponseWriter, r *http.Request) error {
func RelayPrometheusRemoteWrite(
	topic string,
	remoteWriteURL string,
	compressedData []byte,
) (string, error) {
	// Create a new HTTP POST request
	req, err := http.NewRequest(
		http.MethodPost,
		remoteWriteURL,
		bytes.NewBuffer(compressedData),
	)
	if err != nil {
		return "", fmt.Errorf("failed to create HTTP request: %w", err)
	}

	// NOTE: decode topic to determine the content encoding of the message
	// Example: io.prometheus.exporter.remote.<site>.encoding.snappy
	// Example: io.prometheus.exporter.remote.<site>.encoding.zstd
	parts := strings.Split(topic, ".")

	// NOTE: Require at least 2 parts in NATS subject or exit with an error
	//       this assumes that any published subject will have at least 2 parts
	//       even if it doesn't follow the above `*.encoding.<type>`.
	if len(parts) < 2 {
		return "", fmt.Errorf("Topic source '%s' does not have enough parts to check.", topic)
	}

	// Access the last two elements of the slice.
	encKey := parts[len(parts)-2]
	encVal := parts[len(parts)-1]

	// Compare the last second to last part.
	if encKey == "encoding" {
		logger.Debug("Found encoding '%s' with '%s'", encKey, encVal)
	} else {
		// Fallback default mode of assuming content encoding
		logger.Info("Unknown topic '%s', falling back to '*.encoding.snappy'", topic)
		encKey = "encoding"
		encVal = "snappy"
	}

	switch encVal {
	case "snappy":
		logger.Debug("Content encoding '%s' used by Prometheus.", encVal)
	case "zstd":
		logger.Debug("Content encoding '%s' used by VictoraMetrics.", encVal)
	default:
		// NOTE: This is for older versions of prometheus-nats-ambassador to
		//       still work with each other below version 0.2.0.
		logger.Info("Content encoding '%s' unknown, setting 'snappy' as default", encVal)
		encVal = "snappy"
	}

	// NOTE: will deprecate defaulting content-encoding and error out in future
	if encVal != "snappy" {
		return "", fmt.Errorf("Unknown content encoding type '%s'", encVal)
	}

	// Set the required Prometheus remote write headers from the input collected
	// Prometheus Remote Write 1.0 spec
	// https://prometheus.io/docs/specs/prw/remote_write_spec/#protocol
	req.Header.Set("Content-Type", "application/x-protobuf")
	req.Header.Set("Content-Encoding", encVal)
	req.Header.Set("X-Prometheus-Remote-Write-Version", "0.1.0")
	req.Header.Set("User-Agent", userAgent)

	// Send the request
	client := http.Client{
		Timeout: time.Duration(60) * time.Second,
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to send HTTP request: %w", err)
	}
	defer resp.Body.Close() // Ensure the response body is closed

	// Increase counter by one for returned metrics
	proxyReply.With(prometheus.Labels{
		"subject": topic,
		"code":    strconv.Itoa(resp.StatusCode),
	}).Inc()

	// Read the response body in case of an error for better logging
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		logger.Warn("Could not read response body for status %d: %v", resp.StatusCode, err)
	}

	// Check the response status code
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return "", fmt.Errorf("remote write endpoint returned non-success status: %s (body: %s)", resp.Status, string(responseBody))
	}

	if showDebug {
		logger.Debug(
			"Successfully relayed %d bytes to %s, status: %s",
			len(compressedData),
			remoteWriteURL,
			resp.Status,
		)
	}

	return "", nil
}
