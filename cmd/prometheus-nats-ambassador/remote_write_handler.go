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
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// Read data for remote writer
func (pubsub *ProxyConn) remoteWriteHandler(w http.ResponseWriter, r *http.Request) {
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
	if r.Header.Get("Content-Type") != "application/x-protobuf" {
		log.Printf("Warning: Unexpected Content-Type: %s", r.Header.Get("Content-Type"))
		http.Error(w, "Content-Type must be application/x-protobuf", http.StatusBadRequest)
		return
	}
	if r.Header.Get("Content-Encoding") != "snappy" {
		log.Printf("Warning: Unexpected Content-Encoding: %s", r.Header.Get("Content-Encoding"))
		http.Error(w, "Content-Encoding must be snappy", http.StatusBadRequest)
		return
	}
	if r.Header.Get("X-Prometheus-Remote-Write-Version") != "0.1.0" {
		log.Printf("Warning: Unexpected X-Prometheus-Remote-Write-Version: %s", r.Header.Get("X-Prometheus-Remote-Write-Version"))
		http.Error(w, "X-Prometheus-Remote-Write-Version must be 0.1.0", http.StatusBadRequest)
		return
	}

	// Read the raw binary body directly
	compressedData, err := io.ReadAll(r.Body)
	if err != nil {
		log.Printf("Error reading request body: %v", err)
		http.Error(w, "Failed to read request body", http.StatusInternalServerError)
		return
	}

	if len(compressedData) == 0 {
		log.Println("Received empty request body.")
		http.Error(w, "Empty request body", http.StatusBadRequest)
		return
	}

	if showDebug {
		log.Printf(
			"DEBUG Received Prometheus remote write request (size: %d bytes). Publishing to NATS topic '%s'...",
			len(compressedData),
			topicBase,
		)
	}

	// Publish the raw compressed data to NATS
	err = pubsub.nc.Publish(topicBase, compressedData)
	if err != nil {
		log.Printf("Error publishing to NATS: %v", err)
		http.Error(w, "Failed to publish data to NATS", http.StatusInternalServerError)
		return
	}

	// Acknowledge receipt to Prometheus
	// 204 No Content is a common success response for remote write
	w.WriteHeader(http.StatusNoContent)
	if showDebug {
		log.Println("DEBUG Successfully published data to NATS and acknowledged to Prometheus.")
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
func relayPrometheusRemoteWrite(
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

	// Set the required Prometheus remote write headers from the input collected
	// Prometheus Remote Write 1.0 spec
	// https://prometheus.io/docs/specs/prw/remote_write_spec/#protocol
	req.Header.Set("Content-Type", "application/x-protobuf")
	req.Header.Set("Content-Encoding", "snappy")
	req.Header.Set("X-Prometheus-Remote-Write-Version", "0.1.0")

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
		log.Printf("Warning: Could not read response body for status %d: %v", resp.StatusCode, err)
	}

	// Check the response status code
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return "", fmt.Errorf("remote write endpoint returned non-success status: %s (body: %s)", resp.Status, string(responseBody))
	}

	if showDebug {
		log.Printf(
			"DEBUG Successfully relayed %d bytes to %s, status: %s",
			len(compressedData),
			remoteWriteURL,
			resp.Status,
		)
	}

	return "", nil
}
