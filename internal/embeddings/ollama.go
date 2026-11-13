// Package embeddings implements an opt-in local CPU Ollama embedding adapter.
package embeddings

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/AntonioIliev19/conmesh/internal/security"
)

var ErrUnavailable = errors.New("SEMANTIC_UNAVAILABLE: opt in and configure an installed local CPU runtime/model")
var ErrIdentity = errors.New("SEMANTIC_IDENTITY: pinned local model, license or embedding response did not match")

const Dimensions = 768
const MaxBatch = 8
const MaxInputBytes = 32 << 10
const MaxBatchBytes = 128 << 10

var hexDigest = regexp.MustCompile(`^[a-f0-9]{64}$`)

type Settings struct {
	Endpoint      string `json:"endpoint"`
	Model         string `json:"model"`
	ModelDigest   string `json:"model_digest"`
	LicenseDigest string `json:"license_digest"`
	Consent       bool   `json:"consent"`
	LocalOnly     bool   `json:"local_only"`
}

func (s Settings) Validate() error {
	u, err := url.Parse(s.Endpoint)
	if err != nil || u.Scheme != "http" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" || u.Opaque != "" {
		return ErrIdentity
	}
	addr, err := netip.ParseAddr(u.Hostname())
	if err != nil || !addr.IsLoopback() || addr.Zone() != "" {
		return ErrIdentity
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil || port < 1 || port > 65535 {
		return ErrIdentity
	}
	if s.Model != "nomic-embed-text:v1.5" || !hexDigest.MatchString(s.ModelDigest) || !hexDigest.MatchString(s.LicenseDigest) || !s.Consent || !s.LocalOnly {
		return ErrIdentity
	}
	return nil
}
func digest(data []byte) string { s := sha256.Sum256(data); return hex.EncodeToString(s[:]) }
func (s Settings) Identity() string {
	// Prefixes, dimensions and fixed CPU inference settings are part of the cache identity.
	return digest([]byte("ollama-nomic-v1:768:search_document:search_query:num_gpu=0:num_ctx=8192:num_thread=2:truncate=false:" + s.Model + ":" + s.ModelDigest + ":" + s.LicenseDigest))
}

type Client struct {
	settings Settings
	http     *http.Client
}

