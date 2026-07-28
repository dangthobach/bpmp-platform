// Package tabular provides bounded, format-independent batch import and
// streaming export for tabular data.
//
// DTO mapping is explicit through Decoder and Encoder functions. The package
// deliberately avoids reflection and struct tags in the hot path.
package tabular
