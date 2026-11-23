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
| `action` | string | yes | `list`, `read`, or `write` |
| `port` | string | read/write | Port path or name, for example `/dev/ttyUSB0`, `/dev/cu.usbserial-0001`, or `COM3` |
| `baud` | integer | no | Baud rate. Default: `115200`. Linux/macOS support standard termios rates up to `230400`; Windows accepts up to `4000000` |
| `data_bits` | integer | no | Data bits: `5`, `6`, `7`, or `8`. Default: `8` |
| `parity` | string | no | `none`, `even`, or `odd`. Default: `none` |
| `stop_bits` | integer | no | `1` or `2`. Default: `1` |
| `flow_control` | string | no | `none` (default), `rtscts` (hardware RTS/CTS), or `xonxoff` (software flow control) |
| `timeout_ms` | integer | no | Read/write timeout in milliseconds. Default: `1000` |
| `length` | integer | read | Number of bytes to read (`1`–`4096`) |
| `data` | array | write | Bytes to write (`0`–`255` each); required for write unless `text` is provided |
| `text` | string | write | UTF-8 text to write; required for write if `data` is omitted |
| `confirm` | bool | write | Safety guard: must be `true` for write operations |

All three tools return structured results (bus lists, scan tables, hex dumps) that the model can reason about directly. Write-style actions always require the explicit `confirm: true` argument; there is no configuration to disable that guard.
