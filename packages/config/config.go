// SPDX-License-Identifier: Apache-2.0
// Copyright The Headnet Authors

// Package config loads layered configuration for every Headnet component.
//
// Precedence, from lowest to highest:
//
//  1. Defaults compiled into the destination struct by the caller.
//  2. A YAML configuration file.
//  3. Environment variables.
//  4. Command-line flags, applied by the caller after Load returns.
//
// Two properties matter more than convenience here:
//
//   - Unknown YAML keys are a hard error. A typo in a security-relevant
//     setting must not silently fall back to a permissive default; an
//     operator who writes "lisen: :8080" deserves to be told.
//   - Secrets never travel through the configuration file in the examples we
//     ship, and fields tagged secret:"true" are masked by Redact so that
//     start-up logging and `headnet-server -print-config` cannot leak them.
package config

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/goccy/go-yaml"
)

// Validator is implemented by configuration structs that can check their own
// coherence. Load calls Validate after every layer has been applied, so a
// validation error always reflects the effective configuration rather than an
// intermediate state.
type Validator interface {
	Validate() error
}

// Options controls how a configuration is assembled.
type Options struct {
	// EnvPrefix is prepended to every env tag, separated by an underscore.
	// An empty prefix disables environment overlaying entirely, which is what
	// tests usually want.
	EnvPrefix string
	// Getenv looks up an environment variable. It defaults to os.Getenv and
	// exists so tests do not have to mutate the real process environment.
	Getenv func(string) string
}

func (o Options) getenv(key string) string {
	if o.Getenv != nil {
		return o.Getenv(key)
	}
	return os.Getenv(key)
}

// ErrFileNotFound reports that the requested configuration file is absent.
// Callers decide whether that is fatal: a server started with an explicit
// -config flag should fail, while one relying on defaults should not.
var ErrFileNotFound = errors.New("configuration file not found")

// Load populates dst from the given file and the environment.
//
// dst must be a non-nil pointer to a struct, pre-populated with defaults. When
// path is empty the file layer is skipped. When path is set but missing, Load
// returns an error wrapping ErrFileNotFound.
func Load(path string, dst any, opts Options) error {
	rv := reflect.ValueOf(dst)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return fmt.Errorf("config: destination must be a non-nil pointer, got %T", dst)
	}
	if rv.Elem().Kind() != reflect.Struct {
		return fmt.Errorf("config: destination must point to a struct, got %T", dst)
	}

	if path != "" {
		if err := loadFile(path, dst); err != nil {
			return err
		}
	}
	if opts.EnvPrefix != "" {
		if err := applyEnv(rv.Elem(), opts.EnvPrefix, opts); err != nil {
			return err
		}
	}
	if v, ok := dst.(Validator); ok {
		if err := v.Validate(); err != nil {
			return fmt.Errorf("invalid configuration: %w", err)
		}
	}
	return nil
}

// loadFile overlays a YAML document onto dst, rejecting unknown keys.
func loadFile(path string, dst any) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("%w: %s", ErrFileNotFound, path)
		}
		return fmt.Errorf("reading configuration file %s: %w", path, err)
	}
	// A zero-length file is a legitimate way to say "use every default".
	if len(strings.TrimSpace(string(raw))) == 0 {
		return nil
	}
	if err := yaml.UnmarshalWithOptions(raw, dst, yaml.DisallowUnknownField()); err != nil {
		return fmt.Errorf("parsing configuration file %s: %w", path, err)
	}
	return nil
}

// ApplyEnv overlays environment variables onto an already-populated struct.
// It is exported so that a component can re-apply the environment after
// merging in another source.
func ApplyEnv(dst any, opts Options) error {
	rv := reflect.ValueOf(dst)
	if rv.Kind() != reflect.Pointer || rv.IsNil() || rv.Elem().Kind() != reflect.Struct {
		return fmt.Errorf("config: destination must be a non-nil pointer to a struct, got %T", dst)
	}
	if opts.EnvPrefix == "" {
		return nil
	}
	return applyEnv(rv.Elem(), opts.EnvPrefix, opts)
}

// applyEnv walks a struct, overwriting any field whose env variable is set.
//
// A variable that is set but empty is treated as "not set". Distinguishing the
// two would let an operator accidentally blank out a required setting by
// exporting an empty shell variable, which is a far more common mistake than
// deliberately wanting an empty string.
func applyEnv(v reflect.Value, prefix string, opts Options) error {
	t := v.Type()
	for i := range t.NumField() {
		field := t.Field(i)
		if !field.IsExported() {
			continue
		}
		fv := v.Field(i)

		// Recurse into nested configuration sections.
		if fv.Kind() == reflect.Struct && fv.Type() != reflect.TypeOf(time.Time{}) {
			if err := applyEnv(fv, prefix, opts); err != nil {
				return err
			}
			continue
		}

		tag := field.Tag.Get("env")
		if tag == "" || tag == "-" {
			continue
		}
		key := prefix + "_" + tag
		raw := strings.TrimSpace(opts.getenv(key))
		if raw == "" {
			continue
		}
		if err := setValue(fv, raw); err != nil {
			return fmt.Errorf("environment variable %s: %w", key, err)
		}
	}
	return nil
}

// setValue parses a string into a field, reporting a message an operator can
// act on when the syntax is wrong.
func setValue(fv reflect.Value, raw string) error {
	// time.Duration is an int64 underneath, so it must be matched by type
	// before the generic integer case.
	if fv.Type() == reflect.TypeOf(time.Duration(0)) {
		d, err := time.ParseDuration(raw)
		if err != nil {
			return fmt.Errorf("%q is not a duration (expected a value such as 30s, 5m or 1h)", raw)
		}
		fv.SetInt(int64(d))
		return nil
	}

	switch fv.Kind() {
	case reflect.String:
		fv.SetString(raw)
	case reflect.Bool:
		b, err := strconv.ParseBool(raw)
		if err != nil {
			return fmt.Errorf("%q is not a boolean (expected true or false)", raw)
		}
		fv.SetBool(b)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		n, err := strconv.ParseInt(raw, 10, fv.Type().Bits())
		if err != nil {
			return fmt.Errorf("%q is not an integer", raw)
		}
		fv.SetInt(n)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		n, err := strconv.ParseUint(raw, 10, fv.Type().Bits())
		if err != nil {
			return fmt.Errorf("%q is not a non-negative integer", raw)
		}
		fv.SetUint(n)
	case reflect.Float32, reflect.Float64:
		f, err := strconv.ParseFloat(raw, fv.Type().Bits())
		if err != nil {
			return fmt.Errorf("%q is not a number", raw)
		}
		fv.SetFloat(f)
	case reflect.Slice:
		if fv.Type().Elem().Kind() != reflect.String {
			return fmt.Errorf("unsupported slice element type %s", fv.Type().Elem())
		}
		parts := strings.Split(raw, ",")
		out := make([]string, 0, len(parts))
		for _, p := range parts {
			if p = strings.TrimSpace(p); p != "" {
				out = append(out, p)
			}
		}
		fv.Set(reflect.ValueOf(out))
	default:
		return fmt.Errorf("unsupported field type %s", fv.Type())
	}
	return nil
}
