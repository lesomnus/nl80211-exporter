package nl80211_test

import (
	"context"
	"errors"
	"net"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/lesomnus/nl80211-exporter/nl80211"
	"github.com/mdlayher/netlink"
	"github.com/mdlayher/wifi"
)

// fake answers like the kernel would, through *wifi.Client.
type fake struct {
	ifis     []*wifi.Interface
	stations map[string][]*wifi.StationInfo
	surveys  map[string][]*wifi.SurveyInfo
	phys     []*wifi.PHY

	ifisErr     error
	stationsErr map[string]error
	surveyErr   map[string]error
	physErr     error

	surveyCalls []string
	physCalls   int
	deadlines   []time.Time
	closed      bool
}

func (f *fake) Interfaces() ([]*wifi.Interface, error) { return f.ifis, f.ifisErr }

func (f *fake) StationInfo(ifi *wifi.Interface) ([]*wifi.StationInfo, error) {
	if err := f.stationsErr[ifi.Name]; err != nil {
		return nil, err
	}
	return f.stations[ifi.Name], nil
}

func (f *fake) SurveyInfo(ifi *wifi.Interface) ([]*wifi.SurveyInfo, error) {
	f.surveyCalls = append(f.surveyCalls, ifi.Name)
	if err := f.surveyErr[ifi.Name]; err != nil {
		return nil, err
	}
	return f.surveys[ifi.Name], nil
}

func (f *fake) PHYs() ([]*wifi.PHY, error) {
	f.physCalls++
	return f.phys, f.physErr
}

func (f *fake) SetDeadline(t time.Time) error {
	f.deadlines = append(f.deadlines, t)
	return nil
}

func (f *fake) Close() error {
	f.closed = true
	return nil
}

// errno fails the way a netlink request does.
func errno(e syscall.Errno) error {
	return &netlink.OpError{Op: "receive", Err: e}
}

func mac(last byte) net.HardwareAddr {
	return net.HardwareAddr{0x02, 0, 0, 0, 0, last}
}

// router is a dual-band access point whose 5 GHz radio is also a client of an upstream network.
func router() *fake {
	return &fake{
		ifis: []*wifi.Interface{
			{Index: 12, Name: "phy1-sta0", PHY: 1, Type: wifi.InterfaceTypeStation, Frequency: 5260, ChannelWidth: wifi.ChannelWidth80},
			{Index: 11, Name: "phy1-ap0", PHY: 1, Type: wifi.InterfaceTypeAP, Frequency: 5260, ChannelWidth: wifi.ChannelWidth80},
			{Index: 9, Name: "phy0-ap0", PHY: 0, Type: wifi.InterfaceTypeAP, Frequency: 2462, ChannelWidth: wifi.ChannelWidth20},
			{Name: "", PHY: 0, Type: wifi.InterfaceTypeP2PDevice},
		},
		stations: map[string][]*wifi.StationInfo{
			"phy1-sta0": {{
				HardwareAddr: mac(0xaa), Connected: 3 * time.Hour, Inactive: 330 * time.Millisecond,
				Signal: -50, SignalAverage: -49, ReceiveBitrate: 1_200_900_000, TransmitBitrate: 1_080_600_000,
				ReceivedBytes: 1 << 40, TransmittedBytes: 1 << 33, ReceivedPackets: 7, TransmittedPackets: 8,
				TransmitRetries: 8600, TransmitFailed: 41, BeaconLoss: 2,
			}},
			"phy1-ap0": {{HardwareAddr: mac(0x01), Connected: time.Minute, Signal: -66, SignalAverage: -66}},
			"phy0-ap0": {},
		},
		surveys: map[string][]*wifi.SurveyInfo{
			"phy1-sta0": {
				{Frequency: 5180, Noise: -95, ChannelTime: time.Second},
				{Frequency: 5260, Noise: -92, InUse: true, ChannelTime: 20295 * time.Second, ChannelTimeBusy: 1745 * time.Second, ChannelTimeRx: 1414 * time.Second, ChannelTimeBssRx: 72 * time.Second, ChannelTimeTx: 293 * time.Second},
			},
			"phy0-ap0": {
				{Frequency: 2412},
				{Frequency: 2462, Noise: -74, InUse: true, ChannelTime: 20291 * time.Second, ChannelTimeBusy: 2654 * time.Second},
			},
		},
		phys: []*wifi.PHY{{Index: 0, Name: "phy0"}, {Index: 1, Name: "phy1"}},
	}
}

