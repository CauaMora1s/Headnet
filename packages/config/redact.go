// SPDX-License-Identifier: Apache-2.0
// Copyright The Headnet Authors

package config

import (
	"fmt"
	"reflect"
	"strings"
	"time"
)

// Mask is what a populated secret is replaced with.
const Mask = "***"

// Redact renders a configuration struct as a plain map with every field tagged
// secret:"true" masked.
//
// The distinction between a set and an unset secret is preserved: a populated
// secret becomes Mask, an empty one stays empty. That is deliberate. The most
// common configuration mistake is forgetting to supply a secret at all, and an
// operator staring at a redacted dump needs to be able to tell "it is set, I
// just cannot see it" from "it is missing".
//
// Use this for start-up logging, for a `-print-config` flag, and for anything
// that puts configuration in front of a human or a bug report.
func Redact(v any) (map[string]any, error) {
	rv := reflect.ValueOf(v)
	for rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			return nil, fmt.Errorf("config: cannot redact a nil %T", v)
		}
		rv = rv.Elem()
	}
	if rv.Kind() != reflect.Struct {
		return nil, fmt.Errorf("config: Redact requires a struct, got %T", v)
	}
	out, err := redactStruct(rv)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func redactStruct(v reflect.Value) (map[string]any, error) {
	t := v.Type()
	out := make(map[string]any, t.NumField())

	for i := range t.NumField() {
		field := t.Field(i)
		if !field.IsExported() {
			continue
		}
		name := fieldName(field)
		if name == "" {
			continue
		}
		fv := v.Field(i)

		if isSecret(field) {
			out[name] = maskSecret(fv)
			continue
		}

		value, err := redactValue(fv)
		if err != nil {
			return nil, fmt.Errorf("field %s: %w", field.Name, err)
		}
		out[name] = value
	}
	return out, nil
}

func redactValue(fv reflect.Value) (any, error) {
	switch {
	case fv.Kind() == reflect.Pointer:
		if fv.IsNil() {
			return nil, nil
		}
		return redactValue(fv.Elem())

	case fv.Type() == reflect.TypeOf(time.Duration(0)):
		// Render durations the way an operator writes them in YAML.
		return time.Duration(fv.Int()).String(), nil

	case fv.Type() == reflect.TypeOf(time.Time{}):
		return fv.Interface().(time.Time).Format(time.RFC3339), nil

	case fv.Kind() == reflect.Struct:
		return redactStruct(fv)

	case fv.Kind() == reflect.Slice || fv.Kind() == reflect.Array:
		items := make([]any, fv.Len())
		for i := range fv.Len() {
			item, err := redactValue(fv.Index(i))
			if err != nil {
				return nil, err
			}
			items[i] = item
		}
		return items, nil

	default:
		return fv.Interface(), nil
	}
}

// maskSecret replaces a populated secret while keeping an unset one visible.
func maskSecret(fv reflect.Value) any {
	if fv.Kind() == reflect.Pointer {
		if fv.IsNil() {
			return nil
		}
		return maskSecret(fv.Elem())
	}
	if fv.IsZero() {
		// Not configured: show the zero value so the gap is obvious.
		return fv.Interface()
	}
	return Mask
}

// isSecret reports whether a field carries secret material.
func isSecret(field reflect.StructField) bool {
	return strings.EqualFold(field.Tag.Get("secret"), "true")
}

// fieldName resolves the key a field is rendered under, preferring the yaml
// tag so that a redacted dump lines up with the configuration file an operator
// actually wrote. Returns "" for fields explicitly excluded with yaml:"-".
func fieldName(field reflect.StructField) string {
	tag := field.Tag.Get("yaml")
	if tag == "-" {
		return ""
	}
	if name, _, _ := strings.Cut(tag, ","); name != "" {
		return name
	}
	return strings.ToLower(field.Name)
}
