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
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/insikl/prometheus-nats-ambassador/internal/models"
)

// Build information.
const (
	BuildVersion = "0.1.4"
)

// Build information populated at build-time.
var (
	BuildName     string
	BuildCommit   string
	BuildBranch   string
	BuildUser     string
	BuildDate     string
	BuildGo       string
	BuildPlatform string
)

// Follows the same structure if pulled from dapr, get a list of topic/subject
// and the routes they need to call back to. Does not implement the `rules` yet
// https://docs.dapr.io/developing-applications/building-blocks/pubsub/subscription-methods/#programmatic-subscriptions
var (
	topicMap = make(map[string]string)
	// Topic base to publish requests to in the format of
	//  topicBase + host + port
	topicBase = "io.prometheus.exporter."
	topicFmt  = "mod"
)

func usage() {
	fmt.Printf(
		"Usage: %v <options>\n[Options]\n",
		BuildName,
	)
	flag.PrintDefaults()
}

func showUsageAndExit(exitcode int) {
	usage()
	os.Exit(exitcode)
}

// Handler/Context for NATS connection sharing to functions
type ProxyConn struct {
	nc *nats.Conn
}

func ProxyContext(nc *nats.Conn) *ProxyConn {
	if nc == nil {
		panic("nil NATS session!")
	}
	return &ProxyConn{nc}
}

// Internal metrics
var (
	proxyRequest = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "natsambassador",
			Name:      "requests_total",
			Help:      "No of request handled by NATS ambassador handler",
		},
		[]string{
			"subject",
			"code",
		},
	)

	proxyReply = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "natsambassador",
			Name:      "replies_total",
			Help:      "No of replies handled by NATS ambassador handler",
		},
		[]string{
			"subject",
			"code",
		},
	)
)

func main() {
	// Register PromHTTP request/reply counters
	prometheus.MustRegister(proxyRequest)
	prometheus.MustRegister(proxyReply)

	// CLI options
	var natsUrls = flag.String(
		"urls",
		nats.DefaultURL,
		"The NATS server URLs (separated by comma)",
	)
	// NATS connection options
	var natsCreds = flag.String(
		"creds",
		"",
		"User credentials file (required)",
	)
	var natsNkeyFile = flag.String(
		"nkey",
		"",
		"NKey Seed File",
	)
	var natsTlsClientCert = flag.String(
		"tlscert",
		"",
		"TLS client certificate file",
	)
	var natsTlsClientKey = flag.String(
		"tlskey",
		"",
		"Private key file for client certificate",
	)
	var natsTlsCACert = flag.String(
		"tlscacert",
		"",
		"CA certificate to verify peer against",
	)
	// File to look for in dapr programatic subcription format
	// https://docs.dapr.io/developing-applications/building-blocks/pubsub/subscription-methods/#programmatic-subscriptions
	var natsSubs = flag.String(
		"subs",
		"subscriptions.json",
		"Subscriptions file",
	)
	var basePub = flag.String(
		"subjbase",
		topicBase,
		"Set base subject/topic to publish requests to",
	)
	var baseFmt = flag.String(
		"subjfmt",
		topicFmt,
		"Set subject/topic format fwd, rev, or mod",
	)
	var listenAddress = flag.String(
		"listen",
		"localhost:8181",
		"Listen address",
	)
	var showHelp = flag.Bool(
		"h",
		false,
		"Show help message",
	)
	var showVersion = flag.Bool(
		"v",
		false,
		"Show version",
	)

	flag.Usage = usage
	flag.Parse()

	if *showHelp {
		showUsageAndExit(0)
	}

	if *showVersion {
		fmt.Printf("%v, version %v (branch: %v, revision: %v)\n",
			BuildName,
			BuildVersion,
			BuildBranch,
			BuildCommit,
		)
		fmt.Printf("  build user:       %v\n", BuildUser)
		fmt.Printf("  build date:       %v\n", BuildDate)
		fmt.Printf("  go version:       %v\n", BuildGo)
		fmt.Printf("  platform:         %v\n", BuildPlatform)
		os.Exit(0)
	}

	// Override default publish base
	if topicBase != *basePub {
		topicBase = *basePub
	}
	if topicFmt != *baseFmt {
		topicFmt = *baseFmt
	}

	// Open subscription config file
	// n our jsonFile
	// subscriptionRules := "subscriptions.json"
	var exporterSub []models.Subscription
	_, err := os.Stat(*natsSubs)

	if err != nil {
		log.Printf("No subscription file skipping any subscriptions\n")
	}

	log.Printf("Subscription file found [%v]\n", *natsSubs)
	jsonFile, err := os.Open(*natsSubs)
	// if we os.Open returns an error then handle it
	if err != nil {
		log.Fatalln(err)
	}
	log.Printf("Successfully Opened [%v]", *natsSubs)

	// Read files and and create `exporterSub` object.
	byteValue, err := io.ReadAll(jsonFile)
	if err != nil {
		log.Fatalln(err)
	}
	jsonFile.Close()
	err = json.Unmarshal(byteValue, &exporterSub)
	if err != nil {
		log.Fatalln(err)
	}

	// Connect Options.
	opts := []nats.Option{nats.Name(BuildName)}
	opts = setupConnOptions(opts)

	// Make sure `-creds` option is set
	if *natsCreds == "" {
		log.Fatal("specify -creds")
	}

	// Use UserCredentials
	if *natsCreds != "" {
		opts = append(opts, nats.UserCredentials(*natsCreds))
	}

	// Use TLS client authentication
	if *natsTlsClientCert != "" && *natsTlsClientKey != "" {
		opts = append(opts, nats.ClientCert(*natsTlsClientCert, *natsTlsClientKey))
	}

	// Use specific CA certificate
	if *natsTlsCACert != "" {
		opts = append(opts, nats.RootCAs(*natsTlsCACert))
	}

	// Use Nkey authentication.
	if *natsNkeyFile != "" {
		opt, err := nats.NkeyOptionFromSeed(*natsNkeyFile)
		if err != nil {
			log.Fatal(err)
		}
		opts = append(opts, opt)
	}

	// Connect to NATS
	nc, err := nats.Connect(*natsUrls, opts...)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("Connection successful to [%v]", string(*natsUrls))
	pubsubConn := ProxyContext(nc)

	// https://go.dev/tour/flowcontrol/12
	// https://go.dev/tour/flowcontrol/13
	defer nc.Close()

	// Get an array of subscriptions
	for i := 0; i < len(exporterSub); i++ {
		topicMap[exporterSub[i].Topic] = exporterSub[i].Route.Default
		_, err := nc.Subscribe(
			exporterSub[i].Topic,
			func(msg *nats.Msg) {
				reply, err := metrics(
					msg.Subject,
					topicMap[msg.Subject],
					string(msg.Data),
				)
				if err != nil {
					log.Printf("Error on response: [%v]\n", err)
				}
				msg.Respond([]byte(reply))
			},
		)

		if err != nil {
			log.Printf(
				"INFO subscribed to [%v], with endpoint [%v]\n",
				exporterSub[i].Topic,
				exporterSub[i].Route.Default,
			)
		}
	}

	// Prepare HTTP handlers
	http.Handle("/metrics", promhttp.Handler())
	http.HandleFunc("/proxy", pubsubConn.metricsHandler)
	log.Fatal(http.ListenAndServe(*listenAddress, nil))
}

