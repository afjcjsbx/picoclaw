---
description: "Hardware tools expose real devices to the agent. They are disabled by default and intended for boards and machines where PicoClaw has direct device access."
---
# Hardware tools

Hardware tools expose real devices to the agent. They are disabled by default and intended for boards and machines where PicoClaw has direct device access.

| Tool | What it does | Switch (default) | Platform |
|------|--------------|------------------|----------|
| `i2c` | Detect buses, scan, read, and write I2C devices | `tools.i2c.enabled` (`false`) | Linux |
| `spi` | List SPI devices and run transfers | `tools.spi.enabled` (`false`) | Linux |
| `serial` | List ports and read/write serial data | `tools.serial.enabled` (`false`) | Linux, macOS, Windows |

Environment variables follow the pattern `PICOCLAW_TOOLS_I2C_ENABLED`, `PICOCLAW_TOOLS_SPI_ENABLED`, `PICOCLAW_TOOLS_SERIAL_ENABLED`. See [Hardware compatibility](../guides/hardware-compatibility.md) for supported boards and wiring notes.

## Write safety

Enabling a hardware tool does **not** enable writes. Reads, discovery, and `detect`/`list` work, but every write action is denied until the operator opts in:

```json
{
  "tools": {
    "hardware": {
      "require_approval": true,
      "approval_timeout_seconds": 300,
      "max_writes_per_minute": 30,
      "dry_run": false
    },
    "i2c": {
      "enabled": true,
      "allow_write": true,
      "allow": ["1:0x76", "1:0x38"]
    }
  }
}
```

| Setting | Default | Effect |
|---------|---------|--------|
| `tools.<tool>.allow_write` | `false` | Must be `true` before `i2c write`, `spi transfer`, or `serial write` can run |
| `tools.<tool>.allow` | `[]` | Device allowlist. I2C entries are `bus:address` (`"1:0x76"`), SPI entries are `dev` (`"2.0"`), serial entries are port paths (`"/dev/ttyUSB0"`, a resolved `/dev/serial/by-id/...`, or `COM3`). An empty list allows reads on any valid device but never allows writes; a non-empty list restricts both reads and writes to the listed devices |
| `tools.hardware.require_approval` | `true` | Sends an approval prompt to the originating user before each write. A headless or automated sender has no interactive user, so the write is denied |
| `tools.hardware.approval_timeout_seconds` | `300` | How long the approval prompt waits before denying |
| `tools.hardware.max_writes_per_minute` | `30` | Per-tool sliding-window write limit. A negative value disables the limit |
| `tools.hardware.dry_run` | `false` | Validates the target and returns the exact bytes that would be sent without touching the bus |
| `tools.hardware.serial_session_ttl_seconds` | `60` | Idle timeout for persistent serial sessions |
| `tools.hardware.serial_max_sessions` | `2` | Maximum persistent serial sessions per agent conversation; the oldest is evicted |

The `confirm: true` argument is still accepted for compatibility, but it is not a security boundary: the model can set it itself. The real gate is `allow_write` plus the approval hook. If you deliberately run unattended, set `require_approval: false` only after constraining `allow` to the exact devices the automation needs; rate limiting remains active.

Environment overrides use `PICOCLAW_TOOLS_HARDWARE_` and the tool prefix, for example `PICOCLAW_TOOLS_HARDWARE_REQUIRE_APPROVAL=false`, `PICOCLAW_TOOLS_HARDWARE_MAX_WRITES_PER_MINUTE=5`, `PICOCLAW_TOOLS_I2C_ALLOW_WRITE=true`, and `PICOCLAW_TOOLS_I2C_ALLOW=1:0x76,1:0x38`.

## `i2c`

