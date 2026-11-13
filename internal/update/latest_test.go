package update

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLatestVerifiesPlatformManifest(t *testing.T) {
	client, server := release(t, []byte("new executable"), nil)
	client.latestURL = server.URL + "/latest"
	if _, err := client.Latest(context.Background()); err == nil {
		t.Fatal("accepted missing release metadata")
	}
	metadata := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/missing" {
			_, _ = w.Write([]byte(`{"tag_name":"v1.2.4"}`))
			return
		}
		_, _ = w.Write([]byte(`{"tag_name":"v1.2.3","draft":false,"prerelease":false}`))
	}))
	defer metadata.Close()
	client.latestURL = metadata.URL
	if got, err := client.Latest(context.Background()); err != nil || got != "v1.2.3" {
		t.Fatalf("Latest() = %q, %v", got, err)
	}
	client.latestURL = metadata.URL + "/missing"
	if _, err := client.Latest(context.Background()); err == nil {
		t.Fatal("accepted a tag without a matching signed manifest")
	}
}

func TestNewer(t *testing.T) {
	if !Newer("v1.10.0", "v1.9.9") || Newer("v1.2.3", "v1.2.3") ||
		Newer("v1.2.3", "v2.0.0") || Newer("v1.2.3", "dev") {
		t.Fatal("unexpected version ordering")
	}
}
