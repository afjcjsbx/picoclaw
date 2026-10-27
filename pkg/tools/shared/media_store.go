package toolshared

import (
	"errors"
	"fmt"
	"os"

	"github.com/sipeed/picoclaw/pkg/media"
)

// ErrMediaStoreRegistration reports that the temporary file was written but the
// media store refused to register it. Callers that describe the failure in their
// own words can tell the two steps apart with errors.Is.
var ErrMediaStoreRegistration = errors.New("media store registration failed")

// StoreTempMediaInput describes one media artifact on its way to the store: the
// bytes to persist, the metadata the store should record, and the naming pieces
// each caller derives from its own tool context.
type StoreTempMediaInput struct {
	Store      media.MediaStore
	Data       []byte
	MIMEType   string
	TempPrefix string // prefix of the temporary file name, e.g. "tool-inline-"
	Filename   string // name the store records for the artifact
	Source     string // provenance recorded in the metadata
	Scope      string // lifecycle scope the store registers the file under
}

// StoreTempMedia writes Data to a fresh file in the shared media temp directory
// and hands the path to Store. Any failure removes the temporary file again; a
// registration failure wraps ErrMediaStoreRegistration.
func StoreTempMedia(in StoreTempMediaInput) (string, error) {
	dir := media.TempDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}

	tmpFile, err := os.CreateTemp(dir, in.TempPrefix+"*"+ExtensionForMIMEType(in.MIMEType))
	if err != nil {
		return "", err
	}
	tmpPath := tmpFile.Name()

	if _, err = tmpFile.Write(in.Data); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmpPath)
		return "", err
	}
	if err = tmpFile.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return "", err
	}

	ref, err := in.Store.Store(tmpPath, media.MediaMeta{
		Filename:    in.Filename,
		ContentType: in.MIMEType,
		Source:      in.Source,
	}, in.Scope)
	if err != nil {
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("%w: %w", ErrMediaStoreRegistration, err)
	}

	return ref, nil
}
