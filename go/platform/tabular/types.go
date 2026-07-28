package tabular

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"time"
)

// Format identifies a supported tabular file format.
type Format string

const (
	FormatCSV  Format = "csv"
	FormatXLSX Format = "xlsx"
	FormatXLS  Format = "xls"
)

var (
	ErrInvalidConfiguration = errors.New("invalid tabular configuration")
	ErrLimitExceeded        = errors.New("tabular resource limit exceeded")
	ErrFormulaInjection     = errors.New("tabular formula injection risk")
	ErrLegacyXLSWrite       = errors.New("legacy BIFF8 XLS export is not supported; use XLSX or CSV")
)

// Row is the source row passed to a DTO decoder. Number is one-based and
// refers to the physical row in the source sheet.
type Row struct {
	Sheet  string
	Number uint64
	Values []string
}

// Decoder maps one validated source row to an arbitrary DTO.
type Decoder[T any] func(Row) (T, error)

// Cell is one export cell. Supported values are strings, byte slices, booleans,
// integer and floating-point numbers, time.Time and nil.
type Cell struct {
	Value any
}

// Encoder maps an arbitrary DTO to one output row.
type Encoder[T any] func(T) ([]Cell, error)

// Iterator supplies export records without materializing the complete data set.
type Iterator[T any] interface {
	Next(context.Context) (item T, ok bool, err error)
}

// IteratorFunc adapts a function to Iterator.
type IteratorFunc[T any] func(context.Context) (item T, ok bool, err error)

func (fn IteratorFunc[T]) Next(ctx context.Context) (T, bool, error) {
	return fn(ctx)
}

// SliceIterator is a convenience adapter. Large exports should normally use a
// database cursor or another streaming Iterator instead.
func SliceIterator[T any](items []T) Iterator[T] {
	index := 0
	return IteratorFunc[T](func(context.Context) (T, bool, error) {
		var zero T
		if index == len(items) {
			return zero, false, nil
		}
		item := items[index]
		index++
		return item, true, nil
	})
}

// Batch is owned by the handler. The library does not mutate Items after the
// callback returns.
type Batch[T any] struct {
	Sheet    string
	FirstRow uint64
	Items    []T
}

// BatchHandler consumes one bounded batch.
type BatchHandler[T any] func(context.Context, Batch[T]) error

type CSVOptions struct {
	Comma            rune
	Comment          rune
	LazyQuotes       bool
	TrimLeadingSpace bool
	FieldsPerRecord  int
	UseCRLF          bool
}

// ReadOptions contains required resource limits. Zero limits are rejected.
type ReadOptions struct {
	BatchSize          int
	MaxRows            uint64
	MaxColumns         int
	MaxCellBytes       int
	MaxInputBytes      int64
	HeaderRows         uint64
	TempDir            string
	SheetNames         []string
	XLSXUnzipSizeLimit int64
	XLSXMemoryLimit    int64
	CSV                CSVOptions
}

// FormulaPolicy controls cells beginning with =, +, - or @.
type FormulaPolicy uint8

const (
	FormulaPolicyReject FormulaPolicy = iota + 1
	FormulaPolicyEscape
	FormulaPolicyAllow
)

// WriteOptions contains required output limits. Header and SheetName are
// caller-owned configuration, not package-level business defaults.
type WriteOptions struct {
	MaxRows      uint64
	MaxColumns   int
	MaxCellBytes int
	RowsPerSheet uint64
	TempDir      string
	SheetName    string
	Header       []Cell
	Formula      FormulaPolicy
	CSV          CSVOptions
}

type ReadStats struct {
	Rows    uint64
	Batches uint64
	Sheets  uint64
}

type WriteStats struct {
	Rows   uint64
	Sheets uint64
}

// Error adds format and source location without discarding the underlying
// typed error.
type Error struct {
	Op     string
	Format Format
	Sheet  string
	Row    uint64
	Column int
	Err    error
}

func (e *Error) Error() string {
	location := ""
	if e.Sheet != "" {
		location += " sheet=" + e.Sheet
	}
	if e.Row != 0 {
		location += fmt.Sprintf(" row=%d", e.Row)
	}
	if e.Column != 0 {
		location += fmt.Sprintf(" column=%d", e.Column)
	}
	return fmt.Sprintf("tabular %s %s%s: %v", e.Op, e.Format, location, e.Err)
}

func (e *Error) Unwrap() error {
	return e.Err
}

func validateReadOptions(format Format, options ReadOptions) error {
	if options.BatchSize <= 0 || options.MaxRows == 0 || options.MaxColumns <= 0 ||
		options.MaxCellBytes <= 0 || options.MaxInputBytes <= 0 {
		return fmt.Errorf("%w: positive batch, row, column, cell and input limits are required", ErrInvalidConfiguration)
	}
	if options.XLSXUnzipSizeLimit < 0 || options.XLSXMemoryLimit < 0 ||
		(options.XLSXUnzipSizeLimit > 0 && options.XLSXMemoryLimit > options.XLSXUnzipSizeLimit) {
		return fmt.Errorf("%w: invalid XLSX unzip limits", ErrInvalidConfiguration)
	}
	if format == FormatCSV {
		return validateCSVOptions(options.CSV)
	}
	return nil
}

