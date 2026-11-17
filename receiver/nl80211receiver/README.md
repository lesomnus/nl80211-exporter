# nl80211 receiver

An [OpenTelemetry Collector](https://github.com/open-telemetry/opentelemetry-collector) receiver that reads the state of WiFi interfaces from Linux over nl80211.
It produces the metrics in [Metrics](../../README.md#metrics).

| Status    |         |
| --------- | ------- |
| Stability | alpha   |
| Signals   | metrics |

## Build

Add it to an [ocb](https://github.com/open-telemetry/opentelemetry-collector/tree/main/cmd/builder) manifest:

```yaml
receivers:
  - gomod: github.com/lesomnus/nl80211-exporter/receiver/nl80211receiver v0.0.0-<date>-<commit>
```

Use a pseudo-version of a commit on `main`, e.g. from `go list -m github.com/lesomnus/nl80211-exporter/receiver/nl80211receiver@main`.
It needs Collector v0.163.0 or later, and adds less than 0.5 MiB to a `linux/arm64` build.

## Configuration

```yaml
receivers:
  nl80211:
    collection_interval: 15s
```

| Field                 | Default | Description                                  |
| --------------------- | ------- | -------------------------------------------- |
| `collection_interval` | `1m`    | How often to read.                           |
| `initial_delay`       | `1s`    | Delay before the first read.                 |
| `timeout`             | `0s`    | Timeout of each read; `0s` means no timeout. |

Where there is no nl80211, i.e. not on Linux or without a WiFi driver, the receiver logs that once and reports nothing,
so the same configuration can run on nodes without WiFi.
It asks again on every read, so a driver loaded later is picked up.

Counters are cumulative.
A station's counters start when the station connected, as nl80211 reports it, so they have their true start from the first read;
a station that connects again starts them over.
Channel counters start when the receiver does, or, for a series that appears or resets later, at the previous read.

## Running in a container

nl80211 only shows the interfaces in the caller's network namespace, so run the Collector in the host's, e.g. with `network_mode: host`.
It needs no capabilities and no mounts.
