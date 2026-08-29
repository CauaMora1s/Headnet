// Package logging builds the structured loggers used by every Headnet
// component.
//
// It is a thin layer over log/slog rather than a logging framework. The value
// it adds is consistency, which is what makes logs useful during an incident:
//
//   - One configuration surface (level and format) shared by server, daemon,
//     CLI and relay.
//   - Request IDs travel in the context and are attached automatically, so a
//     handler cannot forget to include one.
//   - JSON by default in production, human-readable text in development.
//
// Nothing in Headnet may log private keys, passwords, session tokens, setup
// keys or OIDC client secrets. Configuration is logged through
// config.Redact; anything else that carries secret material must be reduced
// to a non-reversible reference (an ID or a fingerprint) before it reaches a
// log call. See docs/security/key-management.md.
package logging

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"strings"
)

// Format selects the encoding of a log stream.
type Format string

const (
	// FormatJSON emits one JSON object per line. This is the default for
	// anything running as a service, because it is what log shippers parse.
	FormatJSON Format = "json"
	// FormatText emits key=value pairs, which are easier to read directly in
	// a terminal during development.
	FormatText Format = "text"
)

// Formats lists the accepted values, for validation messages and CLI help.
func Formats() []Format { return []Format{FormatJSON, FormatText} }

// Levels lists the accepted level names, from most to least verbose.
func Levels() []string { return []string{"debug", "info", "warn", "error"} }

// Options configures a logger.
type Options struct {
	// Level is one of the names returned by Levels. Empty means "info".
	Level string
	// Format is json or text. Empty means json.
	Format Format
	// AddSource records the file and line of each call site. It is useful
	// while developing and adds noise and cost in production.
	AddSource bool
	// Output is where records are written. Empty means os.Stderr; callers
	// pass a buffer in tests.
	Output io.Writer
}

// ParseLevel converts a level name to a slog.Level, case-insensitively.
func ParseLevel(name string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "debug":
		return slog.LevelDebug, nil
	case "", "info":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("unknown log level %q (expected one of %s)",
			name, strings.Join(Levels(), ", "))
	}
}

// ParseFormat converts a format name to a Format, case-insensitively.
func ParseFormat(name string) (Format, error) {
	switch Format(strings.ToLower(strings.TrimSpace(name))) {
	case "", FormatJSON:
		return FormatJSON, nil
	case FormatText:
		return FormatText, nil
	default:
		return "", fmt.Errorf("unknown log format %q (expected json or text)", name)
	}
}

// New builds a logger. It returns an error for an unusable configuration
// rather than silently falling back, so a typo in the config file surfaces at
// start-up instead of halfway through an incident.
func New(opts Options) (*slog.Logger, error) {
	level, err := ParseLevel(opts.Level)
	if err != nil {
		return nil, err
	}
	format, err := ParseFormat(string(opts.Format))
	if err != nil {
		return nil, err
	}
	out := opts.Output
	if out == nil {
		return nil, fmt.Errorf("logging: Output must be set")
	}

	handlerOpts := &slog.HandlerOptions{Level: level, AddSource: opts.AddSource}

	var handler slog.Handler
	switch format {
	case FormatText:
		handler = slog.NewTextHandler(out, handlerOpts)
	default:
		handler = slog.NewJSONHandler(out, handlerOpts)
	}

	return slog.New(&contextHandler{root: handler, inner: handler}), nil
}

// Discard returns a logger that drops every record. Tests that do not assert
// on log output use it to keep `go test` readable.
func Discard() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

// contextKey is unexported so no other package can collide with our keys.
type contextKey int

const (
	requestIDKey contextKey = iota
	loggerKey
)

// RequestIDAttr is the attribute name under which request IDs are recorded.
// It is exported so tests and log queries can refer to one canonical string.
const RequestIDAttr = "request_id"

// WithRequestID returns a context carrying a request ID. The HTTP middleware
// sets it once per request; everything downstream inherits it for free.
func WithRequestID(ctx context.Context, id string) context.Context {
	if id == "" {
		return ctx
	}
	return context.WithValue(ctx, requestIDKey, id)
}

// RequestIDFrom returns the request ID in the context, or "" if there is none.
func RequestIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey).(string)
	return id
}

// WithLogger returns a context carrying a logger, typically one already
// decorated with request-scoped attributes.
func WithLogger(ctx context.Context, logger *slog.Logger) context.Context {
	if logger == nil {
		return ctx
	}
	return context.WithValue(ctx, loggerKey, logger)
}

// FromContext returns the logger stored in the context.
//
// When there is none it returns a discarding logger rather than nil or a
// global default. A missing logger is a wiring bug, and silently writing to
// stderr from library code would hide it; dropping the record keeps the
// failure contained and keeps callers free of nil checks.
func FromContext(ctx context.Context) *slog.Logger {
	if logger, ok := ctx.Value(loggerKey).(*slog.Logger); ok && logger != nil {
		return logger
	}
	return Discard()
}

// contextHandler attaches context-scoped attributes to every record, so a
// handler cannot forget to include the request ID it was given.
//
// The request ID is always written at the top level of the record, never
// nested inside a group opened with WithGroup. That matters because log
// queries and dashboards address it by a fixed path: if the attribute moved to
// "http.request_id" the moment a component grouped its own fields, correlating
// a request across components would silently break.
type contextHandler struct {
	// root is the underlying handler with no attributes or groups applied.
	root slog.Handler
	// inner is root with every recorded operation already applied. It serves
	// the fast path, where no group is open.
	inner slog.Handler
	// ops records the WithAttrs and WithGroup calls made on this chain so
	// they can be replayed on top of a root-level request ID.
	ops []handlerOp
	// grouped is true once WithGroup has been called anywhere in the chain.
	grouped bool
}

// handlerOp is one recorded decoration: either a group or a set of attributes.
type handlerOp struct {
	group string
	attrs []slog.Attr
}

func (h *contextHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

func (h *contextHandler) Handle(ctx context.Context, record slog.Record) error {
	id := RequestIDFrom(ctx)
	if id == "" {
		return h.inner.Handle(ctx, record)
	}
	if !h.grouped {
		// Fast path: with no group open, appending to the record already puts
		// the attribute at the top level. This covers essentially every call
		// site, so the replay below stays off the hot path.
		record.AddAttrs(slog.String(RequestIDAttr, id))
		return h.inner.Handle(ctx, record)
	}

	// A group is open, so apply the request ID to the bare root handler and
	// replay the recorded decorations on top of it.
	target := h.root.WithAttrs([]slog.Attr{slog.String(RequestIDAttr, id)})
	for _, op := range h.ops {
		if op.group != "" {
			target = target.WithGroup(op.group)
			continue
		}
		target = target.WithAttrs(op.attrs)
	}
	return target.Handle(ctx, record)
}

func (h *contextHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	// slog requires that an empty call returns the receiver unchanged.
	if len(attrs) == 0 {
		return h
	}
	return &contextHandler{
		root:  h.root,
		inner: h.inner.WithAttrs(attrs),
		// Clip before appending so sibling handlers derived from the same
		// parent cannot overwrite each other's recorded operations.
		ops:     append(slices.Clip(h.ops), handlerOp{attrs: attrs}),
		grouped: h.grouped,
	}
}

func (h *contextHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	return &contextHandler{
		root:    h.root,
		inner:   h.inner.WithGroup(name),
		ops:     append(slices.Clip(h.ops), handlerOp{group: name}),
		grouped: true,
	}
}
