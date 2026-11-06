package assist

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"k8s.io/client-go/rest"
)

func TestMintIdentity(t *testing.T) {
	requests := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != "POST" || r.URL.Path != "/api/v1/namespaces/ai/serviceaccounts/assist-tools/token" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		var body struct {
			Spec struct {
				ExpirationSeconds int `json:"expirationSeconds"`
			} `json:"spec"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Spec.ExpirationSeconds != 600 {
			t.Errorf("token request %+v: %v", body, err)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"apiVersion": "authentication.k8s.io/v1", "kind": "TokenRequest",
			"status": map[string]string{"token": "tool-token", "expirationTimestamp": time.Now().Add(10 * time.Minute).UTC().Format(time.RFC3339)},
		})
	}))
	defer server.Close()
	cfg := &rest.Config{Host: server.URL, BearerToken: "operator-token", ContentConfig: rest.ContentConfig{ContentType: "application/json"}, TLSClientConfig: rest.TLSClientConfig{
		CAData: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}),
	}}
	tool, err := MintIdentity(context.Background(), cfg, ToolServiceAccount)
	if err != nil || tool.BearerToken != "tool-token" || requests != 1 {
		t.Fatalf("mint failed: %v, requests %d", err, requests)
	}
	if _, err := MintIdentity(context.Background(), cfg, "other"); err == nil || requests != 1 {
		t.Fatal("unknown identity reached the API")
	}
}
