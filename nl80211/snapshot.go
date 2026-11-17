package nl80211

import (
	"net"
	"time"
)

// Snapshot is the state of the WiFi interfaces and of the channels their radios are on, at one time.
type Snapshot struct {
	Time       time.Time
	Interfaces []Interface
	// Channels in use, one per radio whose driver keeps a survey.
	Channels []Channel
}

// Interface is a WiFi network interface, e.g. an access point, or a station (client) connected to one.
type Interface struct {
	Name string
	Type Type
	// PHY is the radio (wiphy) the interface runs on, e.g. "phy1".
	PHY string
	// Channel the interface is on; zero when it is on none, e.g. a station that is not connected.
	Frequency    int // MHz, of the primary (control) channel
	ChannelWidth int // MHz
	// Stations of the interface; nil when its driver does not list them.
	Stations []Station
	InterfaceDetails
}

// Station is a peer of an interface, as nl80211 calls it:
// a client of an access point, or the access point a station interface is connected to.
type Station struct {
	MAC net.HardwareAddr
	// Time since the station connected; zero when the driver does not report it.
	Connected time.Duration
	// Time since the last activity of the station.
	Inactive time.Duration
	// Signal strength of the last frame received from the station, and its average, in dBm;
	// zero when not reported.
	Signal        int
	SignalAverage int
	// Rate of the last unicast frame received from the station, and of frames sent to it,
	// in bit/s; zero when unknown.
	ReceiveBitrate  int
	TransmitBitrate int
	// Counted since the station connected.
	ReceivedBytes      int
	TransmittedBytes   int
	ReceivedPackets    int
	TransmittedPackets int
	TransmitRetries    int
	TransmitFailed     int
	// Times the beacons of the access point were lost; only station interfaces count them.
	BeaconLosses int
	// How the last frames were received from and sent to the station.
	ReceiveRate  Rate
	TransmitRate Rate
	StationDetails
}

// Channel is what a radio measured on the channel it is on, from its survey.
type Channel struct {
	PHY       string
	Frequency int // MHz
	Noise     int // dBm; zero when not reported
	// Counted while the radio was on the channel; zero when not reported.
	Active     time.Duration // the radio was on
	Busy       time.Duration // the channel was sensed busy, by any transmission or energy
	Receive    time.Duration // the radio was receiving
	BSSReceive time.Duration // the radio was receiving frames of its own BSSes
	Transmit   time.Duration // the radio was transmitting
}

// Type is the mode of an interface: the nl80211 interface type (NL80211_IFTYPE_*) in lowercase.
type Type string

const (
	TypeUnspecified Type = "unspecified"
	TypeAdHoc       Type = "adhoc"
	TypeStation     Type = "station" // "managed" in iw
	TypeAP          Type = "ap"
	TypeAPVLAN      Type = "ap_vlan"
	TypeWDS         Type = "wds"
	TypeMonitor     Type = "monitor"
	TypeMeshPoint   Type = "mesh_point"
	TypeP2PClient   Type = "p2p_client"
	TypeP2PGO       Type = "p2p_go"
	TypeP2PDevice   Type = "p2p_device"
	TypeOCB         Type = "ocb"
	TypeNAN         Type = "nan"
	TypeUnknown     Type = "unknown" // a type newer than this package
)
