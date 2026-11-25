# Common I2C/SPI Device Reference

## Tool syntax used below

- `i2c read bus="1" address=118 register=208 length=1` sends register `0xD0`, then reads one byte.
- `i2c read bus="1" address=56 command=[172,51,0] delay_ms=80 length=6` sends a raw command, waits 80 ms, then reads six bytes.
- `i2c write bus="1" address=118 register=244 data=[37] delay_ms=40 confirm=true` writes `0xF4 = 0x25`, then waits 40 ms.
- `spi transfer device="2.0" speed=1000000 mode=0 data=[1,128,0] confirm=true` sends three bytes and returns three received bytes.
- `spi transfer device="2.0" segments=[{"data":[3,0,0,0]},{"read_length":16}] confirm=true` sends a command and reads the response with chip select held low.

All writes also require `tools.<tool>.allow_write: true`, a matching `tools.<tool>.allow` entry, and (by default) user approval. See `SKILL.md` for the write safety rules.

The tool returns bytes as an integer array plus a hex array. Parse multi-byte values according to the device's endianness and signedness.

## I2C Devices

### AHT20 — Temperature & Humidity
- **Address:** 0x38
- **Init:** `i2c write bus="1" address=56 data=[190,8,0] delay_ms=10 confirm=true`
- **Measure:** `i2c read bus="1" address=56 command=[172,51,0] delay_ms=80 length=6`
- **Parse:** Status=byte[0], Humidity=(byte[1]<<12|byte[2]<<4|byte[3]>>4)/2^20*100, Temp=(byte[3]&0x0F<<16|byte[4]<<8|byte[5])/2^20*200-50
- **Notes:** No register addressing. The AHT20 needs the measurement delay; do not replace `delay_ms` with an immediate read.

### BME280 / BMP280 — Temperature, Humidity, Pressure
- **Address:** 0x76 or 0x77 (SDO pin selects)
- **Chip ID:** `i2c read bus="1" address=118 register=208 length=1` → BMP280=0x58, BME280=0x60
- **Forced measurement:** `i2c write bus="1" address=118 register=244 data=[37] delay_ms=40 confirm=true`, then `i2c read bus="1" address=118 register=247 length=8`
- **Config:** register 0xF2 (humidity oversampling), 0xF4 (temp/press oversampling + mode), 0xF5 (standby, filter)
- **Calibration:** `i2c read bus="1" address=118 register=136 length=26` and `i2c read bus="1" address=118 register=225 length=7`
- **Notes:** Add `repeated_start=true` if the board needs a combined register-read transaction. Data registers 0xF7–0xFE are big-endian; temperature and pressure are 20-bit values assembled from the bytes.
- **Also available via SPI** (mode 0 or 3).

### SSD1306 — 128x64 OLED Display
- **Address:** 0x3C (or 0x3D if SA0 high)
- **Command prefix:** `0x00`; **data prefix:** `0x40`
- **Init sequence:** `i2c write bus="1" address=60 register=0 data=[174,213,128,168,63,211,0,64,141,20,32,0,161,200,218,18,129,207,217,241,219,64,164,166,175] confirm=true`
- **Display on/off:** `i2c write bus="1" address=60 register=0 data=[175] confirm=true` / `data=[174]`
- **Pixel data:** `i2c write bus="1" address=60 register=64 data=[...] confirm=true`; split large frames into chunks of at most 256 bytes.
- **Also available via SPI** (faster, recommended for animations).

### MPU6050 — 6-axis Accelerometer + Gyroscope
- **Address:** 0x68 (or 0x69 if AD0 high)
- **WHO_AM_I:** `i2c read bus="1" address=104 register=117 length=1` → 0x68
- **Wake up:** `i2c write bus="1" address=104 register=107 data=[0] confirm=true` (clear sleep bit)
- **Accel:** `i2c read bus="1" address=104 register=59 length=6` (XH,XL,YH,YL,ZH,ZL; signed 16-bit, default ±2g)
- **Gyro:** `i2c read bus="1" address=104 register=67 length=6` (signed 16-bit, default ±250°/s)
- **Temperature:** `i2c read bus="1" address=104 register=65 length=2`; Temp°C = value/340 + 36.53

