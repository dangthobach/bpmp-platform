package tabular

import (
	"context"
	"encoding/csv"
	"errors"
	"io"
)

const csvSheetName = "csv"

type boundedReader struct {
	reader    io.Reader
	remaining int64
}

func (reader *boundedReader) Read(buffer []byte) (int, error) {
	if reader.remaining <= 0 {
		var probe [1]byte
		if _, err := reader.reader.Read(probe[:]); err == io.EOF {
			return 0, io.EOF
		}
		return 0, ErrLimitExceeded
	}
	if int64(len(buffer)) > reader.remaining {
		buffer = buffer[:reader.remaining]
	}
	n, err := reader.reader.Read(buffer)
	reader.remaining -= int64(n)
	return n, err
}

func readCSV[T any](ctx context.Context, source io.Reader, decoder Decoder[T], options ReadOptions, handler BatchHandler[T]) (ReadStats, error) {
	input := &boundedReader{reader: source, remaining: options.MaxInputBytes}
	reader := csv.NewReader(input)
	reader.Comma = options.CSV.Comma
	reader.Comment = options.CSV.Comment
	reader.LazyQuotes = options.CSV.LazyQuotes
	reader.TrimLeadingSpace = options.CSV.TrimLeadingSpace
	reader.FieldsPerRecord = options.CSV.FieldsPerRecord
	reader.ReuseRecord = true
	collector := newBatchCollector(ctx, FormatCSV, decoder, options, handler)
	collector.stats.Sheets = 1
	var physicalRow uint64
	for {
		record, err := reader.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		physicalRow++
		if err != nil {
			return collector.stats, &Error{Op: "read", Format: FormatCSV, Sheet: csvSheetName, Row: physicalRow, Err: err}
		}
		if physicalRow <= options.HeaderRows {
			continue
		}
		row := Row{Sheet: csvSheetName, Number: physicalRow, Values: record}
		if err := collector.add(row); err != nil {
			return collector.stats, err
		}
	}
	return collector.finish()
}

func writeCSV[T any](ctx context.Context, target io.Writer, iterator Iterator[T], encoder Encoder[T], options WriteOptions) (WriteStats, error) {
	writer := csv.NewWriter(target)
	writer.Comma = options.CSV.Comma
	writer.UseCRLF = options.CSV.UseCRLF
	stats := WriteStats{Sheets: 1}
	if len(options.Header) > 0 {
		if err := writeCSVCells(writer, options.Header, 1, options); err != nil {
			return stats, err
		}
	}
	rowNumber := uint64(1)
	if len(options.Header) > 0 {
		rowNumber++
	}
	for {
		if err := ctx.Err(); err != nil {
			return stats, err
		}
		item, ok, err := iterator.Next(ctx)
		if err != nil {
			return stats, &Error{Op: "iterate", Format: FormatCSV, Row: rowNumber, Err: err}
		}
		if !ok {
			break
		}
		if stats.Rows >= options.MaxRows {
			return stats, &Error{Op: "write", Format: FormatCSV, Row: rowNumber, Err: ErrLimitExceeded}
		}
		cells, err := encoder(item)
		if err != nil {
			return stats, &Error{Op: "encode", Format: FormatCSV, Row: rowNumber, Err: err}
		}
		if err := writeCSVCells(writer, cells, rowNumber, options); err != nil {
			return stats, err
		}
		stats.Rows++
		rowNumber++
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return stats, &Error{Op: "flush", Format: FormatCSV, Err: err}
	}
	return stats, nil
}

func writeCSVCells(writer *csv.Writer, cells []Cell, row uint64, options WriteOptions) error {
	values, err := exportCells(FormatCSV, csvSheetName, row, cells, options, false)
	if err != nil {
		return err
	}
	record := make([]string, len(values))
	for index, value := range values {
		record[index] = value.(string)
	}
	if err := writer.Write(record); err != nil {
		return &Error{Op: "write", Format: FormatCSV, Sheet: csvSheetName, Row: row, Err: err}
	}
	return nil
}
