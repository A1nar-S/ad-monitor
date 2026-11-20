package notifications

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// deliveries counts what happened to each message the notifier handled.
// Results: sent, failed (will be retried), dropped (undeliverable or no
// notifier), digested (queued for the digest instead of sent now).
var deliveries = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "admon_notifier_deliveries_total",
	Help: "Notifier deliveries by notifier kind and result (sent, failed, dropped, digested).",
}, []string{"kind", "result"})
