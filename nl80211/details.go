package nl80211

import (
	"encoding/binary"
	"net"
	"strings"
	"time"

	"github.com/mdlayher/genetlink"
	"github.com/mdlayher/netlink"
	"github.com/mdlayher/wifi"
	"golang.org/x/sys/unix"
)

// Value is a value nl80211 may leave out; OK reports whether the driver reported it.
type Value[T any] struct {
	V  T
	OK bool
}

func some[T any](v T) Value[T] {
	return Value[T]{V: v, OK: true}
}

// InterfaceDetails are what github.com/mdlayher/wifi does not read of an interface.
type InterfaceDetails struct {
	SSID            string
	TransmitPower   Value[float64] // dBm
	CenterFrequency Value[int]     // MHz, of the whole channel, which the primary channel is part of
}

// StationDetails are what github.com/mdlayher/wifi does not read of a station.
type StationDetails struct {
	// Time on air of the frames received from and sent to the station, retries included.
	ReceiveAirtime  Value[time.Duration]
	TransmitAirtime Value[time.Duration]
	// Signal of the acknowledgements the station sent back, last and average, in dBm.
	AckSignal        Value[int]
	AckSignalAverage Value[int]
	// Of the access point a station interface is connected to: the average signal of its beacons,
	// in dBm, and how many were received.
	BeaconSignalAverage Value[int]
	BeaconsReceived     Value[int]
	// Signal of the last frame and its average per receive chain (antenna), in dBm, by chain index.
	ChainSignal        map[int]int
	ChainSignalAverage map[int]int
	// Throughput rate control expects to reach with the station, in bit/s.
	ExpectedThroughput Value[int]
	// Frames from the station dropped for reasons other than the ones counted elsewhere.
	ReceiveDropped Value[int]
	// MPDUs received from the station, and those of them with a bad frame check sequence.
	ReceivedMPDUs Value[int]
	FCSErrors     Value[int]
	// Whether the station may send data, e.g. past its WPA handshake.
	Authorized Value[bool]
}

// Rate is how frames were last sent: the PHY mode, and for all but legacy rates the MCS index
// and spatial streams.
type Rate struct {
	Mode    string // legacy, ht, vht, he, or eht; empty when not reported
	MCS     int    // -1 when there is none
	Streams int    // 0 when unknown
	Width   int    // MHz; 0 when unknown
}

func rateOf(r wifi.RateInfo) Rate {
	if r.Bitrate == 0 {
		return Rate{MCS: -1} // no rate reported
	}
	rate := Rate{MCS: -1, Width: widthOf(r.ChannelWidth)}
	switch r.ModulationType {
	case wifi.RateModulationInfoTypeLegacy:
		rate.Mode, rate.Streams = "legacy", 1
	case wifi.RateModulationInfoTypeHT:
		rate.Mode = "ht"
	case wifi.RateModulationInfoTypeVHT:
		rate.Mode = "vht"
	case wifi.RateModulationInfoTypeHE:
		rate.Mode = "he"
	case wifi.RateModulationInfoTypeEHT:
		rate.Mode = "eht"
	}
	if m := r.Modulation; m != nil {
		rate.MCS = m.GetMCS()
		if n := m.GetNSS(); n > 0 {
			rate.Streams = n
		}
	}
	return rate
}

// raw asks nl80211 directly, for the attributes github.com/mdlayher/wifi does not read.
type raw struct {
	c      *genetlink.Conn
	family genetlink.Family
}

func dialRaw() (*raw, error) {
	c, err := genetlink.Dial(nil)
	if err != nil {
		return nil, err
	}
	family, err := c.GetFamily(unix.NL80211_GENL_NAME)
	if err != nil {
		_ = c.Close()
		return nil, err
	}
	return &raw{c: c, family: family}, nil
}

func (r *raw) dump(cmd uint8, encode func(ae *netlink.AttributeEncoder)) ([]genetlink.Message, error) {
	ae := netlink.NewAttributeEncoder()
	if encode != nil {
		encode(ae)
	}
	b, err := ae.Encode()
	if err != nil {
		return nil, err
	}
	return r.c.Execute(
		genetlink.Message{Header: genetlink.Header{Command: cmd, Version: r.family.Version}, Data: b},
		r.family.ID, netlink.Request|netlink.Dump,
	)
}

func (r *raw) interfaceDetails() (map[int]InterfaceDetails, error) {
	msgs, err := r.dump(unix.NL80211_CMD_GET_INTERFACE, nil)
	if err != nil {
		return nil, err
	}
	return parseInterfaceDetails(msgs)
}

func (r *raw) stationDetails(ifindex int) (map[string]StationDetails, error) {
	msgs, err := r.dump(unix.NL80211_CMD_GET_STATION, func(ae *netlink.AttributeEncoder) {
		ae.Uint32(unix.NL80211_ATTR_IFINDEX, uint32(ifindex))
	})
	if err != nil {
		return nil, err
	}
	return parseStationDetails(msgs)
}

