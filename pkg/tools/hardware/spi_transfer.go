package hardwaretools

import (
	"fmt"
	"unsafe"
)

const (
	spiIocMessage1     = 0x40206B00 // _IOW('k', 0, struct spi_ioc_transfer) — 32 bytes
	maxSPISegments     = 16
	maxSPITransferSize = 4096
)

// spiTransfer matches Linux kernel struct spi_ioc_transfer (32 bytes on all
// architectures).
type spiTransfer struct {
	txBuf       uint64
	rxBuf       uint64
	length      uint32
	speedHz     uint32
	delayUsecs  uint16
	bitsPerWord uint8
	csChange    uint8
	txNbits     uint8
	rxNbits     uint8
	wordDelay   uint8
	pad         uint8
}

type spiSegment struct {
	Data       []byte
	ReadLength int // -1: full-duplex, same as len(Data)
	DelayUsecs int
	CSChange   bool
}

type spiTransferBuffers struct {
	transfers []spiTransfer
	rxBuffers [][]byte
	keepAlive [][]byte
}

func spiIocMessage(count int) uintptr {
	if count < 1 {
		return 0
	}
	return uintptr(spiIocMessage1 + (count-1)*0x00200000)
}

func buildSPITransfers(segments []spiSegment, speed uint32) (spiTransferBuffers, error) {
	if len(segments) == 0 {
		return spiTransferBuffers{}, fmt.Errorf("at least one SPI segment is required")
	}
	if len(segments) > maxSPISegments {
		return spiTransferBuffers{}, fmt.Errorf("too many SPI segments: %d (max %d)", len(segments), maxSPISegments)
	}

	result := spiTransferBuffers{
		transfers: make([]spiTransfer, 0, len(segments)),
		rxBuffers: make([][]byte, 0, len(segments)),
		keepAlive: make([][]byte, 0, len(segments)*2),
	}
	for i, segment := range segments {
		readLength := segment.ReadLength
		if readLength < 0 {
			readLength = len(segment.Data)
		}
		if readLength < 0 || readLength > maxSPITransferSize {
			return spiTransferBuffers{}, fmt.Errorf("segment %d read_length must be between 0 and %d", i, maxSPITransferSize)
		}
		length := max(len(segment.Data), readLength)
		if length == 0 {
			return spiTransferBuffers{}, fmt.Errorf("segment %d has no data and no read_length", i)
		}
		if length > maxSPITransferSize {
			return spiTransferBuffers{}, fmt.Errorf("segment %d is too large: %d bytes (max %d)", i, length, maxSPITransferSize)
		}
		if segment.DelayUsecs < 0 || segment.DelayUsecs > 65535 {
			return spiTransferBuffers{}, fmt.Errorf("segment %d delay_usecs must be between 0 and 65535", i)
		}

		tx := make([]byte, length)
		copy(tx, segment.Data)
		var rx []byte
		if readLength > 0 {
			rx = make([]byte, readLength)
		}

		transfer := spiTransfer{
			length:      uint32(length),
			speedHz:     speed,
			delayUsecs:  uint16(segment.DelayUsecs),
			bitsPerWord: 8,
		}
		if segment.CSChange {
			transfer.csChange = 1
		}
		transfer.txBuf = uint64(uintptr(unsafe.Pointer(&tx[0])))
		if len(rx) > 0 {
			transfer.rxBuf = uint64(uintptr(unsafe.Pointer(&rx[0])))
		}

		result.transfers = append(result.transfers, transfer)
		result.rxBuffers = append(result.rxBuffers, rx)
		result.keepAlive = append(result.keepAlive, tx)
		if len(rx) > 0 {
			result.keepAlive = append(result.keepAlive, rx)
		}
	}
	return result, nil
}
