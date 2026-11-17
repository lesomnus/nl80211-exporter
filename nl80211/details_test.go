package nl80211

import (
	"encoding/binary"
	"net"
	"reflect"
	"testing"
	"time"

	"github.com/mdlayher/genetlink"
	"github.com/mdlayher/netlink"
	"github.com/mdlayher/wifi"
	"golang.org/x/sys/unix"
)

func message(t *testing.T, fn func(ae *netlink.AttributeEncoder)) genetlink.Message {
	t.Helper()
	ae := netlink.NewAttributeEncoder()
	fn(ae)
	b, err := ae.Encode()
	if err != nil {
		t.Fatal(err)
	}
	return genetlink.Message{Data: b}
}

func TestParseInterfaceDetails(t *testing.T) {
	msgs := []genetlink.Message{
		message(t, func(ae *netlink.AttributeEncoder) {
			ae.Uint32(unix.NL80211_ATTR_IFINDEX, 12)
			ae.Bytes(unix.NL80211_ATTR_SSID, []byte("upstream"))
			ae.Uint32(unix.NL80211_ATTR_WIPHY_TX_POWER_LEVEL, 2000)
			ae.Uint32(unix.NL80211_ATTR_CENTER_FREQ1, 5290)
		}),
		// An interface that is on no channel reports neither power nor center.
		message(t, func(ae *netlink.AttributeEncoder) {
			ae.Uint32(unix.NL80211_ATTR_IFINDEX, 13)
			ae.Bytes(unix.NL80211_ATTR_SSID, []byte{'a', 0xff})
		}),
	}
	got, err := parseInterfaceDetails(msgs)
	if err != nil {
		t.Fatal(err)
	}
	want := map[int]InterfaceDetails{
		12: {SSID: "upstream", TransmitPower: some(20.0), CenterFrequency: some(5290)},
		13: {SSID: "a�"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestParseStationDetails(t *testing.T) {
	flags := make([]byte, 8)
	binary.NativeEndian.PutUint32(flags[0:4], 1<<unix.NL80211_STA_FLAG_AUTHORIZED|1<<unix.NL80211_STA_FLAG_ASSOCIATED)
	binary.NativeEndian.PutUint32(flags[4:8], 1<<unix.NL80211_STA_FLAG_AUTHORIZED)
	s8 := func(v int8) uint8 { return uint8(v) }

	full := message(t, func(ae *netlink.AttributeEncoder) {
		ae.Uint32(unix.NL80211_ATTR_IFINDEX, 12)
		ae.Bytes(unix.NL80211_ATTR_MAC, net.HardwareAddr{2, 0, 0, 0, 0, 0xaa})
		ae.Nested(unix.NL80211_ATTR_STA_INFO, func(n *netlink.AttributeEncoder) error {
			n.Uint64(unix.NL80211_STA_INFO_TX_DURATION, 224_057_239)
			n.Uint64(unix.NL80211_STA_INFO_RX_DURATION, 161_740_972)
			n.Uint8(unix.NL80211_STA_INFO_ACK_SIGNAL, s8(-47))
			n.Uint8(unix.NL80211_STA_INFO_ACK_SIGNAL_AVG, s8(-48))
			n.Uint8(unix.NL80211_STA_INFO_BEACON_SIGNAL_AVG, s8(-50))
			n.Uint64(unix.NL80211_STA_INFO_BEACON_RX, 337_000)
			n.Nested(unix.NL80211_STA_INFO_CHAIN_SIGNAL, func(c *netlink.AttributeEncoder) error {
				c.Uint8(0, s8(-51))
				c.Uint8(1, s8(-57))
				return nil
			})
			n.Nested(unix.NL80211_STA_INFO_CHAIN_SIGNAL_AVG, func(c *netlink.AttributeEncoder) error {
				c.Uint8(1, s8(-56))
				return nil
			})
			n.Uint32(unix.NL80211_STA_INFO_EXPECTED_THROUGHPUT, 978_710)
			n.Uint64(unix.NL80211_STA_INFO_RX_DROP_MISC, 5)
			n.Uint32(unix.NL80211_STA_INFO_RX_MPDUS, 1000)
			n.Uint32(unix.NL80211_STA_INFO_FCS_ERROR_COUNT, 7)
			n.Bytes(unix.NL80211_STA_INFO_STA_FLAGS, flags)
			return nil
		})
	})
	// A driver that reports none of it.
	bare := message(t, func(ae *netlink.AttributeEncoder) {
		ae.Bytes(unix.NL80211_ATTR_MAC, net.HardwareAddr{2, 0, 0, 0, 0, 1})
		ae.Nested(unix.NL80211_ATTR_STA_INFO, func(n *netlink.AttributeEncoder) error {
			n.Uint32(unix.NL80211_STA_INFO_INACTIVE_TIME, 10)
			return nil
		})
	})
	got, err := parseStationDetails([]genetlink.Message{full, bare})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]StationDetails{
		"02:00:00:00:00:aa": {
			TransmitAirtime:     some(224_057_239 * time.Microsecond),
			ReceiveAirtime:      some(161_740_972 * time.Microsecond),
			AckSignal:           some(-47),
			AckSignalAverage:    some(-48),
			BeaconSignalAverage: some(-50),
			BeaconsReceived:     some(337_000),
			ChainSignal:         map[int]int{0: -51, 1: -57},
			ChainSignalAverage:  map[int]int{1: -56},
			ExpectedThroughput:  some(978_710_000),
			ReceiveDropped:      some(5),
			ReceivedMPDUs:       some(1000),
			FCSErrors:           some(7),
			Authorized:          some(true),
		},
		"02:00:00:00:00:01": {},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got  %+v\nwant %+v", got, want)
	}
}

func TestRateOf(t *testing.T) {
	for desc, tc := range map[string]struct {
		in   wifi.RateInfo
		want Rate
	}{
		"not reported": {wifi.RateInfo{}, Rate{MCS: -1}},
		"legacy":       {wifi.RateInfo{Bitrate: 54_000_000, ModulationType: wifi.RateModulationInfoTypeLegacy, ChannelWidth: wifi.ChannelWidth20NoHT}, Rate{Mode: "legacy", MCS: -1, Streams: 1, Width: 20}},
		"ht": {wifi.RateInfo{Bitrate: 130_000_000, ModulationType: wifi.RateModulationInfoTypeHT, ChannelWidth: wifi.ChannelWidth20,
			Modulation: wifi.HTModulationInfo{BaseModulationInfo: wifi.BaseModulationInfo{MCS: 7, NSS: 2}, HTMCS: 15}}, Rate{Mode: "ht", MCS: 7, Streams: 2, Width: 20}},
		"vht": {wifi.RateInfo{Bitrate: 585_000_000, ModulationType: wifi.RateModulationInfoTypeVHT, ChannelWidth: wifi.ChannelWidth80,
			Modulation: wifi.VHTModulationInfo{BaseModulationInfo: wifi.BaseModulationInfo{MCS: 7, NSS: 2}}}, Rate{Mode: "vht", MCS: 7, Streams: 2, Width: 80}},
		"he, streams unknown": {wifi.RateInfo{Bitrate: 1_200_900_000, ModulationType: wifi.RateModulationInfoTypeHE, ChannelWidth: wifi.ChannelWidth80,
			Modulation: wifi.HEModulationInfo{BaseModulationInfo: wifi.BaseModulationInfo{MCS: 11, NSS: -1}}}, Rate{Mode: "he", MCS: 11, Width: 80}},
	} {
		if got := rateOf(tc.in); got != tc.want {
			t.Errorf("%s: got %+v, want %+v", desc, got, tc.want)
		}
	}
}
