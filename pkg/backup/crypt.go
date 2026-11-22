package backup

import (
	"bufio"
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"golang.org/x/crypto/scrypt"
)

const (
	// PassphraseEnv supplies the archive passphrase without putting it on the
	// command line. It is independent from PICOCLAW_KEY_PASSPHRASE.
	PassphraseEnv = "PICOCLAW_BACKUP_PASSPHRASE"
	// ScryptLogNEnv overrides the scrypt cost (log2 N) for encrypted backups.
	// Lower it on very small devices; the default is 15 (about 32 MiB).
	ScryptLogNEnv = "PICOCLAW_BACKUP_KDF_LOG_N"

	cryptMagicString = "PICOCLAWBK1"
	cryptVersion     = 1
	cryptKDFScrypt   = 1
	cryptCipherAES   = 1

	defaultScryptLogN = 15
	defaultScryptR    = 8
	defaultScryptP    = 1
	maxScryptLogN     = 18
	maxScryptR        = 16
	maxScryptP        = 4

	cryptKeyLen      = 32
	cryptSaltLen     = 16
	cryptNoncePrefix = 8
	cryptChunkSize   = 1 << 20
	cryptTagLen      = 16
	cryptHeaderLen   = len(cryptMagicString) + 3 + 12 + cryptSaltLen + cryptNoncePrefix
)

var (
	cryptMagic = []byte(cryptMagicString)

	// ErrPassphraseRequired is returned when an encrypted archive is opened
	// without a passphrase.
	ErrPassphraseRequired = errors.New("backup is encrypted: a passphrase is required")
	// ErrDecrypt is returned when authentication fails, which usually means a
	// wrong passphrase or a corrupted archive.
	ErrDecrypt = errors.New("cannot decrypt backup")
)

// scryptLogN is a variable so tests can lower the KDF cost.
var scryptLogN = uint32(defaultScryptLogN)

func effectiveScryptLogN() uint32 {
	raw := strings.TrimSpace(os.Getenv(ScryptLogNEnv))
	if raw == "" {
		return scryptLogN
	}
	value, err := strconv.ParseUint(raw, 10, 32)
	if err != nil || value < 10 || value > maxScryptLogN {
		return scryptLogN
	}
	return uint32(value)
}

func currentEncryptionInfo() *EncryptionInfo {
	return &EncryptionInfo{
		Algorithm: "aes-256-gcm",
		KDF:       "scrypt",
		KDFLogN:   effectiveScryptLogN(),
		KDFR:      defaultScryptR,
		KDFP:      defaultScryptP,
	}
}

type cryptParams struct {
	logN       uint32
	r          uint32
	p          uint32
	salt       [cryptSaltLen]byte
	nonceStart [cryptNoncePrefix]byte
	header     []byte
}

func newCryptParams(passphrase string) (*cryptParams, cipher.AEAD, error) {
	if passphrase == "" {
		return nil, nil, ErrPassphraseRequired
	}
	params := &cryptParams{
		logN: effectiveScryptLogN(),
		r:    defaultScryptR,
		p:    defaultScryptP,
	}
	if _, err := rand.Read(params.salt[:]); err != nil {
		return nil, nil, fmt.Errorf("generate salt: %w", err)
	}
	if _, err := rand.Read(params.nonceStart[:]); err != nil {
		return nil, nil, fmt.Errorf("generate nonce: %w", err)
	}

	params.header = make([]byte, 0, cryptHeaderLen)
	params.header = append(params.header, cryptMagic...)
	params.header = append(params.header, cryptVersion, cryptKDFScrypt, cryptCipherAES)
	params.header = binary.BigEndian.AppendUint32(params.header, params.logN)
	params.header = binary.BigEndian.AppendUint32(params.header, params.r)
	params.header = binary.BigEndian.AppendUint32(params.header, params.p)
	params.header = append(params.header, params.salt[:]...)
	params.header = append(params.header, params.nonceStart[:]...)

	aead, err := deriveAEAD(passphrase, params)
	if err != nil {
		return nil, nil, err
	}
	return params, aead, nil
}

