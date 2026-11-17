package nl80211receiver_test

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/lesomnus/nl80211-exporter/metrics"
	"github.com/lesomnus/nl80211-exporter/nl80211"
	"github.com/lesomnus/nl80211-exporter/receiver/nl80211receiver"
	"go.opentelemetry.io/collector/component/componenttest"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/receiver/receivertest"
	"go.opentelemetry.io/otel/attribute"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

var (
	upstream = net.HardwareAddr{0x02, 0, 0, 0, 0, 0xaa}
	client   = net.HardwareAddr{0x02, 0, 0, 0, 0, 0x01}
)

// fakeReader answers with a router whose counters grow by one each read.
type fakeReader struct {
	mu     sync.Mutex
	reads  int
	err    error // with the snapshot
	fail   error // instead of it
	closed bool
}

func (f *fakeReader) Read(context.Context) (*nl80211.Snapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail != nil {
		return nil, f.fail
	}
	f.reads++
	n := f.reads
	return &nl80211.Snapshot{
		Time: time.Now(),
		Interfaces: []nl80211.Interface{
			{
				Name: "phy1-sta0", Type: nl80211.TypeStation, PHY: "phy1", Frequency: 5260, ChannelWidth: 80,
				Stations: []nl80211.Station{{
					MAC: upstream, Connected: 3 * time.Hour, Inactive: 330 * time.Millisecond,
					Signal: -50, SignalAverage: -49, ReceiveBitrate: 1_200_900_000, TransmitBitrate: 1_080_600_000,
					ReceivedBytes: 1000 * n, TransmittedBytes: 100 * n, ReceivedPackets: 10 * n, TransmittedPackets: n,
					TransmitRetries: n, TransmitFailed: n, BeaconLosses: n,
				}},
			},
			{
				Name: "phy1-ap0", Type: nl80211.TypeAP, PHY: "phy1", Frequency: 5260, ChannelWidth: 80,
				Stations: []nl80211.Station{{MAC: client, Connected: time.Minute, Signal: -66, SignalAverage: -66}},
			},
		},
		Channels: []nl80211.Channel{{
			PHY: "phy1", Frequency: 5260, Noise: -92,
			Active: time.Duration(n) * time.Hour, Busy: time.Duration(n) * time.Minute,
			Receive: time.Duration(n) * time.Second, BSSReceive: time.Duration(n) * time.Millisecond, Transmit: time.Duration(n) * time.Second,
		}},
	}, f.err
}

func (f *fakeReader) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	return nil
}

func TestConfig(t *testing.T) {
	f := nl80211receiver.NewFactory()
	if err := componenttest.CheckConfigStruct(f.CreateDefaultConfig()); err != nil {
		t.Fatal(err)
	}
}

