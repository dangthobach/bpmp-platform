package tabular

import (
	"errors"
	"fmt"
	"io"
	"os"
)

func spool(source io.Reader, maxBytes int64, tempDir string) (_ *os.File, err error) {
	file, err := os.CreateTemp(tempDir, "bpmp-tabular-*")
	if err != nil {
		return nil, fmt.Errorf("create bounded spool: %w", err)
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, file.Close(), os.Remove(file.Name()))
		}
	}()
	written, err := io.Copy(file, io.LimitReader(source, maxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("write bounded spool: %w", err)
	}
	if written > maxBytes {
		return nil, fmt.Errorf("%w: input exceeds %d bytes", ErrLimitExceeded, maxBytes)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return nil, fmt.Errorf("rewind bounded spool: %w", err)
	}
	return file, nil
}

func removeSpool(file *os.File) error {
	if file == nil {
		return nil
	}
	return errors.Join(file.Close(), os.Remove(file.Name()))
}