func parseCryptHeader(header []byte) (*cryptParams, error) {
	if len(header) != cryptHeaderLen || !bytes.Equal(header[:len(cryptMagic)], cryptMagic) {
		return nil, fmt.Errorf("not a PicoClaw encrypted backup")
	}
	offset := len(cryptMagic)
	if header[offset] != cryptVersion {
		return nil, fmt.Errorf("unsupported encrypted backup version %d", header[offset])
	}
	if header[offset+1] != cryptKDFScrypt || header[offset+2] != cryptCipherAES {
		return nil, fmt.Errorf("unsupported encrypted backup algorithm")
	}
	offset += 3

	params := &cryptParams{
		logN: binary.BigEndian.Uint32(header[offset : offset+4]),
		r:    binary.BigEndian.Uint32(header[offset+4 : offset+8]),
		p:    binary.BigEndian.Uint32(header[offset+8 : offset+12]),
	}
	offset += 12
	if params.logN < 10 || params.logN > maxScryptLogN ||
		params.r == 0 || params.r > maxScryptR ||
		params.p == 0 || params.p > maxScryptP {
		return nil, fmt.Errorf("encrypted backup has unsafe KDF parameters")
	}
	copy(params.salt[:], header[offset:offset+cryptSaltLen])
	offset += cryptSaltLen
	copy(params.nonceStart[:], header[offset:offset+cryptNoncePrefix])
	params.header = append([]byte(nil), header...)
	return params, nil
}

