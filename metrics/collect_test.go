package metrics_test

import (
	"math"
	"net"
	"slices"
	"testing"
	"time"

	"github.com/lesomnus/nl80211-exporter/metrics"
	"github.com/lesomnus/nl80211-exporter/nl80211"
	"go.opentelemetry.io/otel/attribute"
)

var (
	upstream = net.HardwareAddr{0x02, 0, 0, 0, 0, 0xaa}
	client   = net.HardwareAddr{0x02, 0, 0, 0, 0, 0x01}
	silent   = net.HardwareAddr{0x02, 0, 0, 0, 0, 0x02}
)

func snapshot(now time.Time) *nl80211.Snapshot {
	return &nl80211.Snapshot{
		Time: now,
		Interfaces: []nl80211.Interface{
			{
				Name: "phy1-sta0", Type: nl80211.TypeStation, PHY: "phy1", Frequency: 5260, ChannelWidth: 80,
				Stations: []nl80211.Station{{
					MAC: upstream, Connected: 3 * time.Hour, Inactive: 330 * time.Millisecond,
					Signal: -50, SignalAverage: -49, ReceiveBitrate: 1_200_900_000, TransmitBitrate: 1_080_600_000,
					ReceivedBytes: 1 << 40, TransmittedBytes: 1 << 33, ReceivedPackets: 7, TransmittedPackets: 8,
					TransmitRetries: 8600, TransmitFailed: 41, BeaconLosses: 2,
				}},
			},
			{
				Name: "phy1-ap0", Type: nl80211.TypeAP, PHY: "phy1", Frequency: 5260, ChannelWidth: 80,
				Stations: []nl80211.Station{
					{MAC: client, Connected: time.Minute + 400*time.Millisecond, Signal: -66, ReceiveBitrate: 650_000_000},
					// A driver that reports neither signal nor connected time.
					{MAC: silent, TransmitRetries: 3},
				},
			},
			{Name: "phy0-ap0", Type: nl80211.TypeAP, PHY: "phy0", Frequency: 2462, ChannelWidth: 20, Stations: []nl80211.Station{}},
			// Not on a channel, and a driver that lists no stations.
			{Name: "wlan9", Type: nl80211.TypeStation, PHY: "phy9"},
		},
		Channels: []nl80211.Channel{
			{PHY: "phy1", Frequency: 5260, Noise: -92, Active: 20295505 * time.Millisecond, Busy: 1745132 * time.Millisecond, Receive: 1414505 * time.Millisecond, BSSReceive: 72479 * time.Millisecond, Transmit: 293694 * time.Millisecond},
			{PHY: "phy0", Frequency: 2462, Active: 20291219 * time.Millisecond, Busy: 2654079 * time.Millisecond},
		},
	}
}

type labels map[string]any

func find(t *testing.T, points []metrics.Point, d *metrics.Desc, want labels) (metrics.Point, bool) {
	t.Helper()
	var found []metrics.Point
	for _, p := range points {
		if p.Desc != d || p.Attrs.Len() != len(want) {
			continue
		}
		match := true
		for k, v := range want {
			got, ok := p.Attrs.Value(attribute.Key(k))
			if !ok || got.AsInterface() != v {
				match = false
				break
			}
		}
		if match {
			found = append(found, p)
		}
	}
	if len(found) > 1 {
		t.Fatalf("%s %v: %d points", d.Name, want, len(found))
	}
	if len(found) == 0 {
		return metrics.Point{}, false
	}
	return found[0], true
}

