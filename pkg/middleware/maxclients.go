package middleware

import (
	"net/http"
	"sync/atomic"

	"github.com/prometheus/client_golang/prometheus"
)

var sseRejected = prometheus.NewCounter(
	prometheus.CounterOpts{
		Name: "sse_connections_rejected_total",
		Help: "Total SSE connections rejected due to max clients",
	},
)

func init() {
	prometheus.MustRegister(sseRejected)
}

// MaxSSEClients limits the number of concurrent SSE connections.
func MaxSSEClients(max int) func(http.HandlerFunc) http.HandlerFunc {
	var active atomic.Int64

	return func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			current := active.Add(1)
			if current > int64(max) {
				active.Add(-1)
				sseRejected.Inc()
				http.Error(w, "Too many streaming connections", http.StatusServiceUnavailable)
				return
			}
			defer active.Add(-1)
			next(w, r)
		}
	}
}
