package gcsproxy

import (
	"github.com/mholt/caddy/caddyhttp/httpserver"
	"github.com/prometheus/client_golang/prometheus"
	"log"
	"net/http"
	"sync"
)

const (
	defaultMetricPath = "/metrics"
	defaultMetricAddr = "localhost:9180"
	namespace         = "gcs_proxy"
)

var (
	requestCount       *prometheus.CounterVec
	gcsRequestDuration *prometheus.HistogramVec
	responseSize       *prometheus.HistogramVec
	responseStatus     *prometheus.CounterVec
	responseLatency    *prometheus.HistogramVec
)

// Metrics holds the prometheus configuration.
type Metrics struct {
	next         httpserver.Handler
	addr         string // where to we listen
	useCaddyAddr bool
	hostname     string
	path         string
	extraLabels  []extraLabel
	// subsystem?
	once sync.Once

	handler http.Handler
}

type extraLabel struct {
	name  string
	value string
}

func (m Metrics) define(subsystem string) {
	if subsystem == "" {
		subsystem = "http"
	}

	extraLabels := m.extraLabelNames()

	requestCount = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Subsystem: subsystem,
		Name:      "request_count_total",
		Help:      "Counter of HTTP(S) requests made.",
	}, append([]string{"host", "family", "proto"}, extraLabels...))

	gcsRequestDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: namespace,
		Subsystem: subsystem,
		Name:      "gcp_request_duration_seconds",
		Help:      "Histogram of the time (in seconds) each gcs request took.",
		Buckets:   append(prometheus.DefBuckets, 15, 20, 30, 60, 120, 180, 240, 480, 960),
	}, append([]string{"host", "family", "proto"}, extraLabels...))

	responseSize = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: namespace,
		Subsystem: subsystem,
		Name:      "response_size_bytes",
		Help:      "Size of the returns response in bytes.",
		Buckets:   []float64{0, 500, 1000, 2000, 3000, 4000, 5000, 10000, 20000, 30000, 50000, 1e5, 5e5, 1e6, 2e6, 3e6, 4e6, 5e6, 10e6},
	}, append([]string{"host", "family", "proto", "status"}, extraLabels...))

	responseStatus = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Subsystem: subsystem,
		Name:      "response_status_count_total",
		Help:      "Counter of response status codes.",
	}, append([]string{"host", "family", "proto", "status"}, extraLabels...))

	responseLatency = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: namespace,
		Subsystem: subsystem,
		Name:      "response_latency_seconds",
		Help:      "Histogram of the time (in seconds) until the first write for each request.",
		Buckets:   append(prometheus.DefBuckets, 15, 20, 30, 60, 120, 180, 240, 480, 960),
	}, append([]string{"host", "family", "proto", "status"}, extraLabels...))
}
func (m *Metrics) extraLabelNames() []string {
	names := make([]string, 0, len(m.extraLabels))

	for _, label := range m.extraLabels {
		names = append(names, label.name)
	}

	return names
}

func (m *Metrics) start() error {
	m.once.Do(func() {
		m.define("")

		prometheus.MustRegister(requestCount)
		prometheus.MustRegister(gcsRequestDuration)
		prometheus.MustRegister(responseLatency)
		prometheus.MustRegister(responseSize)
		prometheus.MustRegister(responseStatus)

		if !m.useCaddyAddr {
			http.Handle(m.path, m.handler)
			go func() {
				err := http.ListenAndServe(m.addr, nil)
				if err != nil {
					log.Printf("[ERROR] Starting handler: %v", err)
				}
			}()
		}
	})
	return nil
}

// NewMetrics -
func NewMetrics() *Metrics {
	return &Metrics{
		path:        defaultMetricPath,
		addr:        defaultMetricAddr,
		extraLabels: []extraLabel{},
	}
}