func setupConnOptions(opts []nats.Option) []nats.Option {
	totalWait := 5 * time.Minute
	reconnectDelay := time.Second

	opts = append(opts, nats.ReconnectWait(reconnectDelay))
	opts = append(opts, nats.MaxReconnects(int(totalWait/reconnectDelay)))
	opts = append(opts, nats.DisconnectErrHandler(func(nc *nats.Conn, err error) {
		log.Printf("Disconnected due to:%s, will attempt reconnects for %.0fm", err, totalWait.Minutes())
	}))
	opts = append(opts, nats.ReconnectHandler(func(nc *nats.Conn) {
		log.Printf("Reconnected [%s]", nc.ConnectedUrl())
	}))
	opts = append(opts, nats.ClosedHandler(func(nc *nats.Conn) {
		log.Fatalf("Exiting: %v", nc.LastError())
	}))
	return opts
}

func (pubsub *ProxyConn) metricsHandler(w http.ResponseWriter, r *http.Request) {
	// Start timer
	start := time.Now()

	// https://go.dev/ref/spec#Expression_switches
	hostReqRaw := r.Header.Get("x-forwarded-host")
	switch hostReqRaw {
	case "":
		hostReqRaw = r.Host
	default:
		log.Fatalln("No host defined, unable to continue")
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
		// https://go.dev/src/net/http/status.go
		w.WriteHeader(http.StatusBadGateway)
		log.Println(err)
		return
	}

	// Check if we couldn't split, set `hostName` to `Host: <...>` and default
	// to port `80`.
	if hostName == "" && hostPort == "" {
		hostName = hostReq
		hostPort = string(rune(80))
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
			log.Fatalf("FATAL: %v last error for request\n", pubsub.nc.LastError())
		}
		// https://go.dev/src/net/http/status.go
		w.WriteHeader(http.StatusServiceUnavailable)
		log.Printf("ERROR: %v on subject [%v], %v\n", err, subj, time.Since(start))

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
func metrics(topic, urlHost, urlParam string) (string, error) {
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
	req, err := http.NewRequest("GET", urlReq, nil)
	if err != nil {
		log.Fatalln(err)
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
		fmt.Println(err)
	}

	resBody, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		log.Fatalln(err)
	}

	// Convert the byte body to type string
	bodyString := string(resBody)

	proxyReply.With(prometheus.Labels{
		"subject": topic,
		"code":    "200",
	}).Inc()

	return bodyString, nil
}
