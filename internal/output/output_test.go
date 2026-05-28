package output

import (
	"bytes"
	"strings"
	"testing"
)

const leakedToken = "ghp_abcdefghijklmnopqrstuvwxyz0123456789"

func TestTextRedacts(t *testing.T) {
	var buf bytes.Buffer
	p := NewPrinter(&buf, false)
	if err := p.Text("auth token " + leakedToken); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), leakedToken) {
		t.Fatalf("Text leaked secret: %q", buf.String())
	}
}

func TestJSONRedacts(t *testing.T) {
	var buf bytes.Buffer
	p := NewPrinter(&buf, true)
	v := map[string]string{"token": leakedToken, "user": "alice"}
	if err := p.JSON(v); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if strings.Contains(out, leakedToken) {
		t.Fatalf("JSON leaked secret: %q", out)
	}
	if !strings.Contains(out, "alice") {
		t.Fatalf("JSON dropped non-secret field: %q", out)
	}
}

func TestTableFormats(t *testing.T) {
	got := Table([]string{"NAME", "STATUS"}, [][]string{{"nginx", "Running"}, {"redis", "CrashLoopBackOff"}})
	for _, want := range []string{"NAME", "STATUS", "nginx", "CrashLoopBackOff"} {
		if !strings.Contains(got, want) {
			t.Fatalf("table missing %q in:\n%s", want, got)
		}
	}
}
