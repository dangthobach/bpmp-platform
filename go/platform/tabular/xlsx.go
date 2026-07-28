package tabular

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"

	"github.com/xuri/excelize/v2"
)

const xlsxMaxRowsPerSheet = 1_048_576

func readXLSX[T any](ctx context.Context, source io.Reader, decoder Decoder[T], options ReadOptions, handler BatchHandler[T]) (stats ReadStats, err error) {
	if options.XLSXUnzipSizeLimit <= 0 || options.XLSXMemoryLimit <= 0 {
		return stats, fmt.Errorf("%w: positive XLSX unzip and memory limits are required", ErrInvalidConfiguration)
	}
	file, err := spool(source, options.MaxInputBytes, options.TempDir)
	if err != nil {
		return stats, &Error{Op: "spool", Format: FormatXLSX, Err: err}
	}
	defer func() {
		err = errors.Join(err, removeSpool(file))
	}()
	workbook, err := excelize.OpenFile(file.Name(), excelize.Options{
		RawCellValue:      true,
		UnzipSizeLimit:    options.XLSXUnzipSizeLimit,
		UnzipXMLSizeLimit: options.XLSXMemoryLimit,
		TmpDir:            options.TempDir,
	})
	if err != nil {
		return stats, &Error{Op: "open", Format: FormatXLSX, Err: err}
	}
	defer func() {
		err = errors.Join(err, workbook.Close())
	}()
	collector := newBatchCollector(ctx, FormatXLSX, decoder, options, handler)
	selected := make(map[string]bool, len(options.SheetNames))
	for _, sheet := range workbook.GetSheetList() {
		if !selectedSheet(sheet, options.SheetNames) {
			continue
		}
		selected[sheet] = true
		collector.stats.Sheets++
		rows, openErr := workbook.Rows(sheet)
		if openErr != nil {
			return collector.stats, &Error{Op: "open rows", Format: FormatXLSX, Sheet: sheet, Err: openErr}
		}
		physicalRow := uint64(0)
		for rows.Next() {
			physicalRow++
			if ctxErr := ctx.Err(); ctxErr != nil {
				return collector.stats, closeWithError(ctxErr, rows)
			}
			values, columnsErr := rows.Columns()
			if columnsErr != nil {
				return collector.stats, closeWithError(&Error{Op: "read", Format: FormatXLSX, Sheet: sheet, Row: physicalRow, Err: columnsErr}, rows)
			}
			if physicalRow <= options.HeaderRows {
				continue
			}
			if addErr := collector.add(Row{Sheet: sheet, Number: physicalRow, Values: values}); addErr != nil {
				return collector.stats, closeWithError(addErr, rows)
			}
		}
		if rowsErr := rows.Error(); rowsErr != nil {
			return collector.stats, closeWithError(&Error{Op: "read", Format: FormatXLSX, Sheet: sheet, Row: physicalRow, Err: rowsErr}, rows)
		}
		if closeErr := rows.Close(); closeErr != nil {
			return collector.stats, &Error{Op: "close rows", Format: FormatXLSX, Sheet: sheet, Err: closeErr}
		}
	}
	if missing := missingSheet(options.SheetNames, selected); missing != "" {
		return collector.stats, &Error{Op: "select sheet", Format: FormatXLSX, Sheet: missing, Err: errors.New("sheet does not exist")}
	}
	return collector.finish()
}

type xlsxStream struct {
	file       *excelize.File
	writer     *excelize.StreamWriter
	options    WriteOptions
	sheet      string
	sheetIndex uint64
	row        uint64
	dataRows   uint64
}