func validateWriteOptions(format Format, options WriteOptions) error {
	if options.MaxRows == 0 || options.MaxColumns <= 0 || options.MaxCellBytes <= 0 {
		return fmt.Errorf("%w: positive row, column and cell limits are required", ErrInvalidConfiguration)
	}
	if options.Formula < FormulaPolicyReject || options.Formula > FormulaPolicyAllow {
		return fmt.Errorf("%w: formula policy is required", ErrInvalidConfiguration)
	}
	if len(options.Header) > options.MaxColumns {
		return fmt.Errorf("%w: header has %d columns, limit is %d", ErrLimitExceeded, len(options.Header), options.MaxColumns)
	}
	if format == FormatXLSX {
		if options.RowsPerSheet == 0 || options.SheetName == "" {
			return fmt.Errorf("%w: XLSX rows per sheet and sheet name are required", ErrInvalidConfiguration)
		}
	}
	if format == FormatCSV {
		return validateCSVOptions(options.CSV)
	}
	return nil
}

func validateCSVOptions(options CSVOptions) error {
	if options.Comma == 0 {
		return fmt.Errorf("%w: CSV delimiter is required", ErrInvalidConfiguration)
	}
	if options.Comma == options.Comment || options.Comma == '\r' || options.Comma == '\n' ||
		options.Comma == 0xFFFD {
		return fmt.Errorf("%w: invalid CSV delimiter or comment", ErrInvalidConfiguration)
	}
	return nil
}

func validateInputRow(format Format, row Row, options ReadOptions) error {
	if len(row.Values) > options.MaxColumns {
		return &Error{Op: "read", Format: format, Sheet: row.Sheet, Row: row.Number, Err: fmt.Errorf("%w: %d columns exceeds %d", ErrLimitExceeded, len(row.Values), options.MaxColumns)}
	}
	for index, value := range row.Values {
		if len(value) > options.MaxCellBytes {
			return &Error{Op: "read", Format: format, Sheet: row.Sheet, Row: row.Number, Column: index + 1, Err: fmt.Errorf("%w: cell has %d bytes, limit is %d", ErrLimitExceeded, len(value), options.MaxCellBytes)}
		}
	}
	return nil
}

func formatCell(value any) (string, error) {
	switch value := value.(type) {
	case nil:
		return "", nil
	case string:
		return value, nil
	case []byte:
		return string(value), nil
	case bool:
		return strconv.FormatBool(value), nil
	case int:
		return strconv.Itoa(value), nil
	case int8:
		return strconv.FormatInt(int64(value), 10), nil
	case int16:
		return strconv.FormatInt(int64(value), 10), nil
	case int32:
		return strconv.FormatInt(int64(value), 10), nil
	case int64:
		return strconv.FormatInt(value, 10), nil
	case uint:
		return strconv.FormatUint(uint64(value), 10), nil
	case uint8:
		return strconv.FormatUint(uint64(value), 10), nil
	case uint16:
		return strconv.FormatUint(uint64(value), 10), nil
	case uint32:
		return strconv.FormatUint(uint64(value), 10), nil
	case uint64:
		return strconv.FormatUint(value, 10), nil
	case float32:
		return strconv.FormatFloat(float64(value), 'g', -1, 32), nil
	case float64:
		return strconv.FormatFloat(value, 'g', -1, 64), nil
	case time.Time:
		return value.Format(time.RFC3339Nano), nil
	default:
		return "", fmt.Errorf("%w: unsupported cell type %T", ErrInvalidConfiguration, value)
	}
}

func protectFormula(value string, policy FormulaPolicy) (string, error) {
	if value == "" {
		return value, nil
	}
	switch value[0] {
	case '=', '+', '-', '@':
		switch policy {
		case FormulaPolicyReject:
			return "", ErrFormulaInjection
		case FormulaPolicyEscape:
			return "'" + value, nil
		case FormulaPolicyAllow:
			return value, nil
		}
	}
	return value, nil
}

func exportCells(format Format, sheet string, row uint64, cells []Cell, options WriteOptions, typed bool) ([]any, error) {
	if len(cells) > options.MaxColumns {
		return nil, &Error{Op: "write", Format: format, Sheet: sheet, Row: row, Err: fmt.Errorf("%w: %d columns exceeds %d", ErrLimitExceeded, len(cells), options.MaxColumns)}
	}
	values := make([]any, len(cells))
	for index, cell := range cells {
		value, err := formatCell(cell.Value)
		if err != nil {
			return nil, &Error{Op: "write", Format: format, Sheet: sheet, Row: row, Column: index + 1, Err: err}
		}
		if len(value) > options.MaxCellBytes {
			return nil, &Error{Op: "write", Format: format, Sheet: sheet, Row: row, Column: index + 1, Err: fmt.Errorf("%w: cell has %d bytes, limit is %d", ErrLimitExceeded, len(value), options.MaxCellBytes)}
		}
		protected := value
		switch cell.Value.(type) {
		case string, []byte:
			var err error
			protected, err = protectFormula(value, options.Formula)
			if err != nil {
				return nil, &Error{Op: "write", Format: format, Sheet: sheet, Row: row, Column: index + 1, Err: err}
			}
		}
		if typed && protected == value {
			values[index] = cell.Value
		} else {
			values[index] = protected
		}
	}
	return values, nil
}

func closeWithError(primary error, closer io.Closer) error {
	if closer == nil {
		return primary
	}
	if closeErr := closer.Close(); closeErr != nil {
		return errors.Join(primary, closeErr)
	}
	return primary
}
