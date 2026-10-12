package transport

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWildcardOriginGuard(t *testing.T) {
	guard, err := newOriginGuard([]string{"https://*.example.org", "https://exact.test"})
	if err != nil {
		t.Fatal(err)
	}
	h := guard(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	for origin, want := range map[string]int{
		"https://app.example.org":      200,
		"https://a.b.example.org":      200,
		"https://exact.test":           200,
		"https://example.org":          403, // the apex is not a subdomain
		"https://evilexample.org":      403, // suffix must follow a dot
		"https://app.example.org.evil": 403,
		"http://app.example.org":       403, // scheme must match
		"https://app.example.org:8443": 403, // wildcard entries allow no port
		"https://other.test":           403,
	} {
		r := httptest.NewRequest("POST", "/x", nil)
		r.Header.Set("Origin", origin)
		r.Header.Set("Sec-Fetch-Site", "cross-site")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != want {
			t.Errorf("origin %q: got %d want %d", origin, w.Code, want)
		}
	}
	for _, bad := range []string{"https://*", "https://*.org", "*.example.org", "https://a.*.example.org", "https://*.example.org/x"} {
		if _, err := newOriginGuard([]string{bad}); err == nil {
			t.Errorf("accepted bad wildcard %q", bad)
		}
	}
}
