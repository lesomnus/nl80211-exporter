package metrics

import (
	"time"

	"github.com/lesomnus/nl80211-exporter/nl80211"
	"go.opentelemetry.io/otel/attribute"
)

type Point struct {
	Desc  *Desc
	Attrs attribute.Set
	Value float64
	// Start of a counter, when known: when it last started from zero.
	// A station's counters start when it connected. Zero otherwise.
	Start time.Time
}

// Collect returns the points of a snapshot, in the order of All.
//
// nl80211 leaves out what a driver does not report, and a value left out reads as zero.
// So a zero signal, noise, bitrate, or connected time, and a zero channel time,
// are left out rather than reported: no WiFi link is anywhere near 0 dBm.
func Collect(s *nl80211.Snapshot) []Point {
	by := map[*Desc][]Point{}
	add := func(d *Desc, v float64, start time.Time, kvs ...attribute.KeyValue) {
		by[d] = append(by[d], Point{Desc: d, Attrs: attribute.NewSet(kvs...), Value: v, Start: start})
	}

	for _, ifi := range s.Interfaces {
		name := attribute.String(AttrInterfaceName, ifi.Name)
		typ := attribute.String(AttrInterfaceType, string(ifi.Type))
		phy := attribute.String(AttrPHYName, ifi.PHY)
		if ifi.Frequency != 0 {
			add(InterfaceFrequency, float64(ifi.Frequency), time.Time{}, name, typ, phy)
			add(InterfaceChannelWidth, float64(ifi.ChannelWidth), time.Time{}, name, typ, phy)
		}
		if ifi.Stations != nil {
			add(StationCount, float64(len(ifi.Stations)), time.Time{}, name, typ, phy)
		}

		for _, st := range ifi.Stations {
			mac := attribute.String(AttrStationMAC, st.MAC.String())
			gauge := func(d *Desc, v float64, kvs ...attribute.KeyValue) {
				if v != 0 {
					add(d, v, time.Time{}, append(kvs, name, typ, mac)...)
				}
			}
			var start time.Time
			if st.Connected > 0 {
				start = s.Time.Add(-st.Connected)
			}
			counter := func(d *Desc, v int, kvs ...attribute.KeyValue) {
				add(d, float64(v), start, append(kvs, name, typ, mac)...)
			}
			rx := attribute.String(AttrDirection, DirectionReceive)
			tx := attribute.String(AttrDirection, DirectionTransmit)

			gauge(StationSignal, float64(st.Signal))
			gauge(StationSignalAverage, float64(st.SignalAverage))
			gauge(StationBitrate, float64(st.ReceiveBitrate), rx)
			gauge(StationBitrate, float64(st.TransmitBitrate), tx)
			counter(StationIO, st.ReceivedBytes, rx)
			counter(StationIO, st.TransmittedBytes, tx)
			counter(StationPackets, st.ReceivedPackets, rx)
			counter(StationPackets, st.TransmittedPackets, tx)
			counter(StationTransmitRetries, st.TransmitRetries)
			counter(StationTransmitFailures, st.TransmitFailed)
			if ifi.Type == nl80211.TypeStation {
				counter(StationBeaconLosses, st.BeaconLosses)
			}
			gauge(StationConnectedTime, st.Connected.Truncate(time.Second).Seconds())
			add(StationInactiveTime, st.Inactive.Seconds(), time.Time{}, name, typ, mac)
		}
	}

	for _, ch := range s.Channels {
		phy := attribute.String(AttrPHYName, ch.PHY)
		freq := attribute.Int(AttrChannelFrequency, ch.Frequency)
		if ch.Noise != 0 {
			add(ChannelNoise, float64(ch.Noise), time.Time{}, phy, freq)
		}
		for _, c := range []struct {
			d *Desc
			v time.Duration
		}{
			{ChannelActiveTime, ch.Active},
			{ChannelBusyTime, ch.Busy},
			{ChannelReceiveTime, ch.Receive},
			{ChannelBSSReceiveTime, ch.BSSReceive},
			{ChannelTransmitTime, ch.Transmit},
		} {
			if c.v > 0 {
				add(c.d, c.v.Seconds(), time.Time{}, phy, freq)
			}
		}
	}

	var points []Point
	for _, d := range All {
		points = append(points, by[d]...)
	}
	return points
}
