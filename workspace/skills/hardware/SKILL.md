---
name: hardware
description: Read and control I2C, SPI, and serial peripherals on Sipeed boards (LicheeRV Nano, MaixCAM, NanoKVM) and other Linux hosts.
homepage: https://wiki.sipeed.com/hardware/en/lichee/RV_Nano/1_intro.html
metadata: {"nanobot":{"emoji":"🔧","requires":{"tools":["i2c","spi"]}}}
---

# Hardware (I2C / SPI / Serial)

Use the `i2c`, `spi`, and (when registered) `serial` tools to interact with sensors, displays, memory chips, and serial devices connected to the board. Serial support is optional because some builds enable only the bus tools.

## Quick Start

```text
# 1. Find available buses and devices
i2c detect
i2c scan  bus="1"
spi list

# 2. Read a register (BME280 chip ID at 0xD0)
i2c read  bus="1" address=118 register=208 length=1

# 3. Command-based sensor (AHT20: trigger, wait 80 ms, read 6 bytes)
i2c read  bus="1" address=56 command=[172,51,0] delay_ms=80 length=6

# 4. SPI full-duplex transfer (MCP3008 channel 0)
spi transfer  device="2.0" speed=1000000 mode=0 data=[1,128,0] confirm=true

# 5. Serial: list ports, then read a line from a USB adapter
serial list
serial read  port="/dev/serial/by-id/usb-FTDI_FT232R_USB_UART_A12345678-if00-port0" read_until="\r\n" timeout_ms=2000
```

## Write Safety

Hardware writes are **read-only by default**, even when the tool is enabled. A write succeeds only when all of these are true:

1. `tools.<tool>.allow_write` is `true`.
2. The target is listed in `tools.<tool>.allow` (I2C `bus:address`, SPI `dev`, serial port).
3. The user approves the write when `tools.hardware.require_approval` is enabled (default).

`confirm: true` is still required by the tool call, but it is not a security boundary: the model can set it. If a write is denied, do not try to bypass the gate. Tell the user which setting or allowlist entry is missing.

Use `tools.hardware.dry_run` to preview the exact bytes without touching the bus. In the default approval mode, CLI runs, cron jobs, and other non-interactive senders are denied unless the operator explicitly sets `require_approval: false`.

Examples of the configuration (operator-controlled, not model-generated):

```json
{
  "tools": {
    "hardware": {
      "require_approval": true,
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

## I2C

### Reading

```text
i2c read  bus="1" address=118 register=208 length=1
i2c read  bus="1" address=56 command=[172,51,0] delay_ms=80 length=6
i2c read  bus="1" address=118 register=247 length=8 repeated_start=true
```

- `register` sends one register byte before the read.
- `command` sends arbitrary bytes before the read (for devices without register addressing).
- `delay_ms` waits between the command/register write and the read. It forces two separate transactions, which is required by devices such as the AHT20.
- `repeated_start: true` keeps command/register and read in one `I2C_RDWR` transaction. It is ignored when `delay_ms > 0`.
- Without `repeated_start`, the kernel inserts a STOP between the register write and the read. Many simple sensors tolerate this; SMBus-style devices and some PMICs need the combined form.
- `length` and `count` are aliases; prefer `length`.

### Writing

```text
# BME280 forced measurement: write 0x25 to register 0xF4, wait 40 ms
i2c write  bus="1" address=118 register=244 data=[37] delay_ms=40 confirm=true