func writeXLSX[T any](ctx context.Context, target io.Writer, iterator Iterator[T], encoder Encoder[T], options WriteOptions) (stats WriteStats, err error) {
	headerRows := uint64(0)
	if len(options.Header) > 0 {
		headerRows = 1
	}
	if options.RowsPerSheet+headerRows > xlsxMaxRowsPerSheet {
		return stats, fmt.Errorf("%w: XLSX rows per sheet plus header exceeds %d", ErrInvalidConfiguration, xlsxMaxRowsPerSheet)
	}
	workbook := excelize.NewFile(excelize.Options{TmpDir: options.TempDir})
	defer func() {
		err = errors.Join(err, workbook.Close())
	}()
	stream := &xlsxStream{file: workbook, options: options}
	if err := stream.start(); err != nil {
		return stats, err
	}
	stats.Sheets = 1
	for {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return stats, ctxErr
		}
		item, ok, nextErr := iterator.Next(ctx)
		if nextErr != nil {
			return stats, &Error{Op: "iterate", Format: FormatXLSX, Sheet: stream.sheet, Row: stream.row + 1, Err: nextErr}
		}
		if !ok {
			break
		}
		if stats.Rows >= options.MaxRows {
			return stats, &Error{Op: "write", Format: FormatXLSX, Sheet: stream.sheet, Row: stream.row + 1, Err: fmt.Errorf("%w: row limit %d", ErrLimitExceeded, options.MaxRows)}
		}
		if stream.dataRows == options.RowsPerSheet {
			if err := stream.nextSheet(); err != nil {
				return stats, err
			}
			stats.Sheets++
		}
		cells, encodeErr := encoder(item)
		if encodeErr != nil {
			return stats, &Error{Op: "encode", Format: FormatXLSX, Sheet: stream.sheet, Row: stream.row + 1, Err: encodeErr}
		}
		if err := stream.write(cells); err != nil {
			return stats, err
		}
		stats.Rows++
	}
	if err := stream.writer.Flush(); err != nil {
		return stats, &Error{Op: "flush", Format: FormatXLSX, Sheet: stream.sheet, Err: err}
	}
	if err := workbook.Write(target, excelize.Options{TmpDir: options.TempDir}); err != nil {
		return stats, &Error{Op: "write output", Format: FormatXLSX, Err: err}
	}
	return stats, nil
}

func (stream *xlsxStream) start() error {
	stream.sheetIndex++
	stream.sheet = xlsxSheetName(stream.options.SheetName, stream.sheetIndex)
	if stream.sheetIndex == 1 {
		if err := stream.file.SetSheetName("Sheet1", stream.sheet); err != nil {
			return &Error{Op: "create sheet", Format: FormatXLSX, Sheet: stream.sheet, Err: err}
		}
	} else if _, err := stream.file.NewSheet(stream.sheet); err != nil {
		return &Error{Op: "create sheet", Format: FormatXLSX, Sheet: stream.sheet, Err: err}
	}
	writer, err := stream.file.NewStreamWriter(stream.sheet)
	if err != nil {
		return &Error{Op: "create stream", Format: FormatXLSX, Sheet: stream.sheet, Err: err}
	}
	stream.writer = writer
	stream.row = 0
	stream.dataRows = 0
	if len(stream.options.Header) > 0 {
		return stream.write(stream.options.Header)
	}
	return nil
}

func (stream *xlsxStream) nextSheet() error {
	if err := stream.writer.Flush(); err != nil {
		return &Error{Op: "flush", Format: FormatXLSX, Sheet: stream.sheet, Err: err}
	}
	return stream.start()
}

func (stream *xlsxStream) write(cells []Cell) error {
	stream.row++
	values, err := exportCells(FormatXLSX, stream.sheet, stream.row, cells, stream.options, true)
	if err != nil {
		return err
	}
	axis, err := excelize.CoordinatesToCellName(1, int(stream.row))
	if err != nil {
		return &Error{Op: "address row", Format: FormatXLSX, Sheet: stream.sheet, Row: stream.row, Err: err}
	}
	if err := stream.writer.SetRow(axis, values); err != nil {
		return &Error{Op: "write", Format: FormatXLSX, Sheet: stream.sheet, Row: stream.row, Err: err}
	}
	if stream.row > 1 || len(stream.options.Header) == 0 {
		stream.dataRows++
	}
	return nil
}

func xlsxSheetName(base string, index uint64) string {
	if index == 1 {
		return base
	}
	suffix := "-" + strconv.FormatUint(index, 10)
	maxBase := 31 - len(suffix)
	if len(base) > maxBase {
		base = base[:maxBase]
	}
	return base + suffix
}
