package tabular

import (
	"context"
	"fmt"
	"io"
)

// Read decodes a source into bounded DTO batches.
func Read[T any](
	ctx context.Context,
	format Format,
	source io.Reader,
	decoder Decoder[T],
	options ReadOptions,
	handler BatchHandler[T],
) (ReadStats, error) {
	if source == nil || decoder == nil || handler == nil {
		return ReadStats{}, fmt.Errorf("%w: source, decoder and handler are required", ErrInvalidConfiguration)
	}
	if err := validateReadOptions(format, options); err != nil {
		return ReadStats{}, err
	}
	switch format {
	case FormatCSV:
		return readCSV(ctx, source, decoder, options, handler)
	case FormatXLSX:
		return readXLSX(ctx, source, decoder, options, handler)
	case FormatXLS:
		return readXLS(ctx, source, decoder, options, handler)
	default:
		return ReadStats{}, fmt.Errorf("%w: unsupported format %q", ErrInvalidConfiguration, format)
	}
}

type batchCollector[T any] struct {
	ctx       context.Context
	format    Format
	decoder   Decoder[T]
	handler   BatchHandler[T]
	options   ReadOptions
	stats     ReadStats
	batch     []T
	batchName string
	firstRow  uint64
}

func newBatchCollector[T any](ctx context.Context, format Format, decoder Decoder[T], options ReadOptions, handler BatchHandler[T]) *batchCollector[T] {
	return &batchCollector[T]{
		ctx:     ctx,
		format:  format,
		decoder: decoder,
		handler: handler,
		options: options,
		batch:   make([]T, 0, options.BatchSize),
	}
}

func (collector *batchCollector[T]) add(row Row) error {
	if err := collector.ctx.Err(); err != nil {
		return err
	}
	if collector.stats.Rows >= collector.options.MaxRows {
		return &Error{Op: "read", Format: collector.format, Sheet: row.Sheet, Row: row.Number, Err: fmt.Errorf("%w: row limit %d", ErrLimitExceeded, collector.options.MaxRows)}
	}
	if err := validateInputRow(collector.format, row, collector.options); err != nil {
		return err
	}
	item, err := collector.decoder(row)
	if err != nil {
		return &Error{Op: "decode", Format: collector.format, Sheet: row.Sheet, Row: row.Number, Err: err}
	}
	if len(collector.batch) == 0 {
		collector.batchName = row.Sheet
		collector.firstRow = row.Number
	}
	if collector.batchName != row.Sheet {
		if err := collector.flush(); err != nil {
			return err
		}
		collector.batchName = row.Sheet
		collector.firstRow = row.Number
	}
	collector.batch = append(collector.batch, item)
	collector.stats.Rows++
	if len(collector.batch) == collector.options.BatchSize {
		return collector.flush()
	}
	return nil
}

func (collector *batchCollector[T]) flush() error {
	if len(collector.batch) == 0 {
		return nil
	}
	batch := Batch[T]{
		Sheet:    collector.batchName,
		FirstRow: collector.firstRow,
		Items:    collector.batch,
	}
	if err := collector.handler(collector.ctx, batch); err != nil {
		return &Error{Op: "handle batch", Format: collector.format, Sheet: batch.Sheet, Row: batch.FirstRow, Err: err}
	}
	collector.stats.Batches++
	collector.batch = make([]T, 0, collector.options.BatchSize)
	return nil
}

func (collector *batchCollector[T]) finish() (ReadStats, error) {
	if err := collector.flush(); err != nil {
		return collector.stats, err
	}
	return collector.stats, nil
}

func selectedSheet(name string, selected []string) bool {
	if len(selected) == 0 {
		return true
	}
	for _, candidate := range selected {
		if candidate == name {
			return true
		}
	}
	return false
}

func missingSheet(requested []string, selected map[string]bool) string {
	for _, name := range requested {
		if !selected[name] {
			return name
		}
	}
	return ""
}