func New(s Settings) (*Client, error) {
	if err := s.Validate(); err != nil {
		return nil, err
	}
	transport := &http.Transport{Proxy: nil, DisableKeepAlives: true, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return nil, ErrIdentity
		}
		ip, err := netip.ParseAddr(host)
		if err != nil || !ip.IsLoopback() || ip.Zone() != "" {
			return nil, ErrIdentity
		}
		return (&net.Dialer{Timeout: 2 * time.Second}).DialContext(ctx, network, address)
	}}
	return &Client{s, &http.Client{Transport: transport, Timeout: 60 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return ErrIdentity }}}, nil
}
func (c *Client) request(ctx context.Context, method, path string, body any, out any) error {
	var data []byte
	if body != nil {
		data, _ = json.Marshal(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.settings.Endpoint+path, bytes.NewReader(data))
	if err != nil {
		return ErrIdentity
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return ErrUnavailable
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return ErrUnavailable
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
	if err != nil || len(raw) > 1<<20 || security.CheckJSON(raw) != nil {
		return ErrIdentity
	}
	if json.Unmarshal(raw, out) != nil {
		return ErrIdentity
	}
	return nil
}
func (c *Client) verify(ctx context.Context) error {
	var tags struct {
		Models []struct {
			Name        string `json:"name"`
			Model       string `json:"model"`
			Digest      string `json:"digest"`
			RemoteHost  string `json:"remote_host"`
			RemoteModel string `json:"remote_model"`
		} `json:"models"`
	}
	if err := c.request(ctx, "GET", "/api/tags", nil, &tags); err != nil {
		return err
	}
	found := 0
	for _, m := range tags.Models {
		if m.Name == c.settings.Model || m.Model == c.settings.Model {
			found++
			if m.Digest != c.settings.ModelDigest || m.RemoteHost != "" || m.RemoteModel != "" {
				return ErrIdentity
			}
		}
	}
	if found != 1 {
		return ErrIdentity
	}
	var show struct {
		License      string                     `json:"license"`
		RemoteHost   string                     `json:"remote_host"`
		RemoteModel  string                     `json:"remote_model"`
		Capabilities []string                   `json:"capabilities"`
		Info         map[string]json.RawMessage `json:"model_info"`
	}
	if err := c.request(ctx, "POST", "/api/show", map[string]string{"model": c.settings.Model}, &show); err != nil {
		return err
	}
	if show.RemoteHost != "" || show.RemoteModel != "" || digest([]byte(show.License)) != c.settings.LicenseDigest {
		return ErrIdentity
	}
	capable := false
	for _, s := range show.Capabilities {
		if s == "embedding" {
			capable = true
		}
	}
	if !capable {
		return ErrIdentity
	}
	var dims int
	if json.Unmarshal(show.Info["nomic-bert.embedding_length"], &dims) != nil || dims != Dimensions {
		return ErrIdentity
	}
	return nil
}

// Embed verifies identity before transmitting content. The user-managed local
// runtime is trusted to honor local-only configuration and the pinned identity.
func (c *Client) Embed(ctx context.Context, texts []string, query bool) ([][]float32, error) {
	return c.EmbedChecked(ctx, texts, query, nil)
}

// EmbedChecked reauthorizes a source batch after runtime identity checks and
// immediately before transmission; it never accepts executable configuration.
func (c *Client) EmbedChecked(ctx context.Context, texts []string, query bool, authorize func() error) ([][]float32, error) {
	if len(texts) == 0 || len(texts) > MaxBatch {
		return nil, ErrIdentity
	}
	total := 0
	input := make([]string, len(texts))
	prefix := "search_document: "
	if query {
		prefix = "search_query: "
	}
	for i, s := range texts {
		total += len(s)
		if len(s) == 0 || len(s) > MaxInputBytes || !utf8.ValidString(s) || security.SensitiveContent([]byte(s)) || strings.ContainsRune(s, 0) {
			return nil, security.ErrDenied
		}
		input[i] = prefix + s
	}
	if total > MaxBatchBytes {
		return nil, ErrIdentity
	}
	if err := c.verify(ctx); err != nil {
		return nil, err
	}
	if authorize != nil {
		if err := authorize(); err != nil {
			return nil, err
		}
	}
	var out struct {
		Model       string      `json:"model"`
		Embeddings  [][]float32 `json:"embeddings"`
		RemoteHost  string      `json:"remote_host"`
		RemoteModel string      `json:"remote_model"`
	}
	body := map[string]any{"model": c.settings.Model, "input": input, "truncate": false, "keep_alive": "0s", "options": map[string]int{"num_gpu": 0, "num_ctx": 8192, "num_thread": 2}}
	if err := c.request(ctx, "POST", "/api/embed", body, &out); err != nil {
		return nil, err
	}
	if out.Model != c.settings.Model || out.RemoteHost != "" || out.RemoteModel != "" || len(out.Embeddings) != len(texts) {
		return nil, ErrIdentity
	}
	for _, v := range out.Embeddings {
		if !ValidVector(v) {
			return nil, ErrIdentity
		}
	}
	if err := c.verify(ctx); err != nil {
		return nil, err
	}
	return out.Embeddings, nil
}
func ValidVector(v []float32) bool {
	if len(v) != Dimensions {
		return false
	}
	var norm float64
	for _, x := range v {
		if math.IsNaN(float64(x)) || math.IsInf(float64(x), 0) {
			return false
		}
		norm += float64(x) * float64(x)
	}
	return norm > 0 && !math.IsInf(norm, 0)
}
func Cosine(a, b []float32) float64 {
	var dot, aa, bb float64
	for i, x := range a {
		y := b[i]
		dot += float64(x) * float64(y)
		aa += float64(x) * float64(x)
		bb += float64(y) * float64(y)
	}
	return dot / math.Sqrt(aa*bb)
}
