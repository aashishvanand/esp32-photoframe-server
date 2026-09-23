package photoframe

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNewClientWithPasswordSendsBasicAuth(t *testing.T) {
	var gotPass string
	var gotOK bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, gotPass, gotOK = r.BasicAuth()
	}))
	defer srv.Close()

	c := NewClientWithPassword("frame.local", "s3cret")
	req, _ := http.NewRequest("GET", srv.URL, nil)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if !gotOK || gotPass != "s3cret" {
		t.Fatalf("basic auth = (%q, %v), want (\"s3cret\", true)", gotPass, gotOK)
	}
	if req.Header.Get("Authorization") != "" {
		t.Fatal("caller's request was mutated")
	}
}

func TestNewClientWithoutPasswordUsesSharedClient(t *testing.T) {
	if NewClientWithPassword("frame.local", "").httpClient != sharedHTTPClient {
		t.Fatal("empty password should reuse the shared client")
	}
}

func TestPasswordNotForwardedOnRedirect(t *testing.T) {
	var leaked bool
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _, leaked = r.BasicAuth()
	}))
	defer other.Close()
	frame := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL, http.StatusFound)
	}))
	defer frame.Close()

	c := NewClientWithPassword("frame.local", "s3cret")
	req, _ := http.NewRequest("GET", frame.URL, nil)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("status = %d, want the unfollowed 302", resp.StatusCode)
	}
	if leaked {
		t.Fatal("password was sent to the redirect target")
	}
}
