// Package output renders command results. Every path runs through redaction, so
// a provider cannot accidentally print a secret regardless of output format.
package output

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/squall-chua/ddc/internal/redact"
)

// Printer writes redacted command output as either text or JSON.
type Printer struct {
	w      io.Writer
	asJSON bool
}

// NewPrinter returns a Printer writing to w. If asJSON is true, callers should
// use JSON; otherwise Text.
func NewPrinter(w io.Writer, asJSON bool) *Printer {
	return &Printer{w: w, asJSON: asJSON}
}

// AsJSON reports whether JSON output was requested.
func (p *Printer) AsJSON() bool { return p.asJSON }

// Text scrubs s and writes it followed by a newline.
func (p *Printer) Text(s string) error {
	_, err := fmt.Fprintln(p.w, redact.Scrub(strings.TrimRight(s, "\n")))
	return err
}

// JSON marshals v, scrubs the encoded form, and writes it.
func (p *Printer) JSON(v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(p.w, redact.Scrub(string(b)))
	return err
}

// Table renders headers and rows as tab-aligned columns. The result is plain
// text; callers pass it to Printer.Text so it too is redacted.
func Table(headers []string, rows [][]string) string {
	var b strings.Builder
	tw := tabwriter.NewWriter(&b, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, strings.Join(headers, "\t"))
	for _, row := range rows {
		fmt.Fprintln(tw, strings.Join(row, "\t"))
	}
	tw.Flush()
	return strings.TrimRight(b.String(), "\n")
}