// parseInterfaceDetails reads a dump of NL80211_CMD_GET_INTERFACE, by interface index.
func parseInterfaceDetails(msgs []genetlink.Message) (map[int]InterfaceDetails, error) {
	out := map[int]InterfaceDetails{}
	for _, m := range msgs {
		ad, err := netlink.NewAttributeDecoder(m.Data)
		if err != nil {
			return nil, err
		}
		var (
			index int
			d     InterfaceDetails
		)
		for ad.Next() {
			b := ad.Bytes()
			switch ad.Type() {
			case unix.NL80211_ATTR_IFINDEX:
				if v, ok := unsigned(b); ok {
					index = int(v)
				}
			case unix.NL80211_ATTR_SSID:
				d.SSID = strings.ToValidUTF8(string(b), "�")
			case unix.NL80211_ATTR_WIPHY_TX_POWER_LEVEL:
				if v, ok := unsigned(b); ok {
					d.TransmitPower = some(float64(int32(v)) / 100) // mBm
				}
			case unix.NL80211_ATTR_CENTER_FREQ1:
				if v, ok := unsigned(b); ok {
					d.CenterFrequency = some(int(v))
				}
			}
		}
		if err := ad.Err(); err != nil {
			return nil, err
		}
		if index != 0 {
			out[index] = d
		}
	}
	return out, nil
}

// parseStationDetails reads a dump of NL80211_CMD_GET_STATION, by station MAC address.
func parseStationDetails(msgs []genetlink.Message) (map[string]StationDetails, error) {
	out := map[string]StationDetails{}
	for _, m := range msgs {
		attrs, err := netlink.UnmarshalAttributes(m.Data)
		if err != nil {
			return nil, err
		}
		var (
			mac net.HardwareAddr
			d   StationDetails
		)
		for _, a := range attrs {
			switch a.Type &^ (netlink.Nested | netlink.NetByteOrder) {
			case unix.NL80211_ATTR_MAC:
				mac = net.HardwareAddr(a.Data)
			case unix.NL80211_ATTR_STA_INFO:
				if err := d.parse(a.Data); err != nil {
					return nil, err
				}
			}
		}
		if mac != nil {
			out[mac.String()] = d
		}
	}
	return out, nil
}

func (d *StationDetails) parse(b []byte) error {
	attrs, err := netlink.UnmarshalAttributes(b)
	if err != nil {
		return err
	}
	for _, a := range attrs {
		v, ok := unsigned(a.Data)
		switch a.Type &^ (netlink.Nested | netlink.NetByteOrder) {
		case unix.NL80211_STA_INFO_TX_DURATION:
			if ok {
				d.TransmitAirtime = some(time.Duration(v) * time.Microsecond)
			}
		case unix.NL80211_STA_INFO_RX_DURATION:
			if ok {
				d.ReceiveAirtime = some(time.Duration(v) * time.Microsecond)
			}
		case unix.NL80211_STA_INFO_ACK_SIGNAL:
			if s, ok := signal(a.Data); ok {
				d.AckSignal = some(s)
			}
		case unix.NL80211_STA_INFO_ACK_SIGNAL_AVG:
			if s, ok := signal(a.Data); ok {
				d.AckSignalAverage = some(s)
			}
		case unix.NL80211_STA_INFO_BEACON_SIGNAL_AVG:
			if s, ok := signal(a.Data); ok {
				d.BeaconSignalAverage = some(s)
			}
		case unix.NL80211_STA_INFO_BEACON_RX:
			if ok {
				d.BeaconsReceived = some(int(v))
			}
		case unix.NL80211_STA_INFO_CHAIN_SIGNAL:
			if c, err := chains(a.Data); err == nil {
				d.ChainSignal = c
			}
		case unix.NL80211_STA_INFO_CHAIN_SIGNAL_AVG:
			if c, err := chains(a.Data); err == nil {
				d.ChainSignalAverage = c
			}
		case unix.NL80211_STA_INFO_EXPECTED_THROUGHPUT:
			if ok {
				d.ExpectedThroughput = some(int(v) * 1000) // kbit/s
			}
		case unix.NL80211_STA_INFO_RX_DROP_MISC:
			if ok {
				d.ReceiveDropped = some(int(v))
			}
		case unix.NL80211_STA_INFO_RX_MPDUS:
			if ok {
				d.ReceivedMPDUs = some(int(v))
			}
		case unix.NL80211_STA_INFO_FCS_ERROR_COUNT:
			if ok {
				d.FCSErrors = some(int(v))
			}
		case unix.NL80211_STA_INFO_STA_FLAGS:
			// struct nl80211_sta_flag_update: the flags reported, then the ones set.
			if len(a.Data) >= 8 {
				mask := binary.NativeEndian.Uint32(a.Data[0:4])
				set := binary.NativeEndian.Uint32(a.Data[4:8])
				bit := uint32(1) << unix.NL80211_STA_FLAG_AUTHORIZED
				if mask&bit != 0 {
					d.Authorized = some(set&bit != 0)
				}
			}
		}
	}
	return nil
}

// chains reads a per-chain signal: one u8 (dBm, signed) per chain, typed by the chain index.
func chains(b []byte) (map[int]int, error) {
	attrs, err := netlink.UnmarshalAttributes(b)
	if err != nil {
		return nil, err
	}
	out := map[int]int{}
	for _, a := range attrs {
		if s, ok := signal(a.Data); ok {
			out[int(a.Type&^(netlink.Nested|netlink.NetByteOrder))] = s
		}
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// unsigned reads a native-endian unsigned integer of any of the widths nl80211 uses.
func unsigned(b []byte) (uint64, bool) {
	switch len(b) {
	case 1:
		return uint64(b[0]), true
	case 2:
		return uint64(binary.NativeEndian.Uint16(b)), true
	case 4:
		return uint64(binary.NativeEndian.Uint32(b)), true
	case 8:
		return binary.NativeEndian.Uint64(b), true
	}
	return 0, false
}

// signal reads a signal strength, which nl80211 puts in a u8 holding a signed dBm.
func signal(b []byte) (int, bool) {
	if len(b) < 1 {
		return 0, false
	}
	return int(int8(b[0])), true
}
