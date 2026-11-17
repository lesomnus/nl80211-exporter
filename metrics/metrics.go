// Package metrics defines the metrics made from nl80211, once, for every exporter of this module.
package metrics

type Kind int

const (
	Gauge Kind = iota
	// Counter is monotonic and cumulative; it may reset when what it counts goes away.
	Counter
)

type Desc struct {
	Name        string
	Description string
	Unit        string
	Kind        Kind
	// Float values; integers otherwise.
	Float bool
}

var (
	InterfaceFrequency = &Desc{
		Name:        "wifi.interface.frequency",
		Description: "Center frequency of the primary channel an interface is on.",
		Unit:        "MHz",
	}
	InterfaceChannelWidth = &Desc{
		Name:        "wifi.interface.channel_width",
		Description: "Width of the channel an interface is on.",
		Unit:        "MHz",
	}
	StationCount = &Desc{
		Name:        "wifi.station.count",
		Description: "Stations of an interface: the clients of an access point, or the access point a station is connected to.",
		Unit:        "{station}",
	}
	StationSignal = &Desc{
		Name:        "wifi.station.signal",
		Description: "Signal strength of the last frame received from a station.",
		Unit:        "dBm",
	}
	StationSignalAverage = &Desc{
		Name:        "wifi.station.signal_average",
		Description: "Average signal strength of the frames received from a station, as the driver averages it.",
		Unit:        "dBm",
	}
	StationBitrate = &Desc{
		Name:        "wifi.station.bitrate",
		Description: "Rate of the last unicast frame received from a station, or of the frames sent to it.",
		Unit:        "bit/s",
	}
	StationIO = &Desc{
		Name:        "wifi.station.io",
		Description: "Bytes received from or sent to a station since it connected.",
		Unit:        "By",
		Kind:        Counter,
	}
	StationPackets = &Desc{
		Name:        "wifi.station.packets",
		Description: "Packets received from or sent to a station since it connected.",
		Unit:        "{packet}",
		Kind:        Counter,
	}
	StationTransmitRetries = &Desc{
		Name:        "wifi.station.transmit_retries",
		Description: "Retries of frames sent to a station.",
		Unit:        "{retry}",
		Kind:        Counter,
	}
	StationTransmitFailures = &Desc{
		Name:        "wifi.station.transmit_failures",
		Description: "Frames sent to a station that were never acknowledged, after all retries.",
		Unit:        "{failure}",
		Kind:        Counter,
	}
	StationBeaconLosses = &Desc{
		Name:        "wifi.station.beacon_losses",
		Description: "Times a station interface lost the beacons of its access point.",
		Unit:        "{loss}",
		Kind:        Counter,
	}
	StationConnectedTime = &Desc{
		Name:        "wifi.station.connected_time",
		Description: "Time since a station connected.",
		Unit:        "s",
	}
	StationInactiveTime = &Desc{
		Name:        "wifi.station.inactive_time",
		Description: "Time since the last activity of a station.",
		Unit:        "s",
		Float:       true,
	}
	ChannelNoise = &Desc{
		Name:        "wifi.channel.noise",
		Description: "Noise floor of the channel a radio is on.",
		Unit:        "dBm",
	}
	ChannelActiveTime = &Desc{
		Name:        "wifi.channel.active_time",
		Description: "Time a radio was on, on the channel.",
		Unit:        "s",
		Kind:        Counter,
		Float:       true,
	}
	ChannelBusyTime = &Desc{
		Name:        "wifi.channel.busy_time",
		Description: "Time a radio sensed the channel busy, by any transmission or energy, its own included.",
		Unit:        "s",
		Kind:        Counter,
		Float:       true,
	}
	ChannelReceiveTime = &Desc{
		Name:        "wifi.channel.receive_time",
		Description: "Time a radio spent receiving on the channel, from any BSS.",
		Unit:        "s",
		Kind:        Counter,
		Float:       true,
	}
	ChannelBSSReceiveTime = &Desc{
		Name:        "wifi.channel.bss_receive_time",
		Description: "Time a radio spent receiving frames of its own BSSes on the channel.",
		Unit:        "s",
		Kind:        Counter,
		Float:       true,
	}
	ChannelTransmitTime = &Desc{
		Name:        "wifi.channel.transmit_time",
		Description: "Time a radio spent transmitting on the channel.",
		Unit:        "s",
		Kind:        Counter,
		Float:       true,
	}
)

// All lists every metric, in the order they are reported.
var All = []*Desc{
	InterfaceFrequency,
	InterfaceChannelWidth,
	StationCount,
	StationSignal,
	StationSignalAverage,
	StationBitrate,
	StationIO,
	StationPackets,
	StationTransmitRetries,
	StationTransmitFailures,
	StationBeaconLosses,
	StationConnectedTime,
	StationInactiveTime,
	ChannelNoise,
	ChannelActiveTime,
	ChannelBusyTime,
	ChannelReceiveTime,
	ChannelBSSReceiveTime,
	ChannelTransmitTime,
}

// Attribute keys.
const (
	AttrInterfaceName    = "network.interface.name"
	AttrInterfaceType    = "wifi.interface.type" // nl80211.Type, e.g. ap or station
	AttrPHYName          = "wifi.phy.name"       // e.g. phy1
	AttrStationMAC       = "wifi.station.mac"    // e.g. 02:00:00:00:01:00
	AttrDirection        = "network.io.direction"
	AttrChannelFrequency = "wifi.channel.frequency" // MHz, as an int
)

// Values of AttrDirection, as seen from the interface.
const (
	DirectionReceive  = "receive"  // from the station
	DirectionTransmit = "transmit" // to the station
)
