package middleware

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/sony/gobreaker/v2"
)

// Metrics counts what the middleware layer rejects or retries. Edge 401/403 responses need no
// counter of their own: http_requests_total already has them by route. A nil *Metrics records nothing.
type Metrics struct {
	reg             prometheus.Registerer
	rateLimited     *prometheus.CounterVec
	retries         *prometheus.CounterVec
	circuitRejected *prometheus.CounterVec
}

func NewMetrics(reg prometheus.Registerer) *Metrics {
	m := &Metrics{
		reg: reg,
		rateLimited: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gateway_rate_limited_total",
			Help: "Requests rejected with 429, by rate limit tier.",
		}, []string{"tier"}),
		retries: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gateway_upstream_retries_total",
			Help: "Retried upstream calls, by upstream service.",
		}, []string{"upstream"}),
		circuitRejected: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gateway_circuit_rejected_total",
			Help: "Requests failed fast because the upstream's circuit was open.",
		}, []string{"upstream"}),
	}
	reg.MustRegister(m.rateLimited, m.retries, m.circuitRejected)
	return m
}

// Series are created at zero when a limit or upstream is set up: a counter that first appears
// with value 1 looks like no increase at all to increase() and rate().

func (m *Metrics) initTier(tier string) {
	if m != nil {
		m.rateLimited.WithLabelValues(tier)
	}
}

func (m *Metrics) rateLimit(tier string) {
	if m != nil {
		m.rateLimited.WithLabelValues(tier).Inc()
	}
}

func (m *Metrics) retry(upstream string) {
	if m != nil {
		m.retries.WithLabelValues(upstream).Inc()
	}
}

func (m *Metrics) circuitReject(upstream string) {
	if m != nil {
		m.circuitRejected.WithLabelValues(upstream).Inc()
	}
}

// watchCircuit exports the breaker state of upstream: 0 closed, 1 half-open, 2 open.
func (m *Metrics) watchCircuit(upstream string, cb *gobreaker.CircuitBreaker[struct{}]) {
	if m == nil {
		return
	}
	m.retries.WithLabelValues(upstream)
	m.circuitRejected.WithLabelValues(upstream)
	m.reg.MustRegister(prometheus.NewGaugeFunc(prometheus.GaugeOpts{
		Name:        "gateway_circuit_state",
		Help:        "Circuit breaker state per upstream: 0 closed, 1 half-open, 2 open.",
		ConstLabels: prometheus.Labels{"upstream": upstream},
	}, func() float64 { return float64(cb.State()) }))
}
