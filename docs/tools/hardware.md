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
| `bits` | integer | no | Bits per word. Default: `8` |
| `data` | array | transfer | Bytes to send (`0`–`255` each) |
| `count` | integer | read | Number of bytes to read (`1`–`4096`) |
| `confirm` | bool | transfer | Safety guard: must be `true` for transfers |

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

All three tools return structured results (bus lists, scan tables, hex dumps) that the model can reason about directly. Write-style actions always require the explicit `confirm: true` argument; there is no configuration to disable that guard.