func start(t *testing.T, open func() (nl80211receiver.Reader, error)) (*consumertest.MetricsSink, *observer.ObservedLogs, func()) {
	t.Helper()
	t.Cleanup(nl80211receiver.SetOpen(open))

	f := nl80211receiver.NewFactory()
	c := f.CreateDefaultConfig().(*nl80211receiver.Config)
	c.CollectionInterval = 20 * time.Millisecond
	c.InitialDelay = 0

	core, logs := observer.New(zap.InfoLevel)
	set := receivertest.NewNopSettings(nl80211receiver.Type)
	set.Logger = zap.New(core)

	sink := new(consumertest.MetricsSink)
	r, err := f.CreateMetrics(t.Context(), set, c, sink)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Start(t.Context(), componenttest.NewNopHost()); err != nil {
		t.Fatal(err)
	}
	return sink, logs, func() {
		if err := r.Shutdown(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
}

func waitScrapes(t *testing.T, sink *consumertest.MetricsSink, n int) []pmetric.Metrics {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for len(sink.AllMetrics()) < n {
		if time.Now().After(deadline) {
			t.Fatalf("want %d scrapes, got %d", n, len(sink.AllMetrics()))
		}
		time.Sleep(10 * time.Millisecond)
	}
	return sink.AllMetrics()
}

func TestReceiver(t *testing.T) {
	r := &fakeReader{}
	opened := time.Now()
	sink, logs, stop := start(t, func() (nl80211receiver.Reader, error) { return r, nil })
	all := waitScrapes(t, sink, 2)
	stop()
	if !r.closed {
		t.Error("the reader is not closed on shutdown")
	}
	if n := logs.Len(); n != 0 {
		t.Errorf("want no logs: %v", logs.All())
	}

	first, second := names(all[0]), names(all[1])
	for _, d := range metrics.All {
		m, ok := first[d.Name]
		if !ok {
			t.Errorf("%s: missing", d.Name)
			continue
		}
		if m.Unit() != d.Unit || m.Description() != d.Description {
			t.Errorf("%s: unit %q, description %q", d.Name, m.Unit(), m.Description())
		}

		var dps pmetric.NumberDataPointSlice
		if d.Kind == metrics.Counter {
			if m.Type() != pmetric.MetricTypeSum || !m.Sum().IsMonotonic() || m.Sum().AggregationTemporality() != pmetric.AggregationTemporalityCumulative {
				t.Errorf("%s: not a monotonic cumulative sum", d.Name)
				continue
			}
			dps = m.Sum().DataPoints()
		} else {
			if m.Type() != pmetric.MetricTypeGauge {
				t.Errorf("%s: not a gauge", d.Name)
				continue
			}
			dps = m.Gauge().DataPoints()
		}
		for i := range dps.Len() {
			dp := dps.At(i)
			want := pmetric.NumberDataPointValueTypeInt
			if d.Float {
				want = pmetric.NumberDataPointValueTypeDouble
			}
			if dp.ValueType() != want {
				t.Errorf("%s: value type %s", d.Name, dp.ValueType())
			}
			if d.Kind == metrics.Counter && (dp.StartTimestamp() == 0 || dp.StartTimestamp() > dp.Timestamp()) {
				t.Errorf("%s: start %v, time %v", d.Name, dp.StartTimestamp(), dp.Timestamp())
			}
		}
	}

	// A station's counters start when it connected; a channel's when the receiver did.
	io := first["wifi.station.io"].Sum().DataPoints().At(0)
	if got, want := io.StartTimestamp().AsTime(), io.Timestamp().AsTime().Add(-3*time.Hour); got.Sub(want).Abs() > time.Millisecond {
		t.Errorf("station counter: start %v, want %v", got, want)
	}
	busy := first["wifi.channel.busy_time"].Sum().DataPoints().At(0)
	if s := busy.StartTimestamp().AsTime(); s.Before(opened) || s.After(busy.Timestamp().AsTime()) {
		t.Errorf("channel counter: start %v, not between %v and its first scrape", s, opened)
	}
	// Starts stay put across scrapes.
	for _, name := range []string{"wifi.station.io", "wifi.channel.busy_time"} {
		a := first[name].Sum().DataPoints().At(0)
		b := second[name].Sum().DataPoints().At(0)
		if a.StartTimestamp() != b.StartTimestamp() || b.IntValue()+int64(b.DoubleValue()) <= a.IntValue()+int64(a.DoubleValue()) {
			t.Errorf("%s: start %v -> %v, value %v -> %v", name, a.StartTimestamp(), b.StartTimestamp(), a, b)
		}
	}

	// Attributes keep their types: the channel is an int.
	dp := first["wifi.channel.noise"].Gauge().DataPoints().At(0)
	if v, ok := dp.Attributes().Get("wifi.channel.frequency"); !ok || v.Type() != pcommon.ValueTypeInt || v.Int() != 5260 {
		t.Errorf("channel attribute: %v", dp.Attributes().AsRaw())
	}
}

func TestNotAvailable(t *testing.T) {
	opens := 0
	var mu sync.Mutex
	sink, logs, stop := start(t, func() (nl80211receiver.Reader, error) {
		mu.Lock()
		defer mu.Unlock()
		opens++
		return nil, fmt.Errorf("%w: no cfg80211", nl80211.ErrNotAvailable)
	})
	time.Sleep(150 * time.Millisecond)
	stop()

	if n := logs.FilterMessageSnippet("not available").Len(); n != 1 {
		t.Errorf("want one idle message, got %d: %v", n, logs.All())
	}
	if n := logs.FilterLevelExact(zap.WarnLevel).Len() + logs.FilterLevelExact(zap.ErrorLevel).Len(); n != 0 {
		t.Errorf("want no warnings or errors: %v", logs.All())
	}
	if n := sink.DataPointCount(); n != 0 {
		t.Errorf("want no data points, got %d", n)
	}
	if mu.Lock(); opens < 2 {
		t.Errorf("want it tried again each scrape, opened %d times", opens)
	}
	mu.Unlock()
}

func TestPartial(t *testing.T) {
	r := &fakeReader{err: errors.Join(errors.New("phy1-ap0: stations: EIO"), errors.New("phy1: survey: EIO"))}
	sink, _, stop := start(t, func() (nl80211receiver.Reader, error) { return r, nil })
	all := waitScrapes(t, sink, 1)
	stop()
	if _, ok := names(all[0])["wifi.station.signal"]; !ok {
		t.Error("what was read is not reported")
	}
}

func TestReconnect(t *testing.T) {
	var (
		mu      sync.Mutex
		readers []*fakeReader
	)
	sink, _, stop := start(t, func() (nl80211receiver.Reader, error) {
		mu.Lock()
		defer mu.Unlock()
		r := &fakeReader{}
		if len(readers) == 0 {
			r.fail = errors.New("netlink receive: bad file descriptor")
		}
		readers = append(readers, r)
		return r, nil
	})
	deadline := time.Now().Add(5 * time.Second)
	for sink.DataPointCount() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("no data points after the reader that read nothing")
		}
		time.Sleep(10 * time.Millisecond)
	}
	stop()

	mu.Lock()
	defer mu.Unlock()
	if len(readers) < 2 || !readers[0].closed {
		t.Errorf("a reader that read nothing is not closed and replaced: %d readers", len(readers))
	}
}

