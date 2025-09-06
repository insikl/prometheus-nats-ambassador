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
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/insikl/prometheus-nats-ambassador/internal/logger"
	"github.com/prometheus/client_golang/prometheus"
)

// HTTP handler function for `/proxy` endpoint
func (pubsub *ProxyConn) ProxyRequestHandler(w http.ResponseWriter, r *http.Request) {
	// Start timer
	start := time.Now()

	// https://go.dev/ref/spec#Expression_switches
	hostReqRaw := r.Header.Get("x-forwarded-host")
	switch hostReqRaw {
	case "":
		hostReqRaw = r.Host
	default:
		logger.Fatal("No host defined, unable to continue")
	}

	promScrapeTimeoutRaw, err := strconv.Atoi(r.Header.Get("x-prometheus-scrape-timeout-seconds"))
	if err != nil {
		// Unable to convert string to int, default to 10 seconds.
		promScrapeTimeoutRaw = 10
	}
	// Set NATS timeout on request
	// https://pkg.go.dev/time#Second
	promScrapeTimeout := time.Duration(promScrapeTimeoutRaw) * time.Second

	// Normalize hostname to lowercase
	hostReq := strings.ToLower(hostReqRaw)

	if hostReq == "" {
		// https://prometheus.io/docs/instrumenting/writing_exporters/#failed-scrapes
		// https://go.dev/src/net/http/status.go
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusBadGateway)
		w.Write([]byte(`<html>
		    <head><title>Prometheus NATS Ambassador</title></head>
			<body><b>ERROR: missing Host parameter</b></body>
			</html>`))
		return
	}

	// Get request and findout NATS subject we'll send requests to
	hostName, hostPort, err := net.SplitHostPort(hostReq)
	if err != nil {
		logger.Error("%v", err)
		// https://go.dev/src/net/http/status.go
		w.WriteHeader(http.StatusBadGateway)
		return
	}

	// Check if we couldn't split, set `hostName` to `Host: <...>` and default
	// to port `80`.
	if hostName == "" && hostPort == "" {
		hostName = hostReq
		hostPort = strconv.Itoa(80)
	}

	// Normalize hostname, create array of hostname in forward and reverse.
	hostFwd := strings.Split(hostName, ".")

	// Reverse hostname
	var hostRev []string
	for _, n := range hostFwd {
		hostRev = append([]string{n}, hostRev...)
	}

	// var subFormat string
	switch topicFmt {
	case "rev":
		hostName = strings.Join(hostRev, ".")
	case "fwd":
		hostName = strings.Join(hostFwd, ".")
	default:
		hostName = strings.ReplaceAll(hostName, ".", "_")
	}

	// Build NATS subject
	subj := topicBase + hostName + "." + hostPort

	// https://pkg.go.dev/net/http#Request.URL
	q := r.URL.Query()
	q.Add("x-prometheus-scrape-timeout-seconds", strconv.Itoa(promScrapeTimeoutRaw))
	r.URL.RawQuery = q.Encode()
	payload := []byte(r.URL.RawQuery)
	msg, err := pubsub.nc.Request(subj, payload, promScrapeTimeout)

	if err != nil {
		if pubsub.nc.LastError() != nil {
			logger.Fatal("%v last error for request\n", pubsub.nc.LastError())
		}
		// https://go.dev/src/net/http/status.go
		w.WriteHeader(http.StatusServiceUnavailable)
		logger.Error("%v on subject [%v], %v\n", err, subj, time.Since(start))

		// Increase counter by one
		proxyRequest.With(prometheus.Labels{
			"subject": subj,
			"code":    "503",
		}).Inc()
		return
	}

	// Increase counter by one
	proxyRequest.With(prometheus.Labels{
		"subject": subj,
		"code":    "200",
	}).Inc()

	w.Header().Set("Content-Type", "text/plain")
	w.Write(msg.Data)
}

// NATS listen subscriber
func ProxyPrometheusRequest(topic, urlHost, urlParam string) (string, error) {
	if topic == "" {
		proxyReply.With(prometheus.Labels{
			"subject": topic,
			"code":    "400",
		}).Inc()
		return "", errors.New("400 Bad Request no topic")
	}

	// Parse body of NATS message which is expected to be a query string
	p, _ := url.ParseQuery(urlParam)

	// Look for Prometheus scrape timeout in the query string which gets posted
	// as the body of the NATS message. If not found default to 10 seconds.
	timeoutScrape, err := strconv.Atoi(p.Get("x-prometheus-scrape-timeout-seconds"))
	if err != nil {
		timeoutScrape = 10
	}

	// Remove and rebuild query, even if `urlParam` is blank, not a big deal if
	// URL ends with a `?`
	p.Del("x-prometheus-scrape-timeout-seconds")
	urlParam = p.Encode()
	urlReq := urlHost + "?" + urlParam

	// Prepare rull URL request to pull from subscription rules
	req, err := http.NewRequest(http.MethodGet, urlReq, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create HTTP GET request: %w", err)
	}

	// Set any headers and timeout, understood there could be an issue with the
	// timeout as the same value is set in multiple places and weird race
	// conditions can happen. HOWEVER, if an exporter is always close to max
	// scrape timeout, that timeout should be increased on the scrapper.
	req.Header.Set("Content-Type", "text/plain")
	client := http.Client{
		Timeout: time.Duration(timeoutScrape) * time.Second,
	}
	resp, err := client.Do(req)

	if err != nil {
		return "", fmt.Errorf("failed to send HTTP request: %w", err)
	}

	resBody, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return "", fmt.Errorf("failed to read response body: %w", err)
	}

	// Convert the byte body to type string
	bodyString := string(resBody)

	proxyReply.With(prometheus.Labels{
		"subject": topic,
		"code":    "200",
	}).Inc()

	return bodyString, nil
}