func TestCollect(t *testing.T) {
	now := time.Date(2026, 11, 17, 9, 0, 0, 0, time.UTC)
	points := metrics.Collect(snapshot(now))

	order := map[*metrics.Desc]int{}
	for i, d := range metrics.All {
		order[d] = i
	}
	if !slices.IsSortedFunc(points, func(a, b metrics.Point) int { return order[a.Desc] - order[b.Desc] }) {
		t.Error("points are not in the order of All")
	}

	ifi := func(name, typ, phy string) labels {
		return labels{"network.interface.name": name, "wifi.interface.type": typ, "wifi.phy.name": phy}
	}
	sta := func(name, typ string, mac net.HardwareAddr, kvs ...any) labels {
		l := labels{"network.interface.name": name, "wifi.interface.type": typ, "wifi.station.mac": mac.String()}
		for i := 0; i < len(kvs); i += 2 {
			l[kvs[i].(string)] = kvs[i+1]
		}
		return l
	}
	ch := func(phy string, freq int64) labels {
		return labels{"wifi.phy.name": phy, "wifi.channel.frequency": freq}
	}
	rx := []any{"network.io.direction", "receive"}
	tx := []any{"network.io.direction", "transmit"}

	type want struct {
		d     *metrics.Desc
		attrs labels
		value float64
		start time.Time
	}
	for _, w := range []want{
		{metrics.InterfaceFrequency, ifi("phy1-sta0", "station", "phy1"), 5260, time.Time{}},
		{metrics.InterfaceChannelWidth, ifi("phy1-ap0", "ap", "phy1"), 80, time.Time{}},
		{metrics.InterfaceFrequency, ifi("phy0-ap0", "ap", "phy0"), 2462, time.Time{}},
		{metrics.StationCount, ifi("phy1-sta0", "station", "phy1"), 1, time.Time{}},
		{metrics.StationCount, ifi("phy1-ap0", "ap", "phy1"), 2, time.Time{}},
		{metrics.StationCount, ifi("phy0-ap0", "ap", "phy0"), 0, time.Time{}},

		{metrics.StationSignal, sta("phy1-sta0", "station", upstream), -50, time.Time{}},
		{metrics.StationSignalAverage, sta("phy1-sta0", "station", upstream), -49, time.Time{}},
		{metrics.StationBitrate, sta("phy1-sta0", "station", upstream, rx...), 1_200_900_000, time.Time{}},
		{metrics.StationBitrate, sta("phy1-sta0", "station", upstream, tx...), 1_080_600_000, time.Time{}},
		{metrics.StationIO, sta("phy1-sta0", "station", upstream, rx...), 1 << 40, now.Add(-3 * time.Hour)},
		{metrics.StationIO, sta("phy1-sta0", "station", upstream, tx...), 1 << 33, now.Add(-3 * time.Hour)},
		{metrics.StationPackets, sta("phy1-sta0", "station", upstream, tx...), 8, now.Add(-3 * time.Hour)},
		{metrics.StationTransmitRetries, sta("phy1-sta0", "station", upstream), 8600, now.Add(-3 * time.Hour)},
		{metrics.StationTransmitFailures, sta("phy1-sta0", "station", upstream), 41, now.Add(-3 * time.Hour)},
		{metrics.StationBeaconLosses, sta("phy1-sta0", "station", upstream), 2, now.Add(-3 * time.Hour)},
		{metrics.StationConnectedTime, sta("phy1-sta0", "station", upstream), 10800, time.Time{}},
		{metrics.StationInactiveTime, sta("phy1-sta0", "station", upstream), 0.33, time.Time{}},

		{metrics.StationSignal, sta("phy1-ap0", "ap", client), -66, time.Time{}},
		{metrics.StationIO, sta("phy1-ap0", "ap", client, rx...), 0, now.Add(-time.Minute - 400*time.Millisecond)},
		{metrics.StationConnectedTime, sta("phy1-ap0", "ap", client), 60, time.Time{}},
		{metrics.StationTransmitRetries, sta("phy1-ap0", "ap", silent), 3, time.Time{}},
		{metrics.StationInactiveTime, sta("phy1-ap0", "ap", silent), 0, time.Time{}},

		{metrics.ChannelNoise, ch("phy1", 5260), -92, time.Time{}},
		{metrics.ChannelActiveTime, ch("phy1", 5260), 20295.505, time.Time{}},
		{metrics.ChannelBusyTime, ch("phy1", 5260), 1745.132, time.Time{}},
		{metrics.ChannelReceiveTime, ch("phy1", 5260), 1414.505, time.Time{}},
		{metrics.ChannelBSSReceiveTime, ch("phy1", 5260), 72.479, time.Time{}},
		{metrics.ChannelTransmitTime, ch("phy1", 5260), 293.694, time.Time{}},
		{metrics.ChannelBusyTime, ch("phy0", 2462), 2654.079, time.Time{}},
	} {
		p, ok := find(t, points, w.d, w.attrs)
		if !ok {
			t.Errorf("%s %v: missing", w.d.Name, w.attrs)
			continue
		}
		if math.Abs(p.Value-w.value) > 1e-9 || !p.Start.Equal(w.start) {
			t.Errorf("%s %v: value %v start %v, want %v start %v", w.d.Name, w.attrs, p.Value, p.Start, w.value, w.start)
		}
	}

	for _, w := range []want{
		// No signal, average, bitrate, or connected time that was not reported.
		{d: metrics.StationSignalAverage, attrs: sta("phy1-ap0", "ap", client)},
		{d: metrics.StationBitrate, attrs: sta("phy1-ap0", "ap", client, tx...)},
		{d: metrics.StationSignal, attrs: sta("phy1-ap0", "ap", silent)},
		{d: metrics.StationConnectedTime, attrs: sta("phy1-ap0", "ap", silent)},
		// Access points count no beacon losses.
		{d: metrics.StationBeaconLosses, attrs: sta("phy1-ap0", "ap", client)},
		// No channel for an interface that is on none, and no count of stations its driver does not list.
		{d: metrics.InterfaceFrequency, attrs: ifi("wlan9", "station", "phy9")},
		{d: metrics.StationCount, attrs: ifi("wlan9", "station", "phy9")},
		// No noise or channel time that was not reported.
		{d: metrics.ChannelNoise, attrs: ch("phy0", 2462)},
		{d: metrics.ChannelTransmitTime, attrs: ch("phy0", 2462)},
	} {
		if p, ok := find(t, points, w.d, w.attrs); ok {
			t.Errorf("%s %v: want none, got %v", w.d.Name, w.attrs, p.Value)
		}
	}

	// The channel is an int attribute, as the interface frequency is an int.
	p, _ := find(t, points, metrics.ChannelNoise, ch("phy1", 5260))
	if v, _ := p.Attrs.Value(metrics.AttrChannelFrequency); v.Type() != attribute.INT64 {
		t.Errorf("channel frequency is a %s", v.Type())
	}
}

func TestCollectEmpty(t *testing.T) {
	if points := metrics.Collect(&nl80211.Snapshot{Time: time.Now()}); len(points) != 0 {
		t.Errorf("want no points, got %d", len(points))
	}
}
