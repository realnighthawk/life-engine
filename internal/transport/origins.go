package transport

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// newOriginGuard builds the cross-origin check for browser calls. http.CrossOriginProtection only matches exact
// origins and silently accepts a wildcard string that then never matches anything, so wildcard entries
// ("https://*.example.org") are handled here instead: a request whose Origin is https://<one or more labels>.example.org
// (same scheme, no port, no path) is trusted; everything else goes through the stdlib guard.
func newOriginGuard(trusted []string) (func(http.Handler) http.Handler, error) {
	guard := http.NewCrossOriginProtection()
	var wildcards []wildcardOrigin
	for _, o := range trusted {
		if o == "" {
			continue
		}
		if strings.Contains(o, "*") {
			w, err := parseWildcardOrigin(o)
			if err != nil {
				return nil, err
			}
			wildcards = append(wildcards, w)
			continue
		}
		if err := guard.AddTrustedOrigin(o); err != nil {
			return nil, err
		}
	}
	return func(next http.Handler) http.Handler {
		guarded := guard.Handler(next)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			for _, wc := range wildcards {
				if wc.matches(r.Header.Get("Origin")) {
					next.ServeHTTP(w, r)
					return
				}
			}
			guarded.ServeHTTP(w, r)
		})
	}, nil
}

type wildcardOrigin struct{ scheme, suffix string } // suffix is ".example.org"

func parseWildcardOrigin(s string) (wildcardOrigin, error) {
	scheme, host, ok := strings.Cut(s, "://")
	if !ok || scheme == "" || !strings.HasPrefix(host, "*.") || strings.Contains(host[2:], "*") ||
		strings.ContainsAny(host, "/:") || strings.Count(host, ".") < 2 {
		return wildcardOrigin{}, fmt.Errorf("invalid wildcard origin %q: want scheme://*.domain.tld", s)
	}
	return wildcardOrigin{scheme: scheme, suffix: host[1:]}, nil
}

func (w wildcardOrigin) matches(origin string) bool {
	u, err := url.Parse(origin)
	if err != nil || u.Scheme != w.scheme || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" {
		return false
	}
	host := u.Host // includes any port, which a wildcard entry never allows
	return strings.HasSuffix(host, w.suffix) && len(host) > len(w.suffix)
}
