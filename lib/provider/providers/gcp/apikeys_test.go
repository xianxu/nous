package gcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestCreateAPIKey_PostsRestrictionsAndReturnsOperation(t *testing.T) {
	mux := http.NewServeMux()
	var bodyCaptured createAPIKeyRequest
	mux.HandleFunc("/v2/projects/myproj/locations/global/keys", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("method = %s", r.Method)
		}
		json.NewDecoder(r.Body).Decode(&bodyCaptured)
		json.NewEncoder(w).Encode(Operation{Name: "operations/k.create-x", Done: false})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestClient(srv, "tok")
	c.APIKeys = srv.URL // newTestClient doesn't set this by default

	op, err := c.CreateAPIKey(context.Background(), "myproj", "charon-aistudio",
		[]string{"generativelanguage.googleapis.com"})
	if err != nil {
		t.Fatalf("CreateAPIKey: %v", err)
	}
	if op.Done {
		t.Error("expected Done=false on initial response")
	}
	if bodyCaptured.DisplayName != "charon-aistudio" {
		t.Errorf("displayName = %q", bodyCaptured.DisplayName)
	}
	if bodyCaptured.Restrictions == nil || len(bodyCaptured.Restrictions.APITargets) != 1 {
		t.Fatalf("expected 1 apiTarget, got %+v", bodyCaptured.Restrictions)
	}
	if bodyCaptured.Restrictions.APITargets[0].Service != "generativelanguage.googleapis.com" {
		t.Errorf("apiTarget service = %q", bodyCaptured.Restrictions.APITargets[0].Service)
	}
}

func TestWaitAPIKeyOperation_PollsThenReturnsKey(t *testing.T) {
	var calls atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/operations/k.create-x", func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if n < 2 {
			json.NewEncoder(w).Encode(Operation{Name: "operations/k.create-x", Done: false})
			return
		}
		// Done with the freshly-minted key embedded in response.
		op := Operation{
			Name: "operations/k.create-x",
			Done: true,
			Response: map[string]any{
				"name":        "projects/myproj/locations/global/keys/abc-uid",
				"uid":         "abc-uid",
				"displayName": "charon-aistudio",
				"keyString":   "AIzaSy_FAKE_BUT_PLAUSIBLE",
			},
		}
		json.NewEncoder(w).Encode(op)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestClient(srv, "tok")
	c.APIKeys = srv.URL

	op, err := c.WaitAPIKeyOperation(context.Background(), "operations/k.create-x")
	if err != nil {
		t.Fatalf("WaitAPIKeyOperation: %v", err)
	}
	key, err := ExtractAPIKey(op)
	if err != nil {
		t.Fatalf("ExtractAPIKey: %v", err)
	}
	if key.UID != "abc-uid" {
		t.Errorf("UID = %q", key.UID)
	}
	if key.KeyString != "AIzaSy_FAKE_BUT_PLAUSIBLE" {
		t.Errorf("KeyString = %q", key.KeyString)
	}
	if key.Name != "projects/myproj/locations/global/keys/abc-uid" {
		t.Errorf("Name = %q", key.Name)
	}
}

func TestWaitAPIKeyOperation_PropagatesOperationError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/operations/bad", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(Operation{
			Name: "operations/bad",
			Done: true,
			Error: &OperationError{
				Code:    7,
				Message: "PERMISSION_DENIED",
			},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newTestClient(srv, "tok")
	c.APIKeys = srv.URL

	_, err := c.WaitAPIKeyOperation(context.Background(), "operations/bad")
	if err == nil {
		t.Fatal("expected operation error")
	}
}

func TestDeleteAPIKey_HitsResourceNamePath(t *testing.T) {
	mux := http.NewServeMux()
	hit := false
	mux.HandleFunc("/v2/projects/myproj/locations/global/keys/abc-uid", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "DELETE" {
			t.Errorf("method = %s", r.Method)
		}
		hit = true
		json.NewEncoder(w).Encode(Operation{Name: "operations/k.delete-x", Done: true})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newTestClient(srv, "tok")
	c.APIKeys = srv.URL

	op, err := c.DeleteAPIKey(context.Background(), "projects/myproj/locations/global/keys/abc-uid")
	if err != nil {
		t.Fatalf("DeleteAPIKey: %v", err)
	}
	if !hit {
		t.Error("DELETE did not reach the expected URL path")
	}
	if !op.Done {
		t.Error("expected Done op for delete")
	}
}

func TestExtractAPIKey_NilOperation(t *testing.T) {
	if _, err := ExtractAPIKey(nil); err == nil {
		t.Error("expected error for nil op")
	}
	if _, err := ExtractAPIKey(&Operation{}); err == nil {
		t.Error("expected error for empty response")
	}
}