| Argument | Type | Required | Description |
|----------|------|----------|-------------|
| `action` | string | yes | `detect` (list buses), `scan` (find devices on a bus), `read`, or `write` |
| `bus` | string | scan/read/write | Bus number, for example `"1"` for `/dev/i2c-1` |
| `address` | integer | read/write | 7-bit device address (`0x03`–`0x77`), for example `56` for `0x38` |
| `register` | integer | no | Register address (`0`–`255`), for example `118` for `0x76`; when set, it is sent before the read/write |
| `command` | array | no | Raw command bytes to send before a read, for devices without register addressing (for example the AHT20's `[172, 51, 0]`). Conflicts with `register` |
| `data` | array | write | Bytes to write (`0`–`255` each) |
| `length` | integer | no | Number of bytes to read (`1`–`256`). Default: `1`. The historical name `count` is accepted as an alias |
| `delay_ms` | integer | no | Wait `0`–`60000` ms between the command/register write and the read, or after a write |
| `repeated_start` | bool | no | Keep command/register and read in one transaction using a repeated START. Ignored when `delay_ms > 0` |
| `confirm` | bool | write | Safety guard: must be `true` for write operations |

A register read without `delay_ms` uses two separate messages by default, which inserts a STOP between them. Set `repeated_start: true` for devices that require a combined transaction. Devices that need conversion time, such as the AHT20, should use `delay_ms`:

```text
i2c read  bus="1" address=56 command=[172,51,0] delay_ms=80 length=6
```

This sends the measurement command, waits 80 ms, and reads six bytes in one tool call.

## `spi`

| Argument | Type | Required | Description |
|----------|------|----------|-------------|
| `action` | string | yes | `list`, `transfer` (full-duplex send/receive), or `read` (receive by sending zeros) |
| `device` | string | transfer/read | SPI device identifier, for example `"2.0"` for `/dev/spidev2.0` |
| `speed` | integer | no | Clock speed in Hz. Default: `1000000` (1 MHz) |
| `mode` | integer | no | SPI mode `0`–`3` (CPOL/CPHA). Default: `0` |
| `bits` | integer | no | Bits per word. Only `8` is supported; other widths are rejected instead of being mis-encoded. Default: `8` |
| `data` | array | transfer | Bytes to send (`0`–`255` each). Required unless `segments` is used |
| `read_length` | integer | no | Bytes to receive for a transfer (`0`–`4096`). Omit for full-duplex (same as `data`); `0` means write-only |
| `delay_usecs` | integer | no | Delay after the transfer (`0`–`65535` µs) |
| `cs_change` | bool | no | Toggle chip select after the transfer |
| `segments` | array | no | Multi-segment transfer in one ioctl. Each item supports `data`, `read_length`, `delay_usecs`, and `cs_change`. Conflicts with top-level `data`/`delay_usecs`/`cs_change` |
| `length` | integer | read | Number of bytes to read (`1`–`4096`). The historical name `count` is accepted as an alias |
| `count` | integer | read | Deprecated alias for `length` |
| `confirm` | bool | transfer | Safety guard: must be `true` for transfers |

A multi-segment transfer is useful for devices that need a command and a response with chip select held low:

```text
spi transfer  device="2.0" speed=1000000 segments=[
  {"data":[3,0,0,0], "cs_change":false},
  {"read_length":4, "delay_usecs":100, "cs_change":true}
]
```

## `serial`

| Argument | Type | Required | Description |
|----------|------|----------|-------------|
| `action` | string | yes | `list`, `open` (persistent session), `read`, `write`, `close`, or `sessions` |
| `port` | string | read/write | Port path or name, for example `/dev/ttyUSB0`, `/dev/cu.usbserial-0001`, a stable `/dev/serial/by-id/...` or `/dev/serial/by-path/...` link, or `COM3` |
| `baud` | integer | no | Baud rate. Default: `115200`. Linux supports standard termios rates up to `4000000`; macOS also accepts arbitrary rates through `IOSSIOSPEED`; Windows accepts up to `4000000` |
| `data_bits` | integer | no | Data bits: `5`, `6`, `7`, or `8`. Default: `8` |
| `parity` | string | no | `none`, `even`, or `odd`. Default: `none` |
| `stop_bits` | integer | no | `1` or `2`. Default: `1` |
| `flow_control` | string | no | `none` (default), `rtscts` (hardware RTS/CTS), or `xonxoff` (software flow control) |
| `rs485` | bool | no | Enable Linux RS-485 mode through `TIOCSRS485`. Unsupported on macOS and Windows |
| `rs485_delay_before_ms` | integer | no | RTS delay before sending (`0`–`1000` ms). Requires `rs485: true` |
| `rs485_delay_after_ms` | integer | no | RTS delay after sending (`0`–`1000` ms). Requires `rs485: true` |
| `timeout_ms` | integer | no | Read/write timeout in milliseconds. Default: `1000` |
| `length` | integer | read | Number of bytes for a plain read (`1`–`4096`). Required unless `read_until` or `read_available` is set; in those modes it caps the buffer (default `4096`) |
| `read_until` | string | no | Read until a delimiter, included in the payload. Accepts `\n`, `\r\n`, `\t`, `\\`, and `\xNN` escapes. Cannot be combined with `read_available` |
| `read_available` | bool | no | Read whatever is available, stopping after 50 ms of idle time or at `timeout_ms` |
| `data` | array | write | Bytes to write (`0`–`255` each); required for write unless `text` is provided |
| `text` | string | write | UTF-8 text to write; required for write if `data` is omitted |
| `confirm` | bool | write | Safety guard: must be `true` for write operations |

For line-oriented devices, prefer a delimiter over an exact byte count:

```text
serial read  port="/dev/ttyUSB0" read_until="\r\n" timeout_ms=2000
serial read  port="/dev/ttyUSB0" read_available=true timeout_ms=500
```

`read_until` returns partial data with `matched: false` when the timeout expires. `read_available` stops after 50 ms with no new bytes, so it is useful for prompts and interactive consoles.

### Persistent sessions

Plain `read` and `write` open and close the port on every call. Some devices reset when the serial line is reopened (DTR toggling on Arduino/ESP32 boards) or need a continuous conversation (AT modems, GPS, boot consoles). Open a session to keep the port alive:

```text
serial open    port="/dev/serial/by-id/usb-FTDI_FT232R_USB_UART_A12345678-if00-port0" baud=115200
serial read    port="/dev/serial/by-id/usb-FTDI_FT232R_USB_UART_A12345678-if00-port0" read_until="\r\n"
serial write   port="/dev/serial/by-id/usb-FTDI_FT232R_USB_UART_A12345678-if00-port0" text="AT\r\n" confirm=true
serial sessions
serial close   port="/dev/serial/by-id/usb-FTDI_FT232R_USB_UART_A12345678-if00-port0"
```

Sessions are scoped to the agent conversation and close automatically after 60 seconds of inactivity, on `/clear`, when the agent stops, or when the oldest session is evicted beyond the two-session limit. The `open` settings must match later `read`/`write` calls; close and reopen to change baud or framing.

RS-485 direction control is Linux-only and driver-dependent. `rs485: true` asks the kernel to toggle RTS around each write; USB-RS485 adapters that switch direction in hardware usually work without it, while native UARTs may additionally need the right RTS polarity in the device tree. `rs485_delay_*` inserts the kernel's before/after-send delays.

All three tools return structured results (bus lists, scan tables, hex dumps) that the model can reason about directly. Write-style actions always require the explicit `confirm: true` argument; there is no configuration to disable that guard.
