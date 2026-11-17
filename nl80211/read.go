// Package nl80211 reads the state of WiFi interfaces from Linux over nl80211,
// the netlink interface of cfg80211 that iw, hostapd, and wpa_supplicant use.
//
// It only asks: for the interfaces, their stations, the survey of each radio,
// and the names of the radios. It sets nothing and never starts a scan.
// None of it needs privileges, but nl80211 only shows the interfaces in the
// caller's network namespace.
package nl80211

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"runtime"
	"strconv"
	"syscall"
	"time"

	"github.com/mdlayher/wifi"
)

// ErrNotAvailable means there is no nl80211 to ask: the system is not Linux,
// or its kernel has no cfg80211, i.e. no WiFi driver.
var ErrNotAvailable = errors.New("nl80211 is not available")

// Client is the part of *wifi.Client a Reader uses, so tests can stand in for the kernel.
type Client interface {
	Interfaces() ([]*wifi.Interface, error)
	StationInfo(ifi *wifi.Interface) ([]*wifi.StationInfo, error)
	SurveyInfo(ifi *wifi.Interface) ([]*wifi.SurveyInfo, error)
	PHYs() ([]*wifi.PHY, error)
	SetDeadline(t time.Time) error
	Close() error
}

// Reader reads snapshots over one nl80211 connection.
// It is not safe for concurrent use.
type Reader struct {
	c    Client
	phys map[int]string // radio (wiphy) index to name
}

// Open connects to nl80211.
func Open() (*Reader, error) {
	c, err := wifi.New()
	if err != nil {
		if runtime.GOOS != "linux" || errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("%w: %w", ErrNotAvailable, err)
		}
		return nil, err
	}
	return NewReader(c), nil
}

// NewReader reads through c, which Close closes.
func NewReader(c Client) *Reader {
	return &Reader{c: c, phys: map[int]string{}}
}

func (r *Reader) Close() error {
	return r.c.Close()
}

// Read reads every WiFi interface that has a name, its stations,
// and the channel each radio is on.
//
// An interface that goes away while it is read is left out.
// If reading an interface or a radio fails otherwise, the snapshot has the
// rest and the error says what is missing. A nil snapshot means nothing could be read.
func (r *Reader) Read(ctx context.Context) (*Snapshot, error) {
	if deadline, ok := ctx.Deadline(); ok {
		if err := r.c.SetDeadline(deadline); err != nil {
			return nil, err
		}
		defer r.c.SetDeadline(time.Time{})
	}

	ifis, err := r.c.Interfaces()
	if err != nil {
		return nil, fmt.Errorf("interfaces: %w", err)
	}

	s := &Snapshot{Time: time.Now()}
	var errs []error
	surveyed := map[int]bool{}
	for _, ifi := range ifis {
		if ifi.Name == "" {
			// A device without a network interface, e.g. for P2P.
			continue
		}

		stations, err := r.c.StationInfo(ifi)
		listed := true
		switch {
		case err == nil:
		case gone(err):
			continue
		case errors.Is(err, errors.ErrUnsupported):
			listed = false // the driver lists no stations
		default:
			errs = append(errs, fmt.Errorf("%s: stations: %w", ifi.Name, err))
			continue
		}

		phy := r.phyName(ifi.PHY)
		x := Interface{
			Name: ifi.Name,
			Type: typeOf(ifi.Type),
			PHY:  phy,
		}
		if ifi.Frequency != 0 {
			x.Frequency = ifi.Frequency
			x.ChannelWidth = widthOf(ifi.ChannelWidth)
		}
		if listed {
			x.Stations = make([]Station, 0, len(stations))
			for _, sta := range stations {
				x.Stations = append(x.Stations, stationOf(sta))
			}
		}
		s.Interfaces = append(s.Interfaces, x)

		// A survey is the radio's, whichever of its interfaces asks.
		// Ask once per radio, through an interface that is on a channel.
		if ifi.Frequency == 0 || surveyed[ifi.PHY] {
			continue
		}
		surveys, err := r.c.SurveyInfo(ifi)
		switch {
		case err == nil:
		case gone(err):
			continue // another interface of the radio may answer
		case errors.Is(err, errors.ErrUnsupported):
			surveyed[ifi.PHY] = true // the driver keeps no survey
			continue
		default:
			surveyed[ifi.PHY] = true
			errs = append(errs, fmt.Errorf("%s: survey: %w", phy, err))
			continue
		}
		surveyed[ifi.PHY] = true
		for _, sv := range surveys {
			// A survey lists every channel the radio has visited, e.g. while scanning.
			if sv.InUse {
				s.Channels = append(s.Channels, channelOf(phy, sv))
			}
		}
	}
	return s, errors.Join(errs...)
}

