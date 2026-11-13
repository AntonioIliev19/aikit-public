package config

import (
	"strings"
	"testing"

	"github.com/AntonioIliev19/conmesh/internal/embeddings"
)

func TestOptionalSemanticConfigurationCompatibility(t *testing.T) {
	var old Config
	if err := Decode([]byte(`{"schema_version":1,"harnesses":[],"indexing":{"enabled":true,"profile":"balanced","semantic":false}}`), &old); err != nil || old.Validate() != nil || old.Semantic != nil {
		t.Fatal("old basic config compatibility changed", err)
	}
	settings := &embeddings.Settings{Endpoint: "http://127.0.0.1:11434", Model: "nomic-embed-text:v1.5", ModelDigest: strings.Repeat("a", 64), LicenseDigest: strings.Repeat("b", 64), Consent: true, LocalOnly: true}
	old.Semantic = settings
	if old.Validate() == nil {
		t.Fatal("settings enabled semantic implicitly")
	}
	old.Indexing.Semantic = true
	if err := old.Validate(); err != nil {
		t.Fatal("explicit semantic settings rejected", err)
	}
	old.Indexing.Enabled = false
	if old.Validate() == nil {
		t.Fatal("semantic accepted without indexing opt-in")
	}
	old.Indexing.Enabled = true
	settings.LocalOnly = false
	if old.Validate() == nil {
		t.Fatal("cloud-capable runtime accepted")
	}
	if Decode([]byte(`{"schema_version":1,"schema_version":1}`), &old) == nil {
		t.Fatal("duplicate configuration keys accepted")
	}
}
