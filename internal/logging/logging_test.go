package logging_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/CauaMora1s/Headnet/internal/logging"
)

// newBuffered returns a JSON logger writing into a buffer the test can inspect.
func newBuffered(t *testing.T, opts logging.Options) (*slog.Logger, *bytes.Buffer) {
	t.Helper()
	buf := &bytes.Buffer{}
	opts.Output = buf
	logger, err := logging.New(opts)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	return logger, buf
}

// records decodes every JSON line the logger emitted.
func records(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if line == "" {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("log line is not valid JSON: %q: %v", line, err)
		}
		out = append(out, rec)
	}
	return out
}

func TestParseLevel(t *testing.T) {
	tests := []struct {
		in      string
		want    slog.Level
		wantErr bool
	}{
		{"debug", slog.LevelDebug, false},
		{"DEBUG", slog.LevelDebug, false},
		{"  info  ", slog.LevelInfo, false},
		{"", slog.LevelInfo, false},
		{"warn", slog.LevelWarn, false},
		{"warning", slog.LevelWarn, false},
		{"error", slog.LevelError, false},
		{"verbose", 0, true},
	}
	for _, tt := range tests {
		got, err := logging.ParseLevel(tt.in)
		if tt.wantErr {
			if err == nil {
				t.Errorf("ParseLevel(%q) accepted an unknown level", tt.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseLevel(%q) failed: %v", tt.in, err)
		}
		if got != tt.want {
			t.Errorf("ParseLevel(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

func TestParseLevelErrorListsTheValidValues(t *testing.T) {
	_, err := logging.ParseLevel("verbose")
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, level := range logging.Levels() {
		if !strings.Contains(err.Error(), level) {
			t.Errorf("error %q does not mention the valid level %q", err, level)
		}
	}
}

func TestParseFormat(t *testing.T) {
	for _, in := range []string{"json", "JSON", "", "  json "} {
		got, err := logging.ParseFormat(in)
		if err != nil || got != logging.FormatJSON {
			t.Errorf("ParseFormat(%q) = (%q, %v), want json", in, got, err)
		}
	}
	if got, err := logging.ParseFormat("text"); err != nil || got != logging.FormatText {
		t.Errorf("ParseFormat(\"text\") = (%q, %v), want text", got, err)
	}
	if _, err := logging.ParseFormat("logfmt"); err == nil {
		t.Error("ParseFormat accepted an unknown format")
	}
}

func TestNewRejectsBadConfiguration(t *testing.T) {
	buf := &bytes.Buffer{}
	if _, err := logging.New(logging.Options{Level: "chatty", Output: buf}); err == nil {
		t.Error("New accepted an unknown level")
	}
	if _, err := logging.New(logging.Options{Format: "xml", Output: buf}); err == nil {
		t.Error("New accepted an unknown format")
	}
	if _, err := logging.New(logging.Options{}); err == nil {
		t.Error("New accepted a nil output writer")
	}
}

func TestJSONOutputIsMachineReadable(t *testing.T) {
	logger, buf := newBuffered(t, logging.Options{Format: logging.FormatJSON})
	logger.Info("server started", "port", 8080)

	got := records(t, buf)
	if len(got) != 1 {
		t.Fatalf("expected exactly one record, got %d", len(got))
	}
	if got[0]["msg"] != "server started" {
		t.Errorf("msg = %v, want \"server started\"", got[0]["msg"])
	}
	if got[0]["port"] != float64(8080) {
		t.Errorf("port = %v, want 8080", got[0]["port"])
	}
	if got[0]["level"] != "INFO" {
		t.Errorf("level = %v, want INFO", got[0]["level"])
	}
}

func TestTextOutputIsHumanReadable(t *testing.T) {
	buf := &bytes.Buffer{}
	logger, err := logging.New(logging.Options{Format: logging.FormatText, Output: buf})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	logger.Info("server started", "port", 8080)

	if !strings.Contains(buf.String(), "port=8080") {
		t.Fatalf("text output = %q, want key=value pairs", buf.String())
	}
}

func TestLevelFiltering(t *testing.T) {
	logger, buf := newBuffered(t, logging.Options{Level: "warn"})
	logger.Debug("noisy")
	logger.Info("routine")
	logger.Warn("worth attention")
	logger.Error("broken")

	got := records(t, buf)
	if len(got) != 2 {
		t.Fatalf("emitted %d records at level=warn, want 2", len(got))
	}
	if got[0]["msg"] != "worth attention" || got[1]["msg"] != "broken" {
		t.Fatalf("wrong records survived filtering: %v", got)
	}
}

func TestRequestIDIsAttachedAutomatically(t *testing.T) {
	// The whole point of the context handler: a handler cannot forget to
	// include the request ID, because it never has to remember.
	logger, buf := newBuffered(t, logging.Options{})
	ctx := logging.WithRequestID(context.Background(), "req_ABC123")

	logger.InfoContext(ctx, "handled request")

	got := records(t, buf)
	if got[0][logging.RequestIDAttr] != "req_ABC123" {
		t.Fatalf("%s = %v, want req_ABC123", logging.RequestIDAttr, got[0][logging.RequestIDAttr])
	}
}

func TestRequestIDIsOmittedWhenAbsent(t *testing.T) {
	logger, buf := newBuffered(t, logging.Options{})
	logger.InfoContext(context.Background(), "no request in flight")

	got := records(t, buf)
	if _, present := got[0][logging.RequestIDAttr]; present {
		t.Fatalf("%s was emitted with an empty value: %v", logging.RequestIDAttr, got[0])
	}
}

func TestRequestIDSurvivesWithAttrsAndWithGroup(t *testing.T) {
	// slog.Logger.With and .WithGroup replace the handler; the context
	// decoration has to survive that or it silently stops working as soon as
	// a component adds its own attributes.
	logger, buf := newBuffered(t, logging.Options{})
	ctx := logging.WithRequestID(context.Background(), "req_XYZ")

	logger.With("component", "api").InfoContext(ctx, "with attrs")
	logger.WithGroup("http").InfoContext(ctx, "with group")

	for i, rec := range records(t, buf) {
		if rec[logging.RequestIDAttr] != "req_XYZ" {
			t.Errorf("record %d lost the request ID: %v", i, rec)
		}
	}
}

func TestWithRequestIDIgnoresEmptyValues(t *testing.T) {
	ctx := logging.WithRequestID(context.Background(), "")
	if got := logging.RequestIDFrom(ctx); got != "" {
		t.Fatalf("RequestIDFrom = %q, want empty", got)
	}
}

func TestFromContextReturnsTheStoredLogger(t *testing.T) {
	logger, buf := newBuffered(t, logging.Options{})
	ctx := logging.WithLogger(context.Background(), logger.With("component", "storage"))

	logging.FromContext(ctx).Info("hello")

	got := records(t, buf)
	if got[0]["component"] != "storage" {
		t.Fatalf("component = %v, want the decorated logger to be used", got[0]["component"])
	}
}

func TestFromContextDiscardsWhenUnwired(t *testing.T) {
	// A missing logger is a wiring bug. Dropping the record keeps the failure
	// contained instead of leaking output to stderr from library code, and
	// spares every caller a nil check.
	logger := logging.FromContext(context.Background())
	if logger == nil {
		t.Fatal("FromContext returned nil; callers would panic")
	}
	logger.Error("this must not panic and must not be written anywhere")
}

func TestWithLoggerIgnoresNil(t *testing.T) {
	ctx := logging.WithLogger(context.Background(), nil)
	if logging.FromContext(ctx) == nil {
		t.Fatal("storing a nil logger poisoned the context")
	}
}

func TestAddSourceRecordsCallSite(t *testing.T) {
	logger, buf := newBuffered(t, logging.Options{AddSource: true})
	logger.Info("where am i")

	got := records(t, buf)
	if _, present := got[0]["source"]; !present {
		t.Fatalf("AddSource did not record a source: %v", got[0])
	}
}

func TestDiscardWritesNothing(t *testing.T) {
	// Nothing to capture, but it must neither panic nor be nil.
	logging.Discard().Error("dropped", "key", "value")
}
