package tabular

import (
	"context"
	"errors"
	"fmt"
	"io"

	legacyxls "github.com/MeKo-Christian/xls"
)

func readXLS[T any](ctx context.Context, source io.Reader, decoder Decoder[T], options ReadOptions, handler BatchHandler[T]) (stats ReadStats, err error) {
	file, err := spool(source, options.MaxInputBytes, options.TempDir)
	if err != nil {
		return stats, &Error{Op: "spool", Format: FormatXLS, Err: err}
	}
	defer func() {
		err = errors.Join(err, removeSpool(file))
	}()
	defer func() {
		if recovered := recover(); recovered != nil {
			err = &Error{Op: "parse", Format: FormatXLS, Err: fmt.Errorf("legacy XLS parser rejected malformed input: %v", recovered)}
		}
	}()
	workbook, err := legacyxls.OpenReader(file)
	if err != nil {
		return stats, &Error{Op: "open", Format: FormatXLS, Err: err}
	}
	collector := newBatchCollector(ctx, FormatXLS, decoder, options, handler)
	selected := make(map[string]bool, len(options.SheetNames))
	for sheetIndex := 0; sheetIndex < workbook.NumSheets(); sheetIndex++ {
		sheet := workbook.GetSheet(sheetIndex)
		if sheet == nil {
			return collector.stats, &Error{Op: "open sheet", Format: FormatXLS, Err: fmt.Errorf("sheet %d is missing", sheetIndex)}
		}
		name := sheet.Name
		if !selectedSheet(name, options.SheetNames) {
			continue
		}
		selected[name] = true
		collector.stats.Sheets++
		rowCount := int(sheet.MaxRow) + 1
		for rowIndex := 0; rowIndex < rowCount; rowIndex++ {
			physicalRow := uint64(rowIndex + 1)
			if physicalRow <= options.HeaderRows {
				continue
			}
			if ctxErr := ctx.Err(); ctxErr != nil {
				return collector.stats, ctxErr
			}
			sourceRow := sheet.Row(rowIndex)
			if sourceRow == nil {
				if addErr := collector.add(Row{Sheet: name, Number: physicalRow}); addErr != nil {
					return collector.stats, addErr
				}
				continue
			}
			values := make([]string, sourceRow.LastCol())
			for columnIndex := range values {
				values[columnIndex] = sourceRow.Col(columnIndex)
			}
			if addErr := collector.add(Row{Sheet: name, Number: physicalRow, Values: values}); addErr != nil {
				return collector.stats, addErr
			}
		}
	}
	if missing := missingSheet(options.SheetNames, selected); missing != "" {
		return collector.stats, &Error{Op: "select sheet", Format: FormatXLS, Sheet: missing, Err: errors.New("sheet does not exist")}
	}
	return collector.finish()
}
