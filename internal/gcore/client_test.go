package gcore

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

type rec struct{ method, path, auth, body string }

func TestClientSendsAPIKeyAndShapes(t *testing.T) {
	var got []rec
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got = append(got, rec{r.Method, r.URL.Path, r.Header.Get("Authorization"), strings.TrimSpace(string(b))})
		switch r.Method {
		case http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		case http.MethodGet:
			io.WriteString(w, `[{"id":1},{"id":2}]`)
		default:
			io.WriteString(w, `{"id":7,"active":true,"push_url_whip":"whip","iframe_url":"if"}`)
		}
	}))
	defer srv.Close()
	c := New(srv.URL, "k")
	ctx := context.Background()

	s, err := c.Create(ctx, "n")
	if err != nil || s.PushURLWHIP != "whip" || s.IframeURL != "if" {
		t.Fatalf("Create: %+v %v", s, err)
	}
	if s, err = c.SetActive(ctx, 7, true); err != nil || !s.Active {
		t.Fatalf("SetActive: %+v %v", s, err)
	}
	if l, err := c.List(ctx); err != nil || len(l) != 2 {
		t.Fatalf("List: %v %v", l, err)
	}
	if err := c.Delete(ctx, 7); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	want := []rec{
		{"POST", "/streaming/streams", "APIKey k", `{"name":"n","active":false}`},
		{"PATCH", "/streaming/streams/7", "APIKey k", `{"active":true}`},
		{"GET", "/streaming/streams", "APIKey k", ""},
		{"DELETE", "/streaming/streams/7", "APIKey k", ""},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d requests, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("request %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestClientErrorDoesNotLeakKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		io.WriteString(w, `{"error":"quota"}`)
	}))
	defer srv.Close()
	_, err := New(srv.URL, "secret-key").Create(context.Background(), "n")
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != 403 {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(err.Error(), "quota") || strings.Contains(err.Error(), "secret-key") {
		t.Fatalf("bad error string: %q", err.Error())
	}
}

// TestLiveSmoke is the early check on the PATCH call the go-live flow depends on.
func TestLiveSmoke(t *testing.T) {
	key := os.Getenv("GCORE_SMOKE_KEY")
	if key == "" {
		t.Skip("set GCORE_SMOKE_KEY to run against the real account")
	}
	ctx := context.Background()
	c := New("https://api.gcore.com", key)
	s, err := c.Create(ctx, "smoke-test")
	if err != nil {
		t.Fatal(err)
	}
	id := s.ID
	defer func() {
		if err := c.Delete(ctx, id); err != nil {
			t.Errorf("delete: %v", err)
		}
	}()
	if s, err = c.SetActive(ctx, id, true); err != nil || !s.Active {
		t.Fatalf("SetActive(true): %+v %v", s, err)
	}
	if s, err = c.SetActive(ctx, id, false); err != nil || s.Active {
		t.Fatalf("SetActive(false): %+v %v", s, err)
	}
}