// gone reports whether an interface went away while it was read.
func gone(err error) bool {
	return errors.Is(err, syscall.ENODEV) || errors.Is(err, fs.ErrNotExist)
}

// phyName names a radio as the kernel does, e.g. "phy1".
func (r *Reader) phyName(index int) string {
	if name, ok := r.phys[index]; ok {
		return name
	}
	// A radio not seen yet: learn the names of all of them.
	if phys, err := r.c.PHYs(); err == nil {
		for _, p := range phys {
			if p.Name != "" {
				r.phys[p.Index] = p.Name
			}
		}
	}
	if name, ok := r.phys[index]; ok {
		return name
	}
	// The kernel's own name for it, which only a rename changes.
	name := "phy" + strconv.Itoa(index)
	r.phys[index] = name
	return name
}

var types = map[wifi.InterfaceType]Type{
	wifi.InterfaceTypeUnspecified:   TypeUnspecified,
	wifi.InterfaceTypeAdHoc:         TypeAdHoc,
	wifi.InterfaceTypeStation:       TypeStation,
	wifi.InterfaceTypeAP:            TypeAP,
	wifi.InterfaceTypeAPVLAN:        TypeAPVLAN,
	wifi.InterfaceTypeWDS:           TypeWDS,
	wifi.InterfaceTypeMonitor:       TypeMonitor,
	wifi.InterfaceTypeMeshPoint:     TypeMeshPoint,
	wifi.InterfaceTypeP2PClient:     TypeP2PClient,
	wifi.InterfaceTypeP2PGroupOwner: TypeP2PGO,
	wifi.InterfaceTypeP2PDevice:     TypeP2PDevice,
	wifi.InterfaceTypeOCB:           TypeOCB,
	wifi.InterfaceTypeNAN:           TypeNAN,
}

func typeOf(t wifi.InterfaceType) Type {
	if v, ok := types[t]; ok {
		return v
	}
	return TypeUnknown
}

// widths are in MHz. 80+80 MHz is two segments of 80 MHz.
var widths = map[wifi.ChannelWidth]int{
	wifi.ChannelWidth20NoHT: 20,
	wifi.ChannelWidth20:     20,
	wifi.ChannelWidth40:     40,
	wifi.ChannelWidth80:     80,
	wifi.ChannelWidth80P80:  160,
	wifi.ChannelWidth160:    160,
	wifi.ChannelWidth5:      5,
	wifi.ChannelWidth10:     10,
	wifi.ChannelWidth1:      1,
	wifi.ChannelWidth2:      2,
	wifi.ChannelWidth4:      4,
	wifi.ChannelWidth8:      8,
	wifi.ChannelWidth16:     16,
	wifi.ChannelWidth320:    320,
}

func widthOf(w wifi.ChannelWidth) int {
	return widths[w]
}

func stationOf(s *wifi.StationInfo) Station {
	return Station{
		MAC:                s.HardwareAddr,
		Connected:          s.Connected,
		Inactive:           s.Inactive,
		Signal:             s.Signal,
		SignalAverage:      s.SignalAverage,
		ReceiveBitrate:     s.ReceiveBitrate,
		TransmitBitrate:    s.TransmitBitrate,
		ReceivedBytes:      s.ReceivedBytes,
		TransmittedBytes:   s.TransmittedBytes,
		ReceivedPackets:    s.ReceivedPackets,
		TransmittedPackets: s.TransmittedPackets,
		TransmitRetries:    s.TransmitRetries,
		TransmitFailed:     s.TransmitFailed,
		BeaconLosses:       s.BeaconLoss,
	}
}

func channelOf(phy string, s *wifi.SurveyInfo) Channel {
	return Channel{
		PHY:        phy,
		Frequency:  s.Frequency,
		Noise:      s.Noise,
		Active:     s.ChannelTime,
		Busy:       s.ChannelTimeBusy,
		Receive:    s.ChannelTimeRx,
		BSSReceive: s.ChannelTimeBssRx,
		Transmit:   s.ChannelTimeTx,
	}
}
