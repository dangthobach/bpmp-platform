package tabular

import (
	"context"
	"fmt"
	"io"
)

// Write streams DTOs from iterator to target.
func Write[T any](
	ctx context.Context,
	format Format,
	target io.Writer,
	iterator Iterator[T],
	encoder Encoder[T],
	options WriteOptions,
) (WriteStats, error) {
	if target == nil || iterator == nil || encoder == nil {
		return WriteStats{}, fmt.Errorf("%w: target, iterator and encoder are required", ErrInvalidConfiguration)
	}
	if err := validateWriteOptions(format, options); err != nil {
		return WriteStats{}, err
	}
	switch format {
	case FormatCSV:
		return writeCSV(ctx, target, iterator, encoder, options)
	case FormatXLSX:
		return writeXLSX(ctx, target, iterator, encoder, options)
	case FormatXLS:
		return WriteStats{}, &Error{Op: "write", Format: FormatXLS, Err: ErrLegacyXLSWrite}
	default:
		return WriteStats{}, fmt.Errorf("%w: unsupported format %q", ErrInvalidConfiguration, format)
	}
}
