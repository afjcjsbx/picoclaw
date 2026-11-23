# Board Pinout & Pinmux Reference

## LicheeRV Nano (SG2002)

### I2C Buses

| Bus | Pins | Notes |
|-----|------|-------|
| I2C-1 | P18 (SCL), P21 (SDA) | **Shared with WiFi SDIO** — must stop WiFi first |
| I2C-3 | Available on header | Check device tree for pin assignment |
| I2C-5 | Software (BitBang) | Slower but no pin conflicts |

### SPI Buses

| Bus | Pins | Notes |
|-----|------|-------|
| SPI-2 | P18 (CS), P21 (MISO), P22 (MOSI), P23 (SCK) | **Shared with WiFi** — must stop WiFi first |
| SPI-4 | Software (BitBang) | Slower but no pin conflicts |

### Setup Steps for I2C-1

```bash
# 1. Stop WiFi (shares pins with I2C-1)
/etc/init.d/S30wifi stop

# 2. Configure pinmux for I2C-1
devmem 0x030010D0 b 0x2   # P18 → I2C1_SCL
devmem 0x030010DC b 0x2   # P21 → I2C1_SDA

# 3. Load i2c-dev module
modprobe i2c-dev

# 4. Verify
ls /dev/i2c-*
```

### Setup Steps for SPI-2

```bash
# 1. Stop WiFi (shares pins with SPI-2)
/etc/init.d/S30wifi stop

# 2. Configure pinmux for SPI-2
devmem 0x030010D0 b 0x1   # P18 → SPI2_CS
devmem 0x030010DC b 0x1   # P21 → SPI2_MISO
devmem 0x030010E0 b 0x1   # P22 → SPI2_MOSI
devmem 0x030010E4 b 0x1   # P23 → SPI2_SCK

# 3. Verify
ls /dev/spidev*
```

### Max Tested SPI Speed
- SPI-2 hardware: tested up to **93 MHz**
- `spidev_test` is pre-installed on the official image for loopback testing

### Serial / UART

UART pins are usually exposed on the debug header or a dedicated connector. Check the board schematic before wiring: the debug console often owns `/dev/ttyS0` or `/dev/ttyAMA0`.

- SoC UARTs: `/dev/ttyS*`, `/dev/ttyAMA*`
- USB-UART adapters: `/dev/ttyUSB*`, `/dev/ttyACM*`
- Stable Linux links: `/dev/serial/by-id/...`, `/dev/serial/by-path/...`
- macOS USB adapters: `/dev/cu.usbserial-*`, `/dev/cu.usbmodem-*`

Use `serial list`, then prefer a `by-id` link over a raw device name. RS-485 requires a transceiver and is Linux-only (`rs485: true`); USB-RS485 adapters usually switch direction in hardware and may not need it.

---

## MaixCAM

### I2C Buses

| Bus | Pins | Notes |
|-----|------|-------|
| I2C-1 | Overlaps with WiFi | Not recommended |
| I2C-3 | Overlaps with WiFi | Not recommended |
| I2C-5 | A15 (SCL), A27 (SDA) | **Recommended** — software I2C, no conflicts |

### Setup Steps for I2C-5

```bash
# Configure pins using pinmap utility
# (MaixCAM uses a pinmap tool instead of devmem)
# Refer to: https://wiki.sipeed.com/hardware/en/maixcam/gpio.html

# Load i2c-dev
modprobe i2c-dev

# Verify
ls /dev/i2c-*
```

---

## MaixCAM2

### I2C Buses

| Bus | Pins | Notes |
|-----|------|-------|
| I2C-6 | A1 (SCL), A0 (SDA) | Available on header |
| I2C-7 | Available | Check device tree |

### Setup Steps

```bash
# Configure pinmap for I2C-6
# A1 → I2C6_SCL, A0 → I2C6_SDA
# Refer to MaixCAM2 documentation for pinmap commands

modprobe i2c-dev
ls /dev/i2c-*
```

---

## NanoKVM

Uses the same SG2002 SoC as LicheeRV Nano. GPIO and I2C access follows the same pinmux procedure. Refer to the LicheeRV Nano section above.

Check NanoKVM-specific pin headers for available I2C/SPI lines:
- https://wiki.sipeed.com/hardware/en/kvm/NanoKVM/introduction.html

---

## Common Issues

### devmem not found
The `devmem` utility may not be in the default image. Options:
- Use `busybox devmem` if busybox is installed
- Download devmem from the Sipeed package repository
- Cross-compile from source (single C file)

### Dynamic bus numbering
I2C adapter numbers can change between boots depending on driver load order. Always use `i2c detect` to find current bus assignments rather than hardcoding bus numbers.

### Permissions
`/dev/i2c-*`, `/dev/spidev*`, and serial ports typically require root access. Options:
- Run picoclaw as root
- Add the user to the `i2c`, `spi`, and `dialout` groups
- Create udev rules such as `SUBSYSTEM=="i2c-dev", MODE="0666"` and `SUBSYSTEM=="tty", GROUP="dialout", MODE="0660"`

### Pinmux commands are shell commands
The `devmem` steps above are not part of the `i2c`/`spi` tools. They require the `exec` tool (which may be disabled) and, with the default policy, user approval. Do not run pinmux writes from an untrusted prompt; verify the register address and value against the board documentation first.

### Serial device disappears
A USB-serial adapter may re-enumerate after a reset or power cycle. Use a `/dev/serial/by-id/...` link, run `serial list` again, and reopen any persistent session with `serial open`.
