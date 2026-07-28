package tabular

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"testing"
)

type testRecord struct {
	ID   uint64
	Name string
}

func testEncoder(record testRecord) ([]Cell, error) {
	return []Cell{{Value: record.ID}, {Value: record.Name}}, nil
}

func testDecoder(row Row) (testRecord, error) {
	if len(row.Values) != 2 {
		return testRecord{}, fmt.Errorf("expected 2 columns, got %d", len(row.Values))
	}
	id, err := strconv.ParseUint(row.Values[0], 10, 64)
	if err != nil {
		return testRecord{}, err
	}
	return testRecord{ID: id, Name: row.Values[1]}, nil
}

func csvReadOptions() ReadOptions {
	return ReadOptions{
		BatchSize:     2,
		MaxRows:       10,
		MaxColumns:    4,
		MaxCellBytes:  1024,
		MaxInputBytes: 64 * 1024,
		HeaderRows:    1,
		CSV:           CSVOptions{Comma: ','},
	}
}

func xlsxReadOptions(tempDir string) ReadOptions {
	return ReadOptions{
		BatchSize:          2,
		MaxRows:            10,
		MaxColumns:         4,
		MaxCellBytes:       1024,
		MaxInputBytes:      8 * 1024 * 1024,
		HeaderRows:         1,
		TempDir:            tempDir,
		XLSXUnzipSizeLimit: 32 * 1024 * 1024,
		XLSXMemoryLimit:    1 * 1024 * 1024,
	}
}

func writeOptions(format Format, tempDir string) WriteOptions {
	options := WriteOptions{
		MaxRows:      10,
		MaxColumns:   4,
		MaxCellBytes: 1024,
		RowsPerSheet: 2,
		TempDir:      tempDir,
		SheetName:    "Records",
		Header:       []Cell{{Value: "id"}, {Value: "name"}},
		Formula:      FormulaPolicyEscape,
	}
	if format == FormatCSV {
		options.CSV = CSVOptions{Comma: ','}
	}
	return options
}

