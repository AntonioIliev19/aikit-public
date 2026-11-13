package embeddings

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/AntonioIliev19/conmesh/internal/security"
)

// All vectors in this file are mocked HTTP fixtures, not model-quality evidence.
func mock(t *testing.T, remote bool) (*Client, *atomic.Int32) {
	t.Helper()
	calls := new(atomic.Int32)
	license := "Apache-2.0 fixture license"
	modelDigest := strings.Repeat("a", 64)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tags":
			json.NewEncoder(w).Encode(map[string]any{"models": []any{map[string]any{"name": "nomic-embed-text:v1.5", "digest": modelDigest}}})
		case "/api/show":
			host := ""
			if remote {
				host = "https://example.invalid"
			}
			json.NewEncoder(w).Encode(map[string]any{"license": license, "capabilities": []string{"embedding"}, "remote_host": host, "model_info": map[string]int{"nomic-bert.embedding_length": Dimensions}})
		case "/api/embed":
			calls.Add(1)
			var req struct {
				Model    string         `json:"model"`
				Input    []string       `json:"input"`
				Truncate bool           `json:"truncate"`
				Options  map[string]int `json:"options"`
			}
			if json.NewDecoder(r.Body).Decode(&req) != nil || req.Truncate || req.Options["num_gpu"] != 0 || req.Options["num_ctx"] != 8192 {
				t.Error("wrong CPU bounded request")
			}
			vectors := [][]float32{}
			for _, input := range req.Input {
				if !strings.HasPrefix(input, "search_document: ") && !strings.HasPrefix(input, "search_query: ") {
					t.Error("missing task prefix")
				}
				v := make([]float32, Dimensions)
				v[0] = 1
				vectors = append(vectors, v)
			}
			json.NewEncoder(w).Encode(map[string]any{"model": req.Model, "embeddings": vectors})
		default:
			t.Error("unexpected runtime endpoint")
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(server.Close)
	c, err := New(Settings{server.URL, "nomic-embed-text:v1.5", modelDigest, digest([]byte(license)), true, true})
	if err != nil {
		t.Fatal(err)
	}
	return c, calls
}
func TestMockLocalCPUFlowAndPreSendGate(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://192.0.2.1:9")
	c, calls := mock(t, false)
	out, err := c.Embed(context.Background(), []string{"authorized code", "other chunk"}, false)
	if err != nil || len(out) != 2 || calls.Load() != 1 {
		t.Fatal("mock flow failed", err)
	}
	_, err = c.EmbedChecked(context.Background(), []string{"newly ignored source"}, false, func() error { return security.ErrDenied })
	if !errors.Is(err, security.ErrDenied) || calls.Load() != 1 {
		t.Fatal("pre-send reauthorization bypassed", err)
	}
	remote, remoteCalls := mock(t, true)
	if _, err := remote.Embed(context.Background(), []string{"safe text"}, true); !errors.Is(err, ErrIdentity) || remoteCalls.Load() != 0 {
		t.Fatal("cloud metadata transmitted input", err)
	}
	c.settings.LicenseDigest = strings.Repeat("0", 64)
	if _, err := c.Embed(context.Background(), []string{"safe text"}, false); !errors.Is(err, ErrIdentity) || calls.Load() != 1 {
		t.Fatal("license mismatch transmitted input", err)
	}
}
func TestEndpointVectorAndRedirectRejections(t *testing.T) {
	base := Settings{"http://127.0.0.1:11434", "nomic-embed-text:v1.5", strings.Repeat("a", 64), strings.Repeat("b", 64), true, true}
	for _, endpoint := range []string{"http://localhost:11434", "https://127.0.0.1:11434", "http://192.0.2.1:11434", "http://127.0.0.1:11434/", "http://user@127.0.0.1:11434", "http://127.0.0.1:11434?x=1", "http://[::1%25zone]:11434"} {
		bad := base
		bad.Endpoint = endpoint
		if _, err := New(bad); err == nil {
			t.Fatal("unsafe endpoint accepted")
		}
	}
	redirects := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://192.0.2.1:11434/api/tags", 302)
	}))
	defer redirects.Close()
	base.Endpoint = redirects.URL
	c, err := New(base)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Embed(context.Background(), []string{"safe"}, false); err == nil {
		t.Fatal("redirect accepted")
	}
	v := make([]float32, Dimensions)
	if ValidVector(v) {
		t.Fatal("zero vector accepted")
	}
	v[0] = float32(math.NaN())
	if ValidVector(v) {
		t.Fatal("NaN accepted")
	}
	v[0] = float32(math.Inf(1))
	if ValidVector(v) {
		t.Fatal("infinite vector accepted")
	}
	v[0] = 1
	if !ValidVector(v) || Cosine(v, v) != 1 {
		t.Fatal("valid vector rejected")
	}
}
