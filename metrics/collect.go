package metrics

import (
	"maps"
	"slices"
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
// nl80211 leaves out what a driver does not report. Through github.com/mdlayher/wifi a value
// left out reads as zero, so a zero signal, noise, bitrate, or connected time, and a zero
// channel time, are left out rather than reported: no WiFi link is anywhere near 0 dBm.
// The details read directly (nl80211.Value) say whether they were reported, and are left out
// only when they were not.
func Collect(s *nl80211.Snapshot) []Point {
	by := map[*Desc][]Point{}
	add := func(d *Desc, v float64, start time.Time, kvs ...attribute.KeyValue) {
		by[d] = append(by[d], Point{Desc: d, Attrs: attribute.NewSet(kvs...), Value: v, Start: start})
	}

	for _, ifi := range s.Interfaces {
		name := attribute.String(AttrInterfaceName, ifi.Name)
		typ := attribute.String(AttrInterfaceType, string(ifi.Type))
		ifa := []attribute.KeyValue{name, typ, attribute.String(AttrPHYName, ifi.PHY)}
		if ifi.SSID != "" {
			ifa = append(ifa, attribute.String(AttrSSID, ifi.SSID))
		}
		if ifi.Frequency != 0 {
			add(InterfaceFrequency, float64(ifi.Frequency), time.Time{}, ifa...)
			add(InterfaceChannelWidth, float64(ifi.ChannelWidth), time.Time{}, ifa...)
		}
		if v := ifi.CenterFrequency; v.OK {
			add(InterfaceCenterFrequency, float64(v.V), time.Time{}, ifa...)
		}
		if v := ifi.TransmitPower; v.OK {
			add(InterfaceTransmitPower, v.V, time.Time{}, ifa...)
		}
		if ifi.Stations != nil {
			add(StationCount, float64(len(ifi.Stations)), time.Time{}, ifa...)
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

			// A detail read directly is reported whenever the driver gave it, zero included.
			known := func(d *Desc, v float64, start time.Time, kvs ...attribute.KeyValue) {
				add(d, v, start, append(kvs, name, typ, mac)...)
			}

			if v := st.Authorized; v.OK {
				authorized := 0.0
				if v.V {
					authorized = 1
				}
				known(StationAuthorized, authorized, time.Time{})
			}
			gauge(StationSignal, float64(st.Signal))
			gauge(StationSignalAverage, float64(st.SignalAverage))
			for _, c := range []struct {
				d      *Desc
				chains map[int]int
			}{{StationChainSignal, st.ChainSignal}, {StationChainSignalAverage, st.ChainSignalAverage}} {
				for _, i := range slices.Sorted(maps.Keys(c.chains)) {
					known(c.d, float64(c.chains[i]), time.Time{}, attribute.Int(AttrChain, i))
				}
			}
			for _, sig := range []struct {
				d *Desc
				v nl80211.Value[int]
			}{{StationAckSignal, st.AckSignal}, {StationAckSignalAverage, st.AckSignalAverage}, {StationBeaconSignalAverage, st.BeaconSignalAverage}} {
				if sig.v.OK {
					known(sig.d, float64(sig.v.V), time.Time{})
				}
			}
			gauge(StationBitrate, float64(st.ReceiveBitrate), rx)
			gauge(StationBitrate, float64(st.TransmitBitrate), tx)
			for _, r := range []struct {
				dir  attribute.KeyValue
				rate nl80211.Rate
			}{{rx, st.ReceiveRate}, {tx, st.TransmitRate}} {
				if r.rate.Mode == "" {
					continue
				}
				mode := attribute.String(AttrRateMode, r.rate.Mode)
				if r.rate.MCS >= 0 {
					known(StationMCS, float64(r.rate.MCS), time.Time{}, r.dir, mode)
				}
				if r.rate.Streams > 0 {
					known(StationSpatialStreams, float64(r.rate.Streams), time.Time{}, r.dir, mode)
				}
				if r.rate.Width > 0 {
					known(StationRateWidth, float64(r.rate.Width), time.Time{}, r.dir, mode)
				}
			}
			if v := st.ExpectedThroughput; v.OK {
				known(StationExpectedThroughput, float64(v.V), time.Time{})
			}
			counter(StationIO, st.ReceivedBytes, rx)
			counter(StationIO, st.TransmittedBytes, tx)
			counter(StationPackets, st.ReceivedPackets, rx)
			counter(StationPackets, st.TransmittedPackets, tx)
			if v := st.ReceiveAirtime; v.OK {
				known(StationAirtime, v.V.Seconds(), start, rx)
			}
			if v := st.TransmitAirtime; v.OK {
				known(StationAirtime, v.V.Seconds(), start, tx)
			}
			counter(StationTransmitRetries, st.TransmitRetries)
			counter(StationTransmitFailures, st.TransmitFailed)
			for _, c := range []struct {
				d *Desc
				v nl80211.Value[int]
			}{{StationReceiveDropped, st.ReceiveDropped}, {StationReceivedMPDUs, st.ReceivedMPDUs}, {StationFCSErrors, st.FCSErrors}, {StationBeacons, st.BeaconsReceived}} {
				if c.v.OK {
					known(c.d, float64(c.v.V), start)
				}
			}
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