func TestCSVRoundTripUsesBoundedBatches(t *testing.T) {
	t.Parallel()
	input := []testRecord{{ID: 1, Name: "alpha,one"}, {ID: 2, Name: "beta"}, {ID: 3, Name: "gamma"}}
	var output bytes.Buffer
	stats, err := Write(context.Background(), FormatCSV, &output, SliceIterator(input), testEncoder, writeOptions(FormatCSV, ""))
	if err != nil {
		t.Fatal(err)
	}
	if stats.Rows != 3 || stats.Sheets != 1 {
		t.Fatalf("unexpected write stats: %+v", stats)
	}

	var batches []Batch[testRecord]
	readStats, err := Read(context.Background(), FormatCSV, strings.NewReader(output.String()), testDecoder, csvReadOptions(), func(_ context.Context, batch Batch[testRecord]) error {
		batches = append(batches, batch)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if readStats.Rows != 3 || readStats.Batches != 2 || len(batches[0].Items) != 2 || len(batches[1].Items) != 1 {
		t.Fatalf("unexpected read result: stats=%+v batches=%+v", readStats, batches)
	}
	if batches[0].Items[0] != input[0] || batches[1].Items[0] != input[2] {
		t.Fatalf("round trip mismatch: %+v", batches)
	}
}

func TestXLSXRoundTripSplitsSheets(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()
	input := []testRecord{{ID: 1, Name: "alpha"}, {ID: 2, Name: "beta"}, {ID: 3, Name: "gamma"}}
	var output bytes.Buffer
	stats, err := Write(context.Background(), FormatXLSX, &output, SliceIterator(input), testEncoder, writeOptions(FormatXLSX, tempDir))
	if err != nil {
		t.Fatal(err)
	}
	if stats.Rows != 3 || stats.Sheets != 2 {
		t.Fatalf("unexpected write stats: %+v", stats)
	}

	options := xlsxReadOptions(tempDir)
	var records []testRecord
	readStats, err := Read(context.Background(), FormatXLSX, bytes.NewReader(output.Bytes()), testDecoder, options, func(_ context.Context, batch Batch[testRecord]) error {
		records = append(records, batch.Items...)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if readStats.Rows != 3 || readStats.Sheets != 2 {
		t.Fatalf("unexpected read stats: %+v", readStats)
	}
	for index := range input {
		if records[index] != input[index] {
			t.Fatalf("record %d mismatch: got %+v want %+v", index, records[index], input[index])
		}
	}
}

func TestReadLegacyXLSInBatches(t *testing.T) {
	t.Parallel()
	source, err := os.Open("testdata/legacy-table.xls")
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	options := ReadOptions{
		BatchSize:     3,
		MaxRows:       100,
		MaxColumns:    256,
		MaxCellBytes:  32 * 1024,
		MaxInputBytes: 64 * 1024,
		TempDir:       t.TempDir(),
	}
	maxBatch := 0
	stats, err := Read(context.Background(), FormatXLS, source, func(row Row) (Row, error) {
		return row, nil
	}, options, func(_ context.Context, batch Batch[Row]) error {
		if len(batch.Items) > maxBatch {
			maxBatch = len(batch.Items)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if stats.Rows == 0 || stats.Sheets == 0 || maxBatch > options.BatchSize {
		t.Fatalf("unexpected XLS stats: %+v max batch=%d", stats, maxBatch)
	}
}

func TestFormulaPolicyRejectsAndEscapes(t *testing.T) {
	t.Parallel()
	record := testRecord{ID: 1, Name: "=HYPERLINK(\"bad\")"}
	options := writeOptions(FormatCSV, "")
	options.Formula = FormulaPolicyReject
	_, err := Write(context.Background(), FormatCSV, io.Discard, SliceIterator([]testRecord{record}), testEncoder, options)
	if !errors.Is(err, ErrFormulaInjection) {
		t.Fatalf("expected formula error, got %v", err)
	}

	options.Formula = FormulaPolicyEscape
	var output bytes.Buffer
	if _, err := Write(context.Background(), FormatCSV, &output, SliceIterator([]testRecord{record}), testEncoder, options); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "'=HYPERLINK") {
		t.Fatalf("formula was not escaped: %q", output.String())
	}
}

func TestReadRejectsInputAndCellLimits(t *testing.T) {
	t.Parallel()
	options := csvReadOptions()
	options.HeaderRows = 0
	options.MaxInputBytes = 4
	_, err := Read(context.Background(), FormatCSV, strings.NewReader("12345"), testDecoder, options, func(context.Context, Batch[testRecord]) error { return nil })
	if !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("expected input limit error, got %v", err)
	}

	options.MaxInputBytes = 1024
	options.MaxCellBytes = 3
	_, err = Read(context.Background(), FormatCSV, strings.NewReader("1,large\n"), testDecoder, options, func(context.Context, Batch[testRecord]) error { return nil })
	if !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("expected cell limit error, got %v", err)
	}
}

func TestReadHonorsCancellation(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	options := csvReadOptions()
	options.HeaderRows = 0
	_, err := Read(ctx, FormatCSV, strings.NewReader("1,alpha\n"), testDecoder, options, func(context.Context, Batch[testRecord]) error { return nil })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation, got %v", err)
	}
}

func TestLegacyXLSWriteFailsClosed(t *testing.T) {
	t.Parallel()
	options := writeOptions(FormatXLS, "")
	_, err := Write(context.Background(), FormatXLS, io.Discard, SliceIterator([]testRecord{{ID: 1}}), testEncoder, options)
	if !errors.Is(err, ErrLegacyXLSWrite) {
		t.Fatalf("expected legacy XLS error, got %v", err)
	}
}

func TestReadRejectsMalformedLegacyXLS(t *testing.T) {
	t.Parallel()
	options := ReadOptions{
		BatchSize:     2,
		MaxRows:       10,
		MaxColumns:    256,
		MaxCellBytes:  1024,
		MaxInputBytes: 1024,
		TempDir:       t.TempDir(),
	}
	_, err := Read(context.Background(), FormatXLS, strings.NewReader("not an OLE workbook"), func(row Row) (Row, error) {
		return row, nil
	}, options, func(context.Context, Batch[Row]) error { return nil })
	if err == nil {
		t.Fatal("malformed XLS must fail closed")
	}
}

func TestReadRejectsMissingXLSXSheet(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()
	var output bytes.Buffer
	if _, err := Write(context.Background(), FormatXLSX, &output, SliceIterator([]testRecord{{ID: 1, Name: "one"}}), testEncoder, writeOptions(FormatXLSX, tempDir)); err != nil {
		t.Fatal(err)
	}
	options := xlsxReadOptions(tempDir)
	options.SheetNames = []string{"Missing"}
	_, err := Read(context.Background(), FormatXLSX, bytes.NewReader(output.Bytes()), testDecoder, options, func(context.Context, Batch[testRecord]) error { return nil })
	if err == nil {
		t.Fatal("missing selected sheet must fail")
	}
}

func TestCSVStreams300KRows(t *testing.T) {
	if testing.Short() {
		t.Skip("300k record integration test")
	}
	const rows = uint64(300_000)
	path := t.TempDir() + string(os.PathSeparator) + "records.csv"
	target, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	var current uint64
	iterator := IteratorFunc[testRecord](func(context.Context) (testRecord, bool, error) {
		if current == rows {
			return testRecord{}, false, nil
		}
		current++
		return testRecord{ID: current, Name: "record-" + strconv.FormatUint(current, 10)}, true, nil
	})
	options := writeOptions(FormatCSV, "")
	options.MaxRows = rows
	stats, err := Write(context.Background(), FormatCSV, target, iterator, testEncoder, options)
	err = errors.Join(err, target.Close())
	if err != nil {
		t.Fatal(err)
	}
	if stats.Rows != rows {
		t.Fatalf("wrote %d rows", stats.Rows)
	}

	source, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	readOptions := csvReadOptions()
	readOptions.BatchSize = 2_000
	readOptions.MaxRows = rows
	readOptions.MaxInputBytes = 32 * 1024 * 1024
	maxBatch := 0
	readStats, err := Read(context.Background(), FormatCSV, source, testDecoder, readOptions, func(_ context.Context, batch Batch[testRecord]) error {
		if len(batch.Items) > maxBatch {
			maxBatch = len(batch.Items)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if readStats.Rows != rows || maxBatch != readOptions.BatchSize {
		t.Fatalf("unexpected streaming result: stats=%+v max batch=%d", readStats, maxBatch)
	}
}

func TestXLSXStreams300KRows(t *testing.T) {
	if testing.Short() {
		t.Skip("300k record integration test")
	}
	const rows = uint64(300_000)
	tempDir := t.TempDir()
	path := tempDir + string(os.PathSeparator) + "records.xlsx"
	target, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	var current uint64
	iterator := IteratorFunc[testRecord](func(context.Context) (testRecord, bool, error) {
		if current == rows {
			return testRecord{}, false, nil
		}
		current++
		return testRecord{ID: current, Name: "record-" + strconv.FormatUint(current, 10)}, true, nil
	})
	options := writeOptions(FormatXLSX, tempDir)
	options.MaxRows = rows
	options.RowsPerSheet = rows
	stats, err := Write(context.Background(), FormatXLSX, target, iterator, testEncoder, options)
	err = errors.Join(err, target.Close())
	if err != nil {
		t.Fatal(err)
	}
	if stats.Rows != rows || stats.Sheets != 1 {
		t.Fatalf("unexpected write stats: %+v", stats)
	}

	source, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	readOptions := xlsxReadOptions(tempDir)
	readOptions.BatchSize = 2_000
	readOptions.MaxRows = rows
	readOptions.MaxInputBytes = 128 * 1024 * 1024
	readOptions.XLSXUnzipSizeLimit = 512 * 1024 * 1024
	readOptions.XLSXMemoryLimit = 8 * 1024 * 1024
	maxBatch := 0
	readStats, err := Read(context.Background(), FormatXLSX, source, testDecoder, readOptions, func(_ context.Context, batch Batch[testRecord]) error {
		if len(batch.Items) > maxBatch {
			maxBatch = len(batch.Items)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if readStats.Rows != rows || readStats.Sheets != 1 || maxBatch != readOptions.BatchSize {
		t.Fatalf("unexpected streaming result: stats=%+v max batch=%d", readStats, maxBatch)
	}
}

func BenchmarkCSVWrite300K(b *testing.B) {
	const rows = uint64(300_000)
	options := writeOptions(FormatCSV, "")
	options.MaxRows = rows
	b.ReportAllocs()
	for range b.N {
		var current uint64
		iterator := IteratorFunc[testRecord](func(context.Context) (testRecord, bool, error) {
			if current == rows {
				return testRecord{}, false, nil
			}
			current++
			return testRecord{ID: current, Name: "record"}, true, nil
		})
		if _, err := Write(context.Background(), FormatCSV, io.Discard, iterator, testEncoder, options); err != nil {
			b.Fatal(err)
		}
	}
}