func read(t *testing.T, f *fake) *nl80211.Snapshot {
	t.Helper()
	s, err := nl80211.NewReader(f).Read(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestRead(t *testing.T) {
	f := router()
	s := read(t, f)

	if len(s.Interfaces) != 3 {
		t.Fatalf("want the 3 named interfaces, got %+v", s.Interfaces)
	}
	sta, ap, ap2 := s.Interfaces[0], s.Interfaces[1], s.Interfaces[2]
	if sta.Name != "phy1-sta0" || sta.Type != nl80211.TypeStation || sta.PHY != "phy1" || sta.Frequency != 5260 || sta.ChannelWidth != 80 {
		t.Errorf("station interface: %+v", sta)
	}
	if ap.Type != nl80211.TypeAP || ap2.PHY != "phy0" || ap2.ChannelWidth != 20 {
		t.Errorf("access points: %+v, %+v", ap, ap2)
	}
	if ap2.Stations == nil || len(ap2.Stations) != 0 {
		t.Errorf("an access point without clients lists none, not nil: %#v", ap2.Stations)
	}

	if len(sta.Stations) != 1 {
		t.Fatalf("stations: %+v", sta.Stations)
	}
	got := sta.Stations[0]
	want := nl80211.Station{
		MAC: mac(0xaa), Connected: 3 * time.Hour, Inactive: 330 * time.Millisecond,
		Signal: -50, SignalAverage: -49, ReceiveBitrate: 1_200_900_000, TransmitBitrate: 1_080_600_000,
		ReceivedBytes: 1 << 40, TransmittedBytes: 1 << 33, ReceivedPackets: 7, TransmittedPackets: 8,
		TransmitRetries: 8600, TransmitFailed: 41, BeaconLosses: 2,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("station:\n got %+v\nwant %+v", got, want)
	}

	// One survey per radio, of the channel in use.
	if strings.Join(f.surveyCalls, ",") != "phy1-sta0,phy0-ap0" {
		t.Errorf("surveys asked through %v", f.surveyCalls)
	}
	wantChannels := []nl80211.Channel{
		{PHY: "phy1", Frequency: 5260, Noise: -92, Active: 20295 * time.Second, Busy: 1745 * time.Second, Receive: 1414 * time.Second, BSSReceive: 72 * time.Second, Transmit: 293 * time.Second},
		{PHY: "phy0", Frequency: 2462, Noise: -74, Active: 20291 * time.Second, Busy: 2654 * time.Second},
	}
	if len(s.Channels) != len(wantChannels) {
		t.Fatalf("channels: %+v", s.Channels)
	}
	for i := range wantChannels {
		if s.Channels[i] != wantChannels[i] {
			t.Errorf("channel %d:\n got %+v\nwant %+v", i, s.Channels[i], wantChannels[i])
		}
	}
	if s.Time.IsZero() {
		t.Error("no time")
	}
}

func TestReadNotOnChannel(t *testing.T) {
	f := &fake{
		ifis:     []*wifi.Interface{{Index: 3, Name: "wlan0", PHY: 0, Type: wifi.InterfaceTypeStation}},
		stations: map[string][]*wifi.StationInfo{},
		phys:     []*wifi.PHY{{Index: 0, Name: "phy0"}},
	}
	s := read(t, f)
	if ifi := s.Interfaces[0]; ifi.Frequency != 0 || ifi.ChannelWidth != 0 || ifi.Stations == nil || len(ifi.Stations) != 0 {
		t.Errorf("a station that is not connected: %+v", ifi)
	}
	if len(f.surveyCalls) != 0 || len(s.Channels) != 0 {
		t.Errorf("no survey without a channel: %v, %+v", f.surveyCalls, s.Channels)
	}
}

func TestReadGone(t *testing.T) {
	f := router()
	f.stationsErr = map[string]error{"phy0-ap0": errno(syscall.ENODEV)}
	f.surveyErr = map[string]error{"phy1-sta0": errno(syscall.ENODEV)}
	s := read(t, f)

	for _, ifi := range s.Interfaces {
		if ifi.Name == "phy0-ap0" {
			t.Error("an interface that went away is reported")
		}
	}
	// The radio of the interface that went away is asked through its other interface.
	if strings.Join(f.surveyCalls, ",") != "phy1-sta0,phy1-ap0" {
		t.Errorf("surveys asked through %v", f.surveyCalls)
	}
}

func TestReadUnsupported(t *testing.T) {
	f := router()
	f.stationsErr = map[string]error{"phy1-ap0": errno(syscall.EOPNOTSUPP)}
	f.surveyErr = map[string]error{"phy1-sta0": errno(syscall.EOPNOTSUPP)}
	s := read(t, f)

	if ifi := s.Interfaces[1]; ifi.Name != "phy1-ap0" || ifi.Stations != nil || ifi.Frequency != 5260 {
		t.Errorf("a driver that lists no stations: %+v", ifi)
	}
	// A radio without a survey is not asked again through its other interface.
	if strings.Join(f.surveyCalls, ",") != "phy1-sta0,phy0-ap0" {
		t.Errorf("surveys asked through %v", f.surveyCalls)
	}
	if len(s.Channels) != 1 || s.Channels[0].PHY != "phy0" {
		t.Errorf("channels: %+v", s.Channels)
	}
}

func TestReadPartial(t *testing.T) {
	f := router()
	f.stationsErr = map[string]error{"phy1-ap0": errno(syscall.EIO)}
	f.surveyErr = map[string]error{"phy0-ap0": errno(syscall.EIO)}
	s, err := nl80211.NewReader(f).Read(t.Context())
	if err == nil || !strings.Contains(err.Error(), "phy1-ap0: stations") || !strings.Contains(err.Error(), "phy0: survey") {
		t.Errorf("error: %v", err)
	}
	if s == nil || len(s.Interfaces) != 2 || len(s.Channels) != 1 {
		t.Fatalf("want the rest: %+v", s)
	}
}

func TestReadFails(t *testing.T) {
	f := router()
	f.ifisErr = errno(syscall.EIO)
	s, err := nl80211.NewReader(f).Read(t.Context())
	if s != nil || !errors.Is(err, syscall.EIO) {
		t.Errorf("snapshot %+v, error %v", s, err)
	}
}

func TestReadDeadline(t *testing.T) {
	f := router()
	r := nl80211.NewReader(f)
	if _, err := r.Read(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(f.deadlines) != 0 {
		t.Errorf("a context without a deadline sets none: %v", f.deadlines)
	}

	deadline := time.Now().Add(time.Second)
	ctx, cancel := context.WithDeadline(t.Context(), deadline)
	defer cancel()
	if _, err := r.Read(ctx); err != nil {
		t.Fatal(err)
	}
	if len(f.deadlines) != 2 || !f.deadlines[0].Equal(deadline) || !f.deadlines[1].IsZero() {
		t.Errorf("want the deadline, then none: %v", f.deadlines)
	}

	if err := r.Close(); err != nil || !f.closed {
		t.Errorf("close: %v", err)
	}
}

func TestPHYNames(t *testing.T) {
	f := router()
	f.phys = []*wifi.PHY{{Index: 0, Name: "wl-2g"}}
	r := nl80211.NewReader(f)
	for range 2 {
		s, err := r.Read(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if s.Interfaces[0].PHY != "phy1" || s.Interfaces[2].PHY != "wl-2g" {
			t.Errorf("a radio nl80211 does not name is named as the kernel would: %+v", s.Interfaces)
		}
	}
	if f.physCalls != 1 {
		t.Errorf("names are asked once per radio not seen yet: %d calls", f.physCalls)
	}
}
