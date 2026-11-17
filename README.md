# nl80211-exporter

Reads the state of WiFi interfaces from Linux over [nl80211](https://wireless.docs.kernel.org/en/latest/en/developers/documentation/nl80211.html), the interface `iw`, `hostapd`, and `wpa_supplicant` use, and reports it as [OpenTelemetry](https://opentelemetry.io/) metrics:
how strong and how fast the link to each station is, and how busy the channel of each radio is.

On an access point, the stations are its clients; on a station interface, such as a router's uplink to another network, the station is the access point it is connected to.

## Metrics

| Metric                           | Type    | Unit        | Attributes  | Description                                                                                        |
| -------------------------------- | ------- | ----------- | ----------- | -------------------------------------------------------------------------------------------------- |
| `wifi.interface.frequency`       | gauge   | `MHz`       | interface   | Center frequency of the primary channel an interface is on.                                        |
| `wifi.interface.channel_width`   | gauge   | `MHz`       | interface   | Width of the channel an interface is on.                                                           |
| `wifi.station.count`             | gauge   | `{station}` | interface   | Stations of an interface: the clients of an access point, or the access point a station is connected to. |
| `wifi.station.signal`            | gauge   | `dBm`       | station     | Signal strength of the last frame received from a station.                                         |
| `wifi.station.signal_average`    | gauge   | `dBm`       | station     | Average signal strength of the frames received from a station, as the driver averages it.          |
| `wifi.station.bitrate`           | gauge   | `bit/s`     | station, `network.io.direction` | Rate of the last unicast frame received from a station (`receive`), or of the frames sent to it (`transmit`). |
| `wifi.station.io`                | counter | `By`        | station, `network.io.direction` | Bytes received from or sent to a station since it connected.                   |
| `wifi.station.packets`           | counter | `{packet}`  | station, `network.io.direction` | Packets received from or sent to a station since it connected.                 |
| `wifi.station.transmit_retries`  | counter | `{retry}`   | station     | Retries of frames sent to a station.                                                               |
| `wifi.station.transmit_failures` | counter | `{failure}` | station     | Frames sent to a station that were never acknowledged, after all retries.                          |
| `wifi.station.beacon_losses`     | counter | `{loss}`    | station     | Times a station interface lost the beacons of its access point. Only station interfaces count them. |
| `wifi.station.connected_time`    | gauge   | `s`         | station     | Time since a station connected.                                                                    |
| `wifi.station.inactive_time`     | gauge   | `s`         | station     | Time since the last activity of a station.                                                         |
| `wifi.channel.noise`             | gauge   | `dBm`       | channel     | Noise floor of the channel a radio is on.                                                          |
| `wifi.channel.active_time`       | counter | `s`         | channel     | Time a radio was on, on the channel.                                                               |
| `wifi.channel.busy_time`         | counter | `s`         | channel     | Time a radio sensed the channel busy, by any transmission or energy, its own included.             |
| `wifi.channel.receive_time`      | counter | `s`         | channel     | Time a radio spent receiving on the channel, from any BSS.                                         |
| `wifi.channel.bss_receive_time`  | counter | `s`         | channel     | Time a radio spent receiving frames of its own BSSes, e.g. its clients, on the channel.            |
| `wifi.channel.transmit_time`     | counter | `s`         | channel     | Time a radio spent transmitting on the channel.                                                    |

The channel times are cumulative, so what they tell is in their rates.
The share of time the channel was busy is the rate of `wifi.channel.busy_time` over that of `wifi.channel.active_time`,
and the share spent receiving other networks is the rate of `wifi.channel.receive_time` less `wifi.channel.bss_receive_time`, over the same.
A weakening link shows as a lower signal first, then lower bitrates and more retries per packet.

nl80211 leaves out what a driver does not report, and a value left out reads as zero.
So a zero signal, noise, bitrate, or connected time, and a zero channel time, are left out rather than reported:
no WiFi link is anywhere near 0 dBm.

### Attributes

- **interface**: `network.interface.name`, `wifi.interface.type`, and `wifi.phy.name`, the radio the interface runs on, e.g. `phy1`.
  `wifi.interface.type` is the nl80211 interface type in lowercase: `ap`, `station` (`managed` in `iw`), `mesh_point`, `monitor`, and so on.
- **station**: `network.interface.name`, `wifi.interface.type`, and `wifi.station.mac`, the MAC address of the station, as `iw` and `hostapd` print it (`02:00:00:00:01:00`).
  The station of a station interface is its access point, so its MAC is the BSSID: a new one means the interface roamed.
- **channel**: `wifi.phy.name`, and `wifi.channel.frequency`, the channel's center frequency in MHz as an int.
  It equals `wifi.interface.frequency` of the interfaces on the channel.

Every station is a series of its own.
An access point that sees many passing clients, like phones that randomize their MAC address, makes many series.

## How it reads nl80211

| Request                    | Asked                          | Used for          | Like                                  |
| -------------------------- | ------------------------------ | ----------------- | ------------------------------------- |
| `NL80211_CMD_GET_INTERFACE` | every read                    | `wifi.interface.*` | `iw dev`                             |
| `NL80211_CMD_GET_STATION`   | every read, per interface     | `wifi.station.*`   | `iw dev <interface> station dump`    |
| `NL80211_CMD_GET_SURVEY`    | every read, once per radio    | `wifi.channel.*`   | `iw dev <interface> survey dump`     |
| `NL80211_CMD_GET_WIPHY`     | once for each radio not seen yet | `wifi.phy.name` | `iw phy`                             |

Requests only read. Nothing is set, and no scan is started: a scan takes the radio off its channel.
None of them needs privileges, but nl80211 only shows the interfaces in the caller's network namespace.

A survey is the radio's, and the interfaces on one radio share it, so it is asked once per radio.
It lists every channel the radio has been on, e.g. while scanning; only the channel in use is reported.
Drivers that keep no survey report no `wifi.channel.*`.

Read through [`github.com/mdlayher/wifi`](https://github.com/mdlayher/wifi).
Tried on the MediaTek MT7981 (mt76) of an OpenWrt 25.12 router, with an access point on each band and a station on the 5 GHz radio, and on a USB adapter on a desktop.
