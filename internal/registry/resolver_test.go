package registry

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type fakeLister struct{ tags []TagInfo }

func (f fakeLister) List(context.Context, ImageReference) ([]TagInfo, error) {
	return append([]TagInfo(nil), f.tags...), nil
}

type countingLister struct {
	tags  []TagInfo
	calls int
}

func (l *countingLister) List(context.Context, ImageReference) ([]TagInfo, error) {
	l.calls++
	return append([]TagInfo(nil), l.tags...), nil
}

func TestParseReference(t *testing.T) {
	cases := map[string]ImageReference{
		"nginx:1.2.3":                           {Registry: "registry-1.docker.io", Repository: "library/nginx", Tag: "1.2.3"},
		"team/app:2.0":                          {Registry: "registry-1.docker.io", Repository: "team/app", Tag: "2.0"},
		"ghcr.io/team/app:v3":                   {Registry: "ghcr.io", Repository: "team/app", Tag: "v3"},
		"registry.example.com:5000/team/app:v1": {Registry: "registry.example.com:5000", Repository: "team/app", Tag: "v1"},
	}
	for input, expected := range cases {
		actual, err := ParseReference(input)
		if err != nil {
			t.Fatalf("%s: %v", input, err)
		}
		if actual.Registry != expected.Registry || actual.Repository != expected.Repository || actual.Tag != expected.Tag {
			t.Fatalf("%s: got %+v want %+v", input, actual, expected)
		}
	}
}

