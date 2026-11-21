package listings

import (
	"context"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	ingested = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "admon_ingest_listings_total",
		Help: "Listings handled by ingest, by result: new (announced), seeded (new but stored silently), known.",
	}, []string{"result"})
	reposts = promauto.NewCounter(prometheus.CounterOpts{
		Name: "admon_ingest_reposts_total",
		Help: "New listings announced as a repost: their item (source fingerprint) was seen in an earlier ad.",
	})
	relayed = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "admon_outbox_relayed_total",
		Help: "Outbox entries handled by the relay, by result (published, failed).",
	}, []string{"result"})
)

// OutboxBacklog is how many outbox entries wait to be published. The partial
// index on unpublished rows keeps this cheap.
func (s *Service) OutboxBacklog(ctx context.Context) (int64, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	var n int64
	err := s.pool.QueryRow(ctx, `SELECT count(*) FROM listings_outbox WHERE published_at IS NULL`).Scan(&n)
	return n, err
}
