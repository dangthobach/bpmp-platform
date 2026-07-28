# Tabular

`tabular` is the shared bounded I/O layer for CSV, XLSX and legacy XLS data.
DTO mapping is explicit and generic; no reflection, struct tags or complete
`[]T` materialization is required.

## Capabilities

| Format | Import | Export | Memory model |
|---|---:|---:|---|
| CSV | Yes | Yes | streaming |
| XLSX | Yes | Yes | streaming rows; large XML spills to configured temp storage |
| XLS (BIFF8) | Yes | fail-closed | bounded file spool; legacy parser materializes one workbook |

BIFF8 XLS export is intentionally rejected with `ErrLegacyXLSWrite`. A BIFF8
sheet is limited to 65,536 rows and the available Go writers either materialize
the workbook or are not production-ready. Use XLSX or CSV for bulk export.

## Usage

```go
type Customer struct {
	ID   uint64
	Name string
}

decoder := func(row tabular.Row) (Customer, error) {
	id, err := strconv.ParseUint(row.Values[0], 10, 64)
	if err != nil {
		return Customer{}, err
	}
	return Customer{ID: id, Name: row.Values[1]}, nil
}

stats, err := tabular.Read(
	ctx,
	tabular.FormatCSV,
	input,
	decoder,
	readOptionsFromConfigurationService,
	func(ctx context.Context, batch tabular.Batch[Customer]) error {
		return repository.UpsertBatch(ctx, batch.Items)
	},
)
```

For export, provide an `Iterator[T]` backed by a keyset-paginated database
cursor and an `Encoder[T]`. `SliceIterator` exists only for already bounded
collections.

All operational limits are caller-provided so services can resolve them from
the Configuration Service per tenant. Zero limits are invalid. Import and
export check context cancellation, row/column/cell/input bounds, preserve
source location in typed errors and apply an explicit formula policy.

## Security

- Use `FormulaPolicyReject` for untrusted exports unless business behavior
  explicitly requires escaping or formulas.
- Keep `MaxInputBytes`, XLSX unzip limits and cell limits conservative.
- Put `TempDir` on an encrypted volume with a storage quota.
- Treat XLS as compatibility input. Prefer XLSX or CSV for new integrations.
- Batch handlers must commit durably before returning success.
