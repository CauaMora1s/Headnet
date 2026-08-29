// SPDX-License-Identifier: Apache-2.0
// Copyright The Headnet Authors

package config_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/headnet/headnet/packages/config"
)

type oidcSection struct {
	Issuer       string `yaml:"issuer"`
	ClientID     string `yaml:"client_id"`
	ClientSecret string `yaml:"client_secret" secret:"true"`
}

type redactable struct {
	Listen    string        `yaml:"listen"`
	Timeout   time.Duration `yaml:"timeout"`
	Providers []string      `yaml:"providers"`
	Token     string        `yaml:"token" secret:"true"`
	Empty     string        `yaml:"empty_secret" secret:"true"`
	OIDC      oidcSection   `yaml:"oidc"`
	Hidden    string        `yaml:"-"`
	Untagged  int
	unversed  string //nolint:unused // present to prove unexported fields are skipped
}

func sample() *redactable {
	return &redactable{
		Listen:    ":8080",
		Timeout:   90 * time.Second,
		Providers: []string{"local", "oidc"},
		Token:     "hunter2-super-secret",
		Empty:     "",
		OIDC: oidcSection{
			Issuer:       "https://id.example.com",
			ClientID:     "headnet",
			ClientSecret: "oidc-secret-value",
		},
		Hidden:   "excluded",
		Untagged: 7,
		unversed: "ignored",
	}
}

func TestRedactMasksPopulatedSecrets(t *testing.T) {
	t.Parallel()
	got, err := config.Redact(sample())
	if err != nil {
		t.Fatalf("Redact failed: %v", err)
	}
	if got["token"] != config.Mask {
		t.Errorf("token = %v, want it masked", got["token"])
	}
	nested, ok := got["oidc"].(map[string]any)
	if !ok {
		t.Fatalf("oidc = %T, want a nested map", got["oidc"])
	}
	if nested["client_secret"] != config.Mask {
		t.Errorf("oidc.client_secret = %v, want it masked", nested["client_secret"])
	}
}

func TestRedactKeepsUnsetSecretsVisiblyEmpty(t *testing.T) {
	t.Parallel()
	// An operator reading a redacted dump has to be able to tell "the secret
	// is set, I just cannot see it" from "the secret is missing entirely",
	// because forgetting to supply one is the more common mistake.
	got, err := config.Redact(sample())
	if err != nil {
		t.Fatalf("Redact failed: %v", err)
	}
	if got["empty_secret"] != "" {
		t.Fatalf("empty_secret = %v, want an empty string rather than a mask", got["empty_secret"])
	}
}

func TestRedactPreservesNonSecretValues(t *testing.T) {
	t.Parallel()
	got, err := config.Redact(sample())
	if err != nil {
		t.Fatalf("Redact failed: %v", err)
	}
	if got["listen"] != ":8080" {
		t.Errorf("listen = %v, want :8080", got["listen"])
	}
	if got["timeout"] != "1m30s" {
		t.Errorf("timeout = %v, want the human-readable duration 1m30s", got["timeout"])
	}
	providers, ok := got["providers"].([]any)
	if !ok || len(providers) != 2 || providers[0] != "local" {
		t.Errorf("providers = %v, want [local oidc]", got["providers"])
	}
	nested := got["oidc"].(map[string]any)
	if nested["issuer"] != "https://id.example.com" {
		t.Errorf("oidc.issuer = %v, want it left intact", nested["issuer"])
	}
}

func TestRedactHonoursFieldNaming(t *testing.T) {
	t.Parallel()
	got, err := config.Redact(sample())
	if err != nil {
		t.Fatalf("Redact failed: %v", err)
	}
	if _, present := got["-"]; present {
		t.Error("a field tagged yaml:\"-\" leaked into the output")
	}
	if _, present := got["hidden"]; present {
		t.Error("a field tagged yaml:\"-\" leaked into the output under its field name")
	}
	if got["untagged"] != 7 {
		t.Errorf("untagged = %v, want an untagged field to fall back to its lower-cased name", got["untagged"])
	}
	if _, present := got["unversed"]; present {
		t.Error("an unexported field leaked into the output")
	}
}

func TestRedactedOutputContainsNoSecretMaterial(t *testing.T) {
	t.Parallel()
	// This is the property that actually matters: whatever the shape of the
	// struct, no secret value may survive into something we might log.
	got, err := config.Redact(sample())
	if err != nil {
		t.Fatalf("Redact failed: %v", err)
	}
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshalling the redacted config failed: %v", err)
	}
	for _, secret := range []string{"hunter2-super-secret", "oidc-secret-value"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("redacted output leaked the secret %q:\n%s", secret, encoded)
		}
	}
}

func TestRedactAcceptsValuesAndPointers(t *testing.T) {
	t.Parallel()
	fromPointer, err := config.Redact(sample())
	if err != nil {
		t.Fatalf("Redact(pointer) failed: %v", err)
	}
	fromValue, err := config.Redact(*sample())
	if err != nil {
		t.Fatalf("Redact(value) failed: %v", err)
	}
	if fromPointer["token"] != fromValue["token"] {
		t.Fatal("Redact behaves differently for a pointer and a value")
	}
}

func TestRedactRejectsUnsupportedInput(t *testing.T) {
	t.Parallel()
	if _, err := config.Redact((*redactable)(nil)); err == nil {
		t.Error("Redact accepted a nil pointer")
	}
	if _, err := config.Redact("not a struct"); err == nil {
		t.Error("Redact accepted a non-struct")
	}
}

func TestRedactDoesNotMutateTheSource(t *testing.T) {
	t.Parallel()
	cfg := sample()
	if _, err := config.Redact(cfg); err != nil {
		t.Fatalf("Redact failed: %v", err)
	}
	if cfg.Token != "hunter2-super-secret" || cfg.OIDC.ClientSecret != "oidc-secret-value" {
		t.Fatal("Redact overwrote the live configuration instead of copying it")
	}
}