func TestRemoteListerUsesConfiguredProxyForCustomRegistry(t *testing.T) {
	used := false
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		used = true
		if r.URL.Host != "registry.unreachable.invalid:5000" {
			t.Fatalf("request used wrong registry host: %q", r.URL.Host)
		}
		if r.URL.Path != "/v2/team/app/tags/list" {
			t.Fatalf("request used wrong registry path: %q", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(tagResponse{Name: "team/app", Tags: []string{"v1.0.0"}})
	}))
	defer proxy.Close()

	t.Setenv("COMPOSE_UPDATER_INSECURE_REGISTRIES", "registry.unreachable.invalid:5000")
	lister := &RemoteLister{client: newHTTPClient(time.Second, proxy.URL)}
	tags, err := lister.List(context.Background(), ImageReference{
		Registry:   "registry.unreachable.invalid:5000",
		Repository: "team/app",
		Tag:        "v0.9.0",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !used || tagNames(tags) != "v1.0.0" {
		t.Fatalf("proxy was not used correctly: used=%v tags=%#v", used, tags)
	}
}

func TestResolveNumericTag(t *testing.T) {
	resolver := NewWithLister(time.Second, nil, fakeLister{tags: tagInfos("1.2.3", "1.5.0", "2.0.0-alpine")})
	decision, err := resolver.Resolve(context.Background(), "example.com/team/app:1.2.3", true)
	if err != nil {
		t.Fatal(err)
	}
	if !decision.Changed || decision.Image != "example.com/team/app:1.5.0" {
		t.Fatalf("unexpected decision: %+v", decision)
	}
}

func TestResolveLatestDoesNotRewrite(t *testing.T) {
	resolver := NewWithLister(time.Second, nil, fakeLister{})
	decision, err := resolver.Resolve(context.Background(), "nginx:latest", true)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Changed || decision.Image != "nginx:latest" {
		t.Fatalf("unexpected decision: %+v", decision)
	}
}

func TestResolveSHATagToLatestStable(t *testing.T) {
	resolver := NewWithLister(time.Second, nil, fakeLister{tags: tagInfos("sha-old", "v1.9.0", "v2.0.0-beta", "v1.10.0")})
	decision, err := resolver.Resolve(context.Background(), "team/app:sha-old", true)
	if err != nil {
		t.Fatal(err)
	}
	if !decision.Changed || decision.Image != "team/app:v1.10.0" {
		t.Fatalf("unexpected decision: %+v", decision)
	}
}

func TestResolveNewestPublishedTag(t *testing.T) {
	old := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	newest := old.Add(time.Hour)
	resolver := NewWithLister(time.Second, nil, fakeLister{tags: []TagInfo{
		{Name: "v2.0.0", LastUpdated: old},
		{Name: "sha-new", LastUpdated: newest},
		{Name: "latest", LastUpdated: old},
	}})
	decision, err := resolver.Resolve(context.Background(), "team/app:sha-old", false)
	if err != nil {
		t.Fatal(err)
	}
	if !decision.Changed || decision.Image != "team/app:sha-new" {
		t.Fatalf("unexpected decision: %+v", decision)
	}
}

func TestResolveNewestFallsBackToLatestAliasWithoutTimestamps(t *testing.T) {
	resolver := NewWithLister(time.Second, nil, fakeLister{tags: tagInfos("1.0.0", "latest", "sha-new")})
	decision, err := resolver.Resolve(context.Background(), "team/app:sha-old", false)
	if err != nil {
		t.Fatal(err)
	}
	if !decision.Changed || decision.Image != "team/app:latest" {
		t.Fatalf("unexpected decision: %+v", decision)
	}
}

func TestResolverCacheResetsBetweenUpdateCycles(t *testing.T) {
	lister := &countingLister{tags: tagInfos("v1.0.0")}
	resolver := NewWithLister(time.Second, nil, lister)
	for range 2 {
		if _, err := resolver.Resolve(context.Background(), "team/app:sha-old", true); err != nil {
			t.Fatal(err)
		}
	}
	if lister.calls != 1 {
		t.Fatalf("same cycle should use cache, calls=%d", lister.calls)
	}
	resolver.ResetCache()
	if _, err := resolver.Resolve(context.Background(), "team/app:sha-old", true); err != nil {
		t.Fatal(err)
	}
	if lister.calls != 2 {
		t.Fatalf("new cycle must query registry again, calls=%d", lister.calls)
	}
}

func TestRemoteListerBearerAndPagination(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			if !strings.Contains(r.URL.Query().Get("scope"), "repository:team/app:pull") {
				t.Fatalf("unexpected scope: %s", r.URL.RawQuery)
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"token": "abc"})
		case "/v2/team/app/tags/list":
			if r.Header.Get("Authorization") != "Bearer abc" {
				w.Header().Set("WWW-Authenticate", `Bearer realm="`+server.URL+`/token",service="test",scope="repository:team/app:pull"`)
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			if r.URL.Query().Get("last") == "1.0.0" {
				_ = json.NewEncoder(w).Encode(tagResponse{Name: "team/app", Tags: []string{"2.0.0"}})
				return
			}
			w.Header().Set("Link", `</v2/team/app/tags/list?n=1000&last=1.0.0>; rel="next"`)
			_ = json.NewEncoder(w).Encode(tagResponse{Name: "team/app", Tags: []string{"1.0.0"}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	reference, err := ParseReference(strings.TrimPrefix(server.URL, "http://") + "/team/app:1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("COMPOSE_UPDATER_INSECURE_REGISTRIES", reference.Registry)
	lister := &RemoteLister{client: server.Client()}
	tags, err := lister.List(context.Background(), reference)
	if err != nil {
		t.Fatal(err)
	}
	if tagNames(tags) != "1.0.0,2.0.0" {
		t.Fatalf("unexpected tags: %#v", tags)
	}
}

func TestRemoteListerDockerHubTimestampsAndPagination(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/repositories/team/app/tags" {
			http.NotFound(w, r)
			return
		}
		if r.URL.Query().Get("page") == "2" {
			_ = json.NewEncoder(w).Encode(map[string]any{"next": "", "results": []map[string]any{{"name": "sha-new", "last_updated": "2026-07-02T00:00:00Z"}}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"next":    server.URL + "/v2/repositories/team/app/tags?page=2",
			"results": []map[string]any{{"name": "v1.0.0", "last_updated": "2026-07-01T00:00:00Z"}},
		})
	}))
	defer server.Close()
	lister := &RemoteLister{client: server.Client(), dockerHubBaseURL: server.URL}
	tags, err := lister.List(context.Background(), ImageReference{Registry: "registry-1.docker.io", Repository: "team/app", Tag: "sha-old"})
	if err != nil {
		t.Fatal(err)
	}
	if tagNames(tags) != "v1.0.0,sha-new" || !tags[1].LastUpdated.After(tags[0].LastUpdated) {
		t.Fatalf("unexpected tags: %#v", tags)
	}
}

func TestParseAuthChallenge(t *testing.T) {
	scheme, params := parseAuthChallenge(`Bearer realm="https://auth.example/token",service="registry",scope="repository:a/b:pull"`)
	if scheme != "Bearer" || params["realm"] != "https://auth.example/token" || params["scope"] != "repository:a/b:pull" {
		t.Fatalf("unexpected challenge: %s %#v", scheme, params)
	}
}

func tagInfos(names ...string) []TagInfo {
	result := make([]TagInfo, 0, len(names))
	for _, name := range names {
		result = append(result, TagInfo{Name: name})
	}
	return result
}

func tagNames(tags []TagInfo) string {
	names := make([]string, 0, len(tags))
	for _, tag := range tags {
		names = append(names, tag.Name)
	}
	return strings.Join(names, ",")
}