### DS3231 — Real-Time Clock
- **Address:** 0x68
- **Read time:** `i2c read bus="1" address=104 register=0 length=7` (seconds, minutes, hours, day, date, month, year; BCD encoded)
- **Set time:** `i2c write bus="1" address=104 register=0 data=[...BCD bytes...] confirm=true`
- **Temperature:** `i2c read bus="1" address=104 register=17 length=2` (signed, 0.25°C resolution)
- **Status:** `i2c read bus="1" address=104 register=15 length=1` (bit 2 = busy, bit 0 = alarm 1 flag)

### INA219 — Current & Power Monitor
- **Address:** 0x40–0x4F (A0/A1 pin selectable)
- **Config:** register 0x00; `i2c write bus="1" address=64 register=0 data=[high,low] confirm=true`
- **Shunt voltage:** `i2c read bus="1" address=64 register=1 length=2` (signed 16-bit, LSB=10 µV)
- **Bus voltage:** `i2c read bus="1" address=64 register=2 length=2` (bits 15:3, LSB=4 mV)
- **Power:** register 0x03; **Current:** register 0x04; **Calibration:** register 0x05 (after calibration)
- **Notes:** All 16-bit registers are big-endian.

### PCA9685 — 16-Channel PWM / Servo Controller
- **Address:** 0x40–0x7F (A0–A5 selectable, default 0x40)
- **Mode 1:** register 0x00 (bit 4=sleep, bit 5=auto-increment)
- **Prescale:** sleep, write register 0xFE, wake. Prescale = round(25 MHz / (4096 × freq)) − 1
- **Channel N:** registers 0x06+4*N to 0x09+4*N (ON_L, ON_H, OFF_L, OFF_H)
- **Servo 0°–180°:** ON=0, OFF=150–600 at 50 Hz. Typical: 0°=150, 90°=375, 180°=600
- **Notes:** ON/OFF values are little-endian. Use `i2c write` with `register=6+4*N` and four data bytes.

### AT24C256 — 256Kbit EEPROM
- **Address:** 0x50–0x57 (A0–A2 selectable)
- **Read:** `i2c read bus="1" address=80 command=[addrHigh,addrLow] length=N`
- **Write:** `i2c write bus="1" address=80 data=[addrHigh,addrLow,...up to 64 data bytes...] delay_ms=5 confirm=true`
- **Page size:** 64 bytes. Writes crossing a page boundary wrap within the page.
- **Notes:** EEPROM writes are persistent. Confirm the address, page boundaries, and payload before writing. Wait at least 5 ms after each page write.

## SPI Devices

### MCP3008 — 8-Channel 10-bit ADC
- **Interface:** SPI mode 0, max 3.6 MHz at 5 V
- **Read channel N:** `spi transfer device="2.0" speed=1000000 mode=0 data=[1,128,0] confirm=true` for channel 0; the second byte is `0x80 | (N << 4)` for single-ended channels.
- **Parse:** value = ((byte[1] & 0x03) << 8) | byte[2]
- **Voltage:** value × Vref / 1024

### W25Q128 — 128Mbit SPI Flash
- **Interface:** SPI mode 0 or 3, up to 104 MHz
- **Read ID:** `spi transfer device="2.0" speed=1000000 mode=0 data=[159,0,0,0] confirm=true` → manufacturer + device ID
- **Read data:** `spi transfer device="2.0" speed=1000000 mode=0 segments=[{"data":[3,addrHigh,addrMid,addrLow]},{"read_length":N}] confirm=true`
- **Status:** `spi transfer device="2.0" speed=1000000 mode=0 data=[5,0] confirm=true` (bit 0 = BUSY)
- **Notes:** Write enable, erase, and program commands modify flash persistently. Verify the datasheet and require explicit user confirmation before issuing them.

## Parsing Helper

The tool result is JSON. Typical I2C read output:

```json
{
  "bus": "/dev/i2c-1",
  "address": "0x76",
  "bytes": [96, 111, 98],
  "hex": ["0x60", "0x6f", "0x62"],
  "length": 3
}
```

Use `bytes` for numeric assembly and `hex` when reporting raw values. For SPI, the result contains a `segments` array with `sent`, `received`, `bytes`, and `hex` per segment.
