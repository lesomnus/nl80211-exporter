package nl80211receiver

import (
	"context"
	"errors"
	"time"

	"github.com/lesomnus/nl80211-exporter/metrics"
	"github.com/lesomnus/nl80211-exporter/nl80211"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/scraper/scrapererror"
	"go.opentelemetry.io/otel/attribute"
	"go.uber.org/zap"
)

// reader is what the scraper uses of *nl80211.Reader.
type reader interface {
	Read(ctx context.Context) (*nl80211.Snapshot, error)
	Close() error
}

// open connects to nl80211; tests stand in for it.
var open = func() (reader, error) {
	r, err := nl80211.Open()
	if err != nil {
		return nil, err
	}
	return r, nil
}

type nl80211Scraper struct {
	logger *zap.Logger
	open   func() (reader, error)

	r           reader
	unavailable bool
	starts      *starts
}

func newScraper(logger *zap.Logger) *nl80211Scraper {
	return &nl80211Scraper{logger: logger, open: open}
}

func (s *nl80211Scraper) start(_ context.Context, _ component.Host) error {
	s.starts = newStarts(pcommon.NewTimestampFromTime(time.Now()))
	return nil
}

func (s *nl80211Scraper) shutdown(_ context.Context) error {
	return s.close()
}

func (s *nl80211Scraper) close() error {
	if s.r == nil {
		return nil
	}
	err := s.r.Close()
	s.r = nil
	return err
}

func (s *nl80211Scraper) scrape(ctx context.Context) (pmetric.Metrics, error) {
	if s.r == nil {
		r, err := s.open()
		if err != nil {
			if errors.Is(err, nl80211.ErrNotAvailable) {
				// One config fits every node: where there is no WiFi, say so once and stay idle.
				if !s.unavailable {
					s.logger.Info("nl80211 is not available; the receiver stays idle", zap.Error(err))
					s.unavailable = true
				}
				return pmetric.NewMetrics(), nil
			}
			return pmetric.NewMetrics(), err
		}
		if s.unavailable {
			s.logger.Info("nl80211 is available")
			s.unavailable = false
		}
		s.r = r
	}

	snap, err := s.r.Read(ctx)
	if snap == nil {
		// Connect again on the next scrape, in case the connection went stale,
		// e.g. when cfg80211 was reloaded.
		_ = s.close()
		return pmetric.NewMetrics(), err
	}

	md := toMetrics(metrics.Collect(snap), s.starts, pcommon.NewTimestampFromTime(snap.Time))
	if err != nil {
		return md, scrapererror.NewPartialScrapeError(err, failures(err))
	}
	return md, nil
}

// failures counts the reads that failed, which errors.Join put together.
func failures(err error) int {
	if j, ok := err.(interface{ Unwrap() []error }); ok {
		return len(j.Unwrap())
	}
	return 1
}

// starts keeps when each counter series started.
//
// A point that knows its start, like a station's counter that started when the
// station connected, starts then. Any other starts when the receiver did, or,
// for a series that appeared or reset later, at the previous scrape.
type starts struct {
	first pcommon.Timestamp
	prev  pcommon.Timestamp
	seen  map[*metrics.Desc]map[attribute.Distinct]series
}

type series struct {
	start pcommon.Timestamp
	// The start the point knew, if any.
	known pcommon.Timestamp
	last  float64
}

// slack is how far a known start may wander between scrapes and still be the same:
// nl80211 reports how long a station has been connected in whole seconds.
const slack = 2 * time.Second

func newStarts(first pcommon.Timestamp) *starts {
	return &starts{first: first, prev: first, seen: map[*metrics.Desc]map[attribute.Distinct]series{}}
}

func toMetrics(points []metrics.Point, st *starts, ts pcommon.Timestamp) pmetric.Metrics {
	md := pmetric.NewMetrics()
	sm := md.ResourceMetrics().AppendEmpty().ScopeMetrics().AppendEmpty()
	sm.Scope().SetName(ScopeName)

	seen := map[*metrics.Desc]map[attribute.Distinct]series{}
	var (
		desc *metrics.Desc
		dps  pmetric.NumberDataPointSlice
	)
	for _, p := range points {
		if p.Desc != desc {
			desc = p.Desc
			m := sm.Metrics().AppendEmpty()
			m.SetName(desc.Name)
			m.SetDescription(desc.Description)
			m.SetUnit(desc.Unit)
			if desc.Kind == metrics.Counter {
				sum := m.SetEmptySum()
				sum.SetIsMonotonic(true)
				sum.SetAggregationTemporality(pmetric.AggregationTemporalityCumulative)
				dps = sum.DataPoints()
			} else {
				dps = m.SetEmptyGauge().DataPoints()
			}
		}

		dp := dps.AppendEmpty()
		dp.SetTimestamp(ts)
		if desc.Float {
			dp.SetDoubleValue(p.Value)
		} else {
			dp.SetIntValue(int64(p.Value))
		}
		for _, kv := range p.Attrs.ToSlice() {
			switch kv.Value.Type() {
			case attribute.INT64:
				dp.Attributes().PutInt(string(kv.Key), kv.Value.AsInt64())
			default:
				dp.Attributes().PutStr(string(kv.Key), kv.Value.Emit())
			}
		}

		if desc.Kind == metrics.Counter && st != nil {
			key := p.Attrs.Equivalent()
			s, ok := st.seen[desc][key]
			if !p.Start.IsZero() {
				known := pcommon.NewTimestampFromTime(p.Start)
				switch {
				case !ok || !near(known, s.known):
					// New, or started again since, e.g. a station that connected again.
					// Also when the clock was stepped, so the start follows it.
					s.start, s.known = known, known
				case p.Value < s.last:
					// Reset while the start stayed, e.g. a 32-bit counter wrapped around.
					s.start = st.prev
				}
			} else {
				switch {
				case !ok && st.prev == st.first:
					s.start = st.first
				case !ok || p.Value < s.last:
					// New since the previous scrape, or reset.
					s.start = st.prev
				}
			}
			s.last = p.Value
			if seen[desc] == nil {
				seen[desc] = map[attribute.Distinct]series{}
			}
			seen[desc][key] = s
			dp.SetStartTimestamp(s.start)
		}
	}

	if st != nil {
		// Forget series that went away, so their keys do not pile up.
		st.seen = seen
		st.prev = ts
	}
	return md
}

func near(a, b pcommon.Timestamp) bool {
	d := time.Duration(int64(a) - int64(b))
	return -slack <= d && d <= slack
}
