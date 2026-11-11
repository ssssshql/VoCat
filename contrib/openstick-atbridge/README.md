# openstick-atbridge

Small helper process that re-exports a WWAN AT character device as a PTY for
hosts whose driver only answers a blocking read.

## Why

On the UFI003 / OpenStick 410 (MSM8916, `bam-dmux`) the AT port
`/dev/wwan0at0` does not behave like a serial port:

* `poll()` never reports `POLLIN` for AT responses,
* a non-blocking `read()` returns `EAGAIN`,
* a `read()` that is **already blocked in the driver** receives the response.

Every consumer that polls and then reads — `socat`, `minicom`, the original
VoCat transport, plain `dd` — therefore sees a silent port. The only reliable
shape is:

```sh
cat /dev/wwan0at0 > /tmp/at.log &   # one blocking reader parked in the driver
printf 'AT\r\n' > /dev/wwan0at0     # a separate writer
```

This bridge formalises that shape: it keeps exactly one blocking read
outstanding on the device and forwards the data to a PTY master, so ordinary
serial consumers work unmodified.

> VoCat 0.3.42+ opens the bridged PTY automatically when it exists (see
> `internal/modem/at_bridge_linux.go`), so this helper is optional on builds
> that already contain that change. It is still useful for other tools.

## Build

```sh
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o atbridge .
```

## Run

```sh
/opt/atbridge/atbridge -dev /dev/wwan0at0 -link /dev/vocat-at
```

The PTY is created at `/dev/vocat-at` (a symlink to the allocated
`/dev/pts/N`). Point your AT consumer at `/dev/vocat-at` instead of
`/dev/wwan0at0`.

## systemd

```ini
[Unit]
Description=WWAN AT port bridge (blocking-read PTY for bam-dmux)
After=systemd-udevd.service
Before=vocat.service

[Service]
Type=simple
ExecStartPre=/bin/sh -c 'for i in $(seq 1 30); do [ -c /dev/wwan0at0 ] && exit 0; sleep 1; done; exit 1'
ExecStart=/opt/atbridge/atbridge -dev /dev/wwan0at0 -link /dev/vocat-at
Restart=always
RestartSec=3
User=root

[Install]
WantedBy=multi-user.target
```

Place the binary at `/opt/atbridge/atbridge`.

**Note for hosts where `systemctl start` blocks:** the OpenStick Debian image
runs an unusual init/sd-notify combination; `systemctl --no-block start <unit>`
returns immediately and the unit still comes up.

## Verified

UFI003 (MSM8916, `bam-dmux`), Debian 11, kernel 5.15:

* `ATI`, `AT+CSQ`, `AT+CFUN?` respond through `/dev/vocat-at`
* VoCat completes modem discovery without AT timeouts
* VoWiFi establishes the IKEv2 tunnel and receives an IMS address
* IMS SMS submission returns SIP `202` and the matching RP-ACK arrives