func TestCounterStarts(t *testing.T) {
	sec := func(s int64) pcommon.Timestamp { return pcommon.Timestamp(s * int64(time.Second)) }
	at := func(s int64) time.Time { return sec(s).AsTime() }
	start := func(t *testing.T, md pmetric.Metrics, d *metrics.Desc, name string) pcommon.Timestamp {
		t.Helper()
		ms := md.ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics()
		for i := range ms.Len() {
			if ms.At(i).Name() != d.Name {
				continue
			}
			dps := ms.At(i).Sum().DataPoints()
			for j := range dps.Len() {
				if v, _ := dps.At(j).Attributes().Get("k"); v.Str() == name {
					return dps.At(j).StartTimestamp()
				}
			}
		}
		t.Fatalf("no %s %s", d.Name, name)
		return 0
	}
	set := func(name string) attribute.Set { return attribute.NewSet(attribute.String("k", name)) }

	t.Run("known", func(t *testing.T) {
		d := metrics.StationIO
		st := nl80211receiver.NewStarts(sec(1000))
		scrape := func(ts int64, points ...metrics.Point) pmetric.Metrics {
			return nl80211receiver.ToMetrics(points, st, sec(ts))
		}
		p := func(name string, v float64, since int64) metrics.Point {
			return metrics.Point{Desc: d, Attrs: set(name), Value: v, Start: at(since)}
		}

		md := scrape(1010, p("a", 5, 400))
		if s := start(t, md, d, "a"); s != sec(400) {
			t.Errorf("connected before the receiver started: start %v, want when it connected", s)
		}
		// Whole seconds of connected time wander against the scrape time.
		md = scrape(1020, p("a", 9, 401))
		if s := start(t, md, d, "a"); s != sec(400) {
			t.Errorf("grown: start %v, want unchanged", s)
		}
		md = scrape(1030, p("a", 2, 1025))
		if s := start(t, md, d, "a"); s != sec(1025) {
			t.Errorf("connected again: start %v, want when it connected again", s)
		}
		md = scrape(1040, p("a", 1, 1025))
		if s := start(t, md, d, "a"); s != sec(1030) {
			t.Errorf("wrapped around: start %v, want the previous scrape", s)
		}
		md = scrape(1050, p("a", 4, 1026))
		if s := start(t, md, d, "a"); s != sec(1030) {
			t.Errorf("grown after wrapping: start %v, want unchanged rather than when it connected", s)
		}
	})

	t.Run("unknown", func(t *testing.T) {
		d := metrics.ChannelBusyTime
		st := nl80211receiver.NewStarts(sec(100))
		p := func(name string, v float64) metrics.Point { return metrics.Point{Desc: d, Attrs: set(name), Value: v} }

		md := nl80211receiver.ToMetrics([]metrics.Point{p("a", 5)}, st, sec(110))
		if s := start(t, md, d, "a"); s != sec(100) {
			t.Errorf("present at the first scrape: start %v, want the receiver's start", s)
		}
		md = nl80211receiver.ToMetrics([]metrics.Point{p("a", 9), p("b", 1)}, st, sec(120))
		if s := start(t, md, d, "a"); s != sec(100) {
			t.Errorf("grown: start %v, want unchanged", s)
		}
		if s := start(t, md, d, "b"); s != sec(110) {
			t.Errorf("new: start %v, want the previous scrape", s)
		}
		md = nl80211receiver.ToMetrics([]metrics.Point{p("a", 2), p("b", 3)}, st, sec(130))
		if s := start(t, md, d, "a"); s != sec(120) {
			t.Errorf("reset: start %v, want the previous scrape", s)
		}
		if s := start(t, md, d, "b"); s != sec(110) {
			t.Errorf("grown: start %v, want unchanged", s)
		}
	})
}

func names(md pmetric.Metrics) map[string]pmetric.Metric {
	m := map[string]pmetric.Metric{}
	ms := md.ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics()
	for i := range ms.Len() {
		m[ms.At(i).Name()] = ms.At(i)
	}
	return m
}
