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
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/insikl/prometheus-nats-ambassador/internal/logger"
	"github.com/insikl/prometheus-nats-ambassador/internal/models"
)

// Build information.
const (
	BuildVersion = "0.2.1"
)

// Build information populated at build-time.
var (
	BuildName   string
	BuildCommit string
	BuildBranch string
	BuildUser   string
	BuildDate   string
	BuildGo     string
	BuildOs     string
	BuildArch   string
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

	// Set useragent
	// <AppName>/<AppVersion> (Go/<GoVersion>; <OS>; <Arch>)
	userAgent = fmt.Sprintf("PrometheusNATSAmbassador/%s (Go/%s; %s; %s)",
		BuildVersion,
		BuildGo,
		BuildOs,
		BuildArch,
	)
	topicRemoteWrite = ""
	showDebug        = false
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
	var remoteWrite = flag.String(
		"remotewrite",
		topicRemoteWrite,
		"Remote write endpoint, cannot be used with '-subs'",
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
	var enableDebug = flag.Bool(
		"d",
		showDebug,
		"Show debug output",
	)

	flag.Usage = usage
	flag.Parse()

	// override default value for debug if set
	if *enableDebug {
		showDebug = true
	}

	// IMPORTANT: Disable all default flags since our custom logger
	// now handles formatting the time.
	log.SetFlags(0)

	// Set debug logging if set
	if showDebug {
		// Extra line prints showing the file and line number of the debug log
		logger.SetLogLevel(logger.DEBUG)
		logger.Debug("Debug logging enabled")
	} else {
		logger.SetLogLevel(logger.INFO)
	}

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
		fmt.Printf("  platform:         %v/%v\n", BuildOs, BuildArch)
		os.Exit(0)
	}

	// Override default publish base
	if topicBase != *basePub {
		topicBase = *basePub
	}
	if topicFmt != *baseFmt {
		topicFmt = *baseFmt
	}
	if *remoteWrite != "" {
		topicRemoteWrite = *remoteWrite
	}

	// Open subscription config file
	// subscriptionRules := "subscriptions.json"
	var exporterSub []models.Subscription
	_, err := os.Stat(*natsSubs)

	if err != nil {
		logger.Info("No subscription file skipping any subscriptions")
	} else {
		logger.Info("Subscription file found [%v]\n", *natsSubs)
		jsonFile, err := os.Open(*natsSubs)
		// if we os.Open returns an error then handle it
		if err != nil {
			logger.Fatal("%v", err)
		}
		logger.Info("Successfully Opened [%v]", *natsSubs)

		// Read files and and create `exporterSub` object.
		byteValue, err := io.ReadAll(jsonFile)
		if err != nil {
			logger.Fatal("%v", err)
		}
		jsonFile.Close()
		err = json.Unmarshal(byteValue, &exporterSub)
		if err != nil {
			logger.Fatal("%v", err)
		}
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
		logger.Fatal("%v", err)
	}
	logger.Info("Connection successful to [%v]", string(*natsUrls))
	pubsubConn := ProxyContext(nc)

	// https://go.dev/tour/flowcontrol/12
	// https://go.dev/tour/flowcontrol/13
	defer nc.Close()

	// Get an array of subscriptions
	for i := 0; i < len(exporterSub); i++ {
		// Get the topic we're going to subscribe to
		topicMap[exporterSub[i].Topic] = exporterSub[i].Route.Default
		_, err := nc.Subscribe(
			exporterSub[i].Topic,
			func(msg *nats.Msg) {
				if showDebug {
					logger.Debug(
						"incoming message for relay on [%v] to endpoint [%v]",
						msg.Subject,
						topicMap[msg.Subject],
					)
				}

				reply, err := ProxyPrometheusRequest(
					msg.Subject,
					topicMap[msg.Subject],
					string(msg.Data),
				)
				if err != nil {
					logger.Error("Error on response: [%v]", err)
				}
				msg.Respond([]byte(reply))
			},
		)

		if err != nil {
			logger.Error("%v", err)
		} else {
			logger.Info(
				"subscribed to [%v], with endpoint [%v]",
				exporterSub[i].Topic,
				exporterSub[i].Route.Default,
			)
		}
	}

	// Check if `-remotewrite` is set and use the `subjbase` to subscribe with
	// this is to avoid using the subscription file used in the req/reply method
	// that did dynamic creation of NATS subjects. In the remote write scenario
	// there is only one subscription that happens on the specified base target
	if topicRemoteWrite != "" {
		_, err := nc.Subscribe(
			topicBase,
			func(msg *nats.Msg) {
				if showDebug {
					logger.Debug(
						"incoming message for relay on [%v] to endpoint [%v]",
						msg.Subject,
						topicRemoteWrite,
					)
				}

				_, err := RelayPrometheusRemoteWrite(
					msg.Subject,
					topicRemoteWrite,
					msg.Data,
				)
				if err != nil {
					logger.Error("Error on response: [%v]", err)
				}
			},
		)

		if err != nil {
			logger.Error("%v", err)
		} else {
			logger.Info(
				"subscribed to [%v], with endpoint [%v]",
				topicBase,
				topicRemoteWrite,
			)
		}
	}

	// Prepare HTTP handlers
	http.Handle("/metrics", promhttp.Handler())
	http.HandleFunc("/proxy", pubsubConn.ProxyRequestHandler)
	http.HandleFunc("/api/v1/write", pubsubConn.RemoteWriteHandler)
	log.Fatal(http.ListenAndServe(*listenAddress, nil))
}

func setupConnOptions(opts []nats.Option) []nats.Option {
	totalWait := 5 * time.Minute
	reconnectDelay := time.Second

	opts = append(opts, nats.ReconnectWait(reconnectDelay))
	opts = append(opts, nats.MaxReconnects(int(totalWait/reconnectDelay)))
	opts = append(opts, nats.DisconnectErrHandler(func(nc *nats.Conn, err error) {
		logger.Warn("Disconnected due to:%s, will attempt reconnects for %.0fm", err, totalWait.Minutes())
	}))
	opts = append(opts, nats.ReconnectHandler(func(nc *nats.Conn) {
		logger.Warn("Reconnected [%s]", nc.ConnectedUrl())
	}))
	opts = append(opts, nats.ClosedHandler(func(nc *nats.Conn) {
		logger.Fatal("Exiting: %v", nc.LastError())
	}))
	return opts
}