func deriveAEAD(passphrase string, params *cryptParams) (cipher.AEAD, error) {
	if params == nil {
		return nil, fmt.Errorf("missing encryption parameters")
	}
	if passphrase == "" {
		return nil, ErrPassphraseRequired
	}
	key, err := scrypt.Key(
		[]byte(passphrase),
		params.salt[:],
		1<<params.logN,
		int(params.r),
		int(params.p),
		cryptKeyLen,
	)
	if err != nil {
		return nil, fmt.Errorf("derive encryption key: %w", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("initialize cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("initialize AEAD: %w", err)
	}
	return aead, nil
}

type encryptWriter struct {
	dst     io.Writer
	aead    cipher.AEAD
	params  *cryptParams
	prefix  [cryptNoncePrefix]byte
	counter uint32
	buf     []byte
	closed  bool
}

func newEncryptWriter(dst io.Writer, passphrase string) (*encryptWriter, *EncryptionInfo, error) {
	params, aead, err := newCryptParams(passphrase)
	if err != nil {
		return nil, nil, err
	}
	if _, err := dst.Write(params.header); err != nil {
		return nil, nil, fmt.Errorf("write encryption header: %w", err)
	}
	writer := &encryptWriter{
		dst:    dst,
		aead:   aead,
		params: params,
		prefix: params.nonceStart,
		buf:    make([]byte, 0, cryptChunkSize),
	}
	info := &EncryptionInfo{
		Algorithm: "aes-256-gcm",
		KDF:       "scrypt",
		KDFLogN:   params.logN,
		KDFR:      params.r,
		KDFP:      params.p,
	}
	return writer, info, nil
}

func (w *encryptWriter) Write(p []byte) (int, error) {
	if w.closed {
		return 0, fmt.Errorf("encrypted writer is closed")
	}
	written := 0
	for len(p) > 0 {
		space := cryptChunkSize - len(w.buf)
		if space == 0 {
			if err := w.flushChunk(); err != nil {
				return written, err
			}
			space = cryptChunkSize
		}
		n := min(space, len(p))
		w.buf = append(w.buf, p[:n]...)
		p = p[n:]
		written += n
	}
	return written, nil
}

func (w *encryptWriter) flushChunk() error {
	if len(w.buf) == 0 {
		return nil
	}
	var length [4]byte
	binary.BigEndian.PutUint32(length[:], uint32(len(w.buf)))
	if _, err := w.dst.Write(length[:]); err != nil {
		return fmt.Errorf("write encrypted chunk length: %w", err)
	}

	nonce := w.nonce()
	ciphertext := w.aead.Seal(nil, nonce, w.buf, w.params.header)
	if _, err := w.dst.Write(ciphertext); err != nil {
		return fmt.Errorf("write encrypted chunk: %w", err)
	}
	w.counter++
	w.buf = w.buf[:0]
	return nil
}

func (w *encryptWriter) nonce() []byte {
	var nonce [cryptNoncePrefix + 4]byte
	copy(nonce[:cryptNoncePrefix], w.prefix[:])
	binary.BigEndian.PutUint32(nonce[cryptNoncePrefix:], w.counter)
	return nonce[:]
}

// Close flushes the last chunk and writes the end marker. Truncation before
// the marker is detected by the reader.
func (w *encryptWriter) Close() error {
	if w.closed {
		return nil
	}
	if err := w.flushChunk(); err != nil {
		return err
	}
	var end [4]byte
	if _, err := w.dst.Write(end[:]); err != nil {
		return fmt.Errorf("write encryption trailer: %w", err)
	}
	w.closed = true
	return nil
}

type decryptReader struct {
	src     *bufio.Reader
	aead    cipher.AEAD
	params  *cryptParams
	prefix  [cryptNoncePrefix]byte
	counter uint32
	plain   []byte
	done    bool
	err     error
}

func newDecryptReader(src io.Reader, passphrase string) (io.Reader, error) {
	br := bufio.NewReaderSize(src, 64*1024)
	header := make([]byte, cryptHeaderLen)
	if _, err := io.ReadFull(br, header); err != nil {
		return nil, fmt.Errorf("read encryption header: %w", err)
	}
	params, err := parseCryptHeader(header)
	if err != nil {
		return nil, err
	}
	aead, err := deriveAEAD(passphrase, params)
	if err != nil {
		return nil, err
	}
	return &decryptReader{
		src:    br,
		aead:   aead,
		params: params,
		prefix: params.nonceStart,
	}, nil
}

func (r *decryptReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	for len(r.plain) == 0 {
		if r.err != nil {
			return 0, r.err
		}
		if r.done {
			return 0, io.EOF
		}
		r.err = r.readChunk()
	}
	n := copy(p, r.plain)
	r.plain = r.plain[n:]
	return n, nil
}

func (r *decryptReader) readChunk() error {
	var length [4]byte
	if _, err := io.ReadFull(r.src, length[:]); err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return fmt.Errorf("%w: archive is truncated", ErrDecrypt)
		}
		return fmt.Errorf("read encrypted chunk length: %w", err)
	}
	size := binary.BigEndian.Uint32(length[:])
	if size == 0 {
		r.done = true
		return nil
	}
	if size > cryptChunkSize {
		return fmt.Errorf("%w: invalid chunk size %d", ErrDecrypt, size)
	}

	ciphertext := make([]byte, int(size)+cryptTagLen)
	if _, err := io.ReadFull(r.src, ciphertext); err != nil {
		return fmt.Errorf("%w: archive is truncated", ErrDecrypt)
	}
	plain, err := r.aead.Open(nil, r.nonce(), ciphertext, r.params.header)
	if err != nil {
		return fmt.Errorf("%w: wrong passphrase or corrupted archive", ErrDecrypt)
	}
	r.counter++
	r.plain = plain
	return nil
}

func (r *decryptReader) nonce() []byte {
	var nonce [cryptNoncePrefix + 4]byte
	copy(nonce[:cryptNoncePrefix], r.prefix[:])
	binary.BigEndian.PutUint32(nonce[cryptNoncePrefix:], r.counter)
	return nonce[:]
}

// IsEncrypted reports whether path starts with the PicoClaw encrypted backup
// magic. It does not validate the rest of the archive.
func IsEncrypted(path string) (bool, error) {
	file, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer file.Close()

	head := make([]byte, len(cryptMagic))
	n, err := io.ReadFull(file, head)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return false, err
	}
	return n == len(head) && bytes.Equal(head, cryptMagic), nil
}