# SSD1306 init sequence (command prefix 0x00)
i2c write  bus="1" address=60 register=0 data=[174,213,128,168,63,211,0,64,141,20,32,0,161,200,218,18,129,207,217,241,219,64,164,166,175] confirm=true
```

- `register` is prepended to `data`.
- `delay_ms` waits after the write (for EEPROM write cycles, sensor conversions, or display resets).
- Maximum payload: 256 bytes per transaction.
- EEPROM writes: use the page size from `references/common-devices.md`; writes crossing a page boundary wrap. Wait at least 5 ms after a page write.

### Scanning

`i2c detect` lists `/dev/i2c-*` buses. `i2c scan bus="1"` probes 0x08–0x77. If an allowlist is configured, only buses that appear in it can be scanned. Scanning uses SMBus quick/read-byte probes and can disturb some devices; do not scan a bus that is in active use by a kernel driver.

## SPI

`spi read` receives bytes by sending zeros. `spi transfer` does a full-duplex transfer and requires `confirm: true`.

```text
spi list
spi read      device="2.0" speed=1000000 mode=0 length=4
spi transfer  device="2.0" speed=1000000 mode=0 data=[159,0,0,0] confirm=true
```

### Multi-segment transfers

Use `segments` when a device needs a command and a response in one chip-select assertion:

```text
spi transfer  device="2.0" speed=1000000 segments=[
  {"data":[3,0,0,0]},
  {"read_length":16, "delay_usecs":10, "cs_change":true}
] confirm=true
```

Each segment supports `data`, `read_length` (omit for full-duplex, `0` for write-only), `delay_usecs`, and `cs_change`.

Limits and compatibility:

- Only 8-bit words are supported. `bits` values other than 8 are rejected.
- Maximum transfer: 4096 bytes per segment.
- `mode` is 0–3 (CPOL/CPHA).
- `length` and `count` are aliases for the `read` action.
- Multi-segment transfers are Linux-only, like the rest of the SPI tool.

## Serial

### Port names and discovery

`serial list` enumerates host ports. Prefer stable identifiers over raw device names:

- `/dev/serial/by-id/...` or `/dev/serial/by-path/...` on Linux
- `/dev/cu.*` on macOS
- `COM3` on Windows

The tool resolves Linux by-id/by-path symlinks and verifies that the target is a real serial device. A dead link (adapter unplugged) is rejected.

### Stateless reads and writes

```text
serial read   port="/dev/ttyUSB0" length=16 timeout_ms=1000
serial read   port="/dev/ttyUSB0" read_until="\r\n" timeout_ms=2000
serial read   port="/dev/ttyUSB0" read_available=true timeout_ms=500
serial write  port="/dev/ttyUSB0" text="AT\r\n" confirm=true
```

- `read_until` includes the delimiter in the payload and returns `matched: false` if the timeout expires first.
- `read_available` returns whatever is currently buffered and stops after 50 ms of idle time.
- `length` is required for a plain read and caps the buffer for the other modes.
- `flow_control` accepts `none`, `rtscts`, or `xonxoff`.
- `rs485: true` enables Linux `TIOCSRS485`; `rs485_delay_before_ms` and `rs485_delay_after_ms` configure the RTS delays. macOS and Windows reject this option.
- Baud rates: Linux standard termios rates up to 4 Mbaud; macOS standard rates plus arbitrary rates through `IOSSIOSPEED`; Windows up to 4 Mbaud.

### Persistent sessions

Opening the port for every call can reset Arduino/ESP32 boards through DTR and loses buffered bytes. Keep a session open for AT modems, GPS receivers, and boot consoles:

```text
serial open    port="/dev/serial/by-id/usb-FTDI_FT232R_USB_UART_A12345678-if00-port0" baud=115200
serial read    port="/dev/serial/by-id/usb-FTDI_FT232R_USB_UART_A12345678-if00-port0" read_until="\r\n"
serial write   port="/dev/serial/by-id/usb-FTDI_FT232R_USB_UART_A12345678-if00-port0" text="AT\r\n" confirm=true
serial sessions
serial close   port="/dev/serial/by-id/usb-FTDI_FT232R_USB_UART_A12345678-if00-port0"
```

- Sessions are scoped to the agent conversation.
- Sessions expire after 60 seconds of inactivity by default (`tools.hardware.serial_session_ttl_seconds`).
- At most two sessions are kept per conversation (`tools.hardware.serial_max_sessions`); the oldest is evicted.
- `/clear` and agent shutdown close the sessions.
- Later `read`/`write` calls must use the same port settings. Close and reopen to change baud, framing, flow control, or RS-485 mode.

## Before You Start — Pinmux Setup

Most I2C/SPI pins on Sipeed boards are shared with WiFi. You must configure pinmux before use.

See `references/board-pinout.md` for board-specific commands.

**Common steps:**

1. Stop WiFi if using shared pins: `/etc/init.d/S30wifi stop`
2. Load the i2c-dev module: `modprobe i2c-dev`
3. Configure pinmux with `devmem` (board-specific; this is a shell command, not an I2C/SPI tool call, and it requires the `exec` tool and user approval)
4. Verify with `i2c detect` and `i2c scan`

## Limits and Safety

- I2C addresses: 7-bit range 0x03–0x77.
- I2C payload: 256 bytes per transaction.
- SPI payload: 4096 bytes per segment; 8-bit words only.
- Serial payload: 4096 bytes per read/write.
- Write-style calls require `confirm: true` and the configuration/approval gates described above.
- Never write to an unknown address or pin. Confirm the device, register, and payload from `references/common-devices.md` or the datasheet.

## Common Devices

See `references/common-devices.md` for register maps and ready-to-use command sequences:
AHT20, BME280, SSD1306 OLED, MPU6050 IMU, DS3231 RTC, INA219 power monitor, PCA9685 PWM, AT24C256 EEPROM, MCP3008 ADC, and W25Q128 flash.

## Troubleshooting

| Problem | Solution |
|---------|----------|
| No I2C buses found | `modprobe i2c-dev`; check device tree and pinmux |
| Permission denied | Run as root or add the user to the `i2c`, `spi`, or `dialout` group |
| No devices on scan | Check wiring, pull-up resistors (4.7k typical), and pinmux |
| Bus number changed | I2C adapter numbers can shift between boots; use `i2c detect` |
| WiFi stopped working | I2C-1/SPI-2 share pins with WiFi SDIO; they cannot be used simultaneously |
| `devmem` not found | Use `busybox devmem` or install it; the `exec` tool must be enabled |
| SPI transfer returns all zeros | Check MISO wiring and device power |
| SPI transfer returns all 0xFF | Device not responding; check CS pin, mode, and clock polarity |
| `I2C_RDWR failed` | Adapter lacks combined transactions; retry without `repeated_start` and use separate writes plus `delay_ms` |
| `bits other than 8 are not supported` | The SPI tool is byte-oriented; only 8-bit words are supported |
| `writes are disabled` | Set `tools.<tool>.allow_write=true` and list the target in `tools.<tool>.allow` |
| `device ... is not in tools.<tool>.allow` | Add the exact I2C `bus:address`, SPI `dev`, or serial port to the allowlist |
| `Approval required for a hardware write` | Ask the user to approve; headless runs need `require_approval: false` explicitly |
| `serial session ... different settings` | Close the session and reopen it with the new settings |
| `read_until` returned `matched: false` | Increase `timeout_ms`, verify the delimiter, or use `read_available` |
| by-id link does not resolve | The adapter is unplugged or udev has not created the link; use `serial list` |
| RS-485 option rejected | RS-485 is Linux-only and needs a compatible UART/transceiver |
