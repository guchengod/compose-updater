package registry

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/guchengod/compose-updater/internal/versiontag"
)

type ImageReference struct {
	Original   string
	Registry   string
	Repository string
	Tag        string
}

type Decision struct {
	Image      string
	CurrentTag string
	LatestTag  string
	Changed    bool
	Reason     string
}

type TagInfo struct {
	Name        string
	LastUpdated time.Time
}

type TagLister interface {
	List(ctx context.Context, reference ImageReference) ([]TagInfo, error)
}

type Resolver struct {
	lister  TagLister
	timeout time.Duration
	logger  *slog.Logger
	mu      sync.Mutex
	cache   map[string][]TagInfo
}

type RemoteLister struct {
	client           *http.Client
	dockerHubBaseURL string
}

type credentials struct {
	Username      string
	Password      string
	RegistryToken string
}

type dockerConfig struct {
	Auths map[string]struct {
		Auth          string `json:"auth"`
		Username      string `json:"username"`
		Password      string `json:"password"`
		IdentityToken string `json:"identitytoken"`
		RegistryToken string `json:"registrytoken"`
	} `json:"auths"`
}

type tagResponse struct {
	Name string   `json:"name"`
	Tags []string `json:"tags"`
}

type dockerHubTagResponse struct {
	Next    string `json:"next"`
	Results []struct {
		Name        string    `json:"name"`
		LastUpdated time.Time `json:"last_updated"`
	} `json:"results"`
}

type tokenResponse struct {
	Token       string `json:"token"`
	AccessToken string `json:"access_token"`
}

func New(timeout time.Duration, logger *slog.Logger, proxyAddress string) *Resolver {
	return NewWithLister(timeout, logger, &RemoteLister{client: newHTTPClient(timeout, proxyAddress)})
}

func NewWithLister(timeout time.Duration, logger *slog.Logger, lister TagLister) *Resolver {
	return &Resolver{lister: lister, timeout: timeout, logger: logger, cache: make(map[string][]TagInfo)}
}

func newHTTPClient(timeout time.Duration, proxyAddress string) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if proxyAddress != "" {
		proxyURL, err := url.Parse(proxyAddress)
		if err == nil {
			transport.Proxy = http.ProxyURL(proxyURL)
		}
	}
	return &http.Client{Timeout: timeout, Transport: transport}
}

func (r *Resolver) ResetCache() {
	r.mu.Lock()
	r.cache = make(map[string][]TagInfo)
	r.mu.Unlock()
}

func (r *Resolver) Resolve(ctx context.Context, image string, stableOnly bool) (Decision, error) {
	image = strings.TrimSpace(image)
	decision := Decision{Image: image}
	if image == "" {
		decision.Reason = "empty"
		return decision, nil
	}
	if strings.Contains(image, "${") || strings.Contains(image, "@") {
		decision.Reason = "dynamic_or_digest"
		return decision, nil
	}
	reference, err := ParseReference(image)
	if err != nil {
		return decision, err
	}
	decision.CurrentTag = reference.Tag
	decision.LatestTag = reference.Tag
	if reference.Tag == "" || strings.EqualFold(reference.Tag, "latest") {
		decision.Reason = "mutable_tag"
		return decision, nil
	}
	tags, err := r.listTags(ctx, reference)
	if err != nil {
		return decision, err
	}
	latest, ok := selectTargetTag(reference.Tag, tags, stableOnly)
	if !ok {
		policy := "最新版"
		if stableOnly {
			policy = "稳定版"
		}
		return decision, fmt.Errorf("仓库 %s/%s 中找不到可用的%s标签", reference.Registry, reference.Repository, policy)
	}
	decision.LatestTag = latest
	if latest == reference.Tag {
		decision.Reason = "already_target_tag"
		return decision, nil
	}
	decision.Image = replaceTag(image, latest)
	decision.Changed = true
	if stableOnly {
		decision.Reason = "newer_stable_tag"
	} else {
		decision.Reason = "newest_published_tag"
	}
	return decision, nil
}

func (r *Resolver) listTags(ctx context.Context, reference ImageReference) ([]TagInfo, error) {
	key := reference.Registry + "/" + reference.Repository
	r.mu.Lock()
	if cached, ok := r.cache[key]; ok {
		result := append([]TagInfo(nil), cached...)
		r.mu.Unlock()
		return result, nil
	}
	r.mu.Unlock()
	requestCtx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	if r.logger != nil {
		r.logger.Info("registry_tags_query_started", "repository", key)
	}
	tags, err := r.lister.List(requestCtx, reference)
	if err != nil {
		return nil, fmt.Errorf("查询 Registry 标签 %q: %w", key, err)
	}
	sort.Slice(tags, func(i, j int) bool { return tags[i].Name < tags[j].Name })
	r.mu.Lock()
	r.cache[key] = append([]TagInfo(nil), tags...)
	r.mu.Unlock()
	return tags, nil
}

func selectTargetTag(current string, tags []TagInfo, stableOnly bool) (string, bool) {
	if stableOnly {
		return selectStableTag(current, tags)
	}
	var newest TagInfo
	for _, tag := range tags {
		if tag.Name == "" || tag.LastUpdated.IsZero() {
			continue
		}
		if newest.Name == "" || tag.LastUpdated.After(newest.LastUpdated) ||
			(tag.LastUpdated.Equal(newest.LastUpdated) && tag.Name < newest.Name) {
			newest = tag
		}
	}
	if newest.Name != "" {
		return newest.Name, true
	}
	for _, tag := range tags {
		if strings.EqualFold(tag.Name, "latest") {
			return tag.Name, true
		}
	}
	return selectLatestRelease(tags, false, "", "", false)
}

func selectStableTag(current string, tags []TagInfo) (string, bool) {
	prefix, suffix := "", ""
	restrictPrefix := false
	if parsed, ok := versiontag.Parse(current); ok {
		prefix = parsed.Prefix
		restrictPrefix = true
		if parsed.Suffix != "" && !isPrereleaseSuffix(parsed.Suffix) {
			suffix = parsed.Suffix
		}
	}
	return selectLatestRelease(tags, true, prefix, suffix, restrictPrefix)
}

func selectLatestRelease(tags []TagInfo, stableOnly bool, prefix, suffix string, restrictPrefix bool) (string, bool) {
	var best versiontag.Version
	found := false
	for _, tag := range tags {
		candidate, ok := versiontag.Parse(tag.Name)
		if !ok {
			continue
		}
		if restrictPrefix && candidate.Prefix != prefix {
			continue
		}
		if suffix != "" {
			if candidate.Suffix != suffix {
				continue
			}
		} else if candidate.Suffix != "" {
			if stableOnly || !isPrereleaseSuffix(candidate.Suffix) {
				continue
			}
		}
		if !found || compareRelease(candidate, best) > 0 {
			best = candidate
			found = true
		}
	}
	return best.Raw, found
}

func compareRelease(left, right versiontag.Version) int {
	if compared := versiontag.Compare(left, right); compared != 0 {
		return compared
	}
	leftPre := isPrereleaseSuffix(left.Suffix)
	rightPre := isPrereleaseSuffix(right.Suffix)
	if leftPre != rightPre {
		if leftPre {
			return -1
		}
		return 1
	}
	return strings.Compare(left.Suffix, right.Suffix)
}

func isPrereleaseSuffix(suffix string) bool {
	value := strings.ToLower(strings.TrimLeft(suffix, "-_"))
	for _, prefix := range []string{"alpha", "beta", "rc", "pre", "preview", "dev", "nightly", "snapshot", "canary", "edge"} {
		if value == prefix || strings.HasPrefix(value, prefix+".") || strings.HasPrefix(value, prefix+"-") || strings.HasPrefix(value, prefix+"_") {
			return true
		}
	}
	return false
}

func ParseReference(image string) (ImageReference, error) {
	image = strings.TrimSpace(image)
	if image == "" {
		return ImageReference{}, errors.New("镜像引用为空")
	}
	if strings.Contains(image, "@") {
		return ImageReference{}, fmt.Errorf("digest 镜像引用不支持查询 tag: %q", image)
	}
	lastSlash := strings.LastIndex(image, "/")
	lastColon := strings.LastIndex(image, ":")
	tag := "latest"
	namePart := image
	if lastColon > lastSlash {
		tag = image[lastColon+1:]
		namePart = image[:lastColon]
	}
	if namePart == "" || tag == "" {
		return ImageReference{}, fmt.Errorf("无效镜像引用 %q", image)
	}
	parts := strings.Split(namePart, "/")
	registry := "registry-1.docker.io"
	repositoryParts := parts
	if len(parts) > 1 && (strings.Contains(parts[0], ".") || strings.Contains(parts[0], ":") || parts[0] == "localhost") {
		registry = parts[0]
		repositoryParts = parts[1:]
	}
	if registry == "docker.io" || registry == "index.docker.io" {
		registry = "registry-1.docker.io"
	}
	if registry == "registry-1.docker.io" && len(repositoryParts) == 1 {
		repositoryParts = append([]string{"library"}, repositoryParts...)
	}
	repository := strings.Join(repositoryParts, "/")
	if repository == "" {
		return ImageReference{}, fmt.Errorf("无效镜像仓库 %q", image)
	}
	return ImageReference{Original: image, Registry: registry, Repository: repository, Tag: tag}, nil
}

func replaceTag(image, newTag string) string {
	lastSlash := strings.LastIndex(image, "/")
	lastColon := strings.LastIndex(image, ":")
	if lastColon > lastSlash {
		return image[:lastColon+1] + newTag
	}
	return image + ":" + newTag
}

func (l *RemoteLister) List(ctx context.Context, reference ImageReference) ([]TagInfo, error) {
	if l.client == nil {
		l.client = &http.Client{Timeout: 2 * time.Minute}
	}
	if reference.Registry == "registry-1.docker.io" {
		if tags, err := l.listDockerHubTags(ctx, reference); err == nil && len(tags) > 0 {
			return tags, nil
		}
	}
	return l.listRegistryTags(ctx, reference)
}

func (l *RemoteLister) listDockerHubTags(ctx context.Context, reference ImageReference) ([]TagInfo, error) {
	baseURL := strings.TrimRight(l.dockerHubBaseURL, "/")
	if baseURL == "" {
		baseURL = "https://hub.docker.com"
	}
	first, err := url.Parse(baseURL + "/v2/repositories/" + escapeRepository(reference.Repository) + "/tags")
	if err != nil {
		return nil, err
	}
	query := first.Query()
	query.Set("page_size", "100")
	query.Set("ordering", "last_updated")
	first.RawQuery = query.Encode()
	var tags []TagInfo
	for nextURL := first; nextURL != nil; {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, nextURL.String(), nil)
		if err != nil {
			return nil, err
		}
		request.Header.Set("Accept", "application/json")
		response, err := l.client.Do(request)
		if err != nil {
			return nil, fmt.Errorf("请求 Docker Hub 标签 %s: %w", nextURL, err)
		}
		body, readErr := io.ReadAll(io.LimitReader(response.Body, 8<<20))
		_ = response.Body.Close()
		if readErr != nil {
			return nil, fmt.Errorf("读取 Docker Hub 标签响应: %w", readErr)
		}
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			return nil, fmt.Errorf("Docker Hub HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(body)))
		}
		var page dockerHubTagResponse
		if err := json.Unmarshal(body, &page); err != nil {
			return nil, fmt.Errorf("解析 Docker Hub 标签响应: %w", err)
		}
		for _, tag := range page.Results {
			tags = append(tags, TagInfo{Name: tag.Name, LastUpdated: tag.LastUpdated})
		}
		if page.Next == "" {
			nextURL = nil
			continue
		}
		nextURL, err = url.Parse(page.Next)
		if err != nil {
			return nil, fmt.Errorf("解析 Docker Hub 下一页地址: %w", err)
		}
	}
	return deduplicateTags(tags), nil
}

func (l *RemoteLister) listRegistryTags(ctx context.Context, reference ImageReference) ([]TagInfo, error) {
	scheme := "https"
	if isInsecureRegistry(reference.Registry) {
		scheme = "http"
	}
	registryURL := &url.URL{
		Scheme:   scheme,
		Host:     reference.Registry,
		Path:     "/v2/" + escapeRepository(reference.Repository) + "/tags/list",
		RawQuery: "n=1000",
	}
	creds := loadCredentials(reference.Registry)
	var tags []TagInfo
	nextURL := registryURL
	for nextURL != nil {
		response, err := l.authorizedGet(ctx, nextURL, reference, creds)
		if err != nil {
			return nil, err
		}
		body, readErr := io.ReadAll(io.LimitReader(response.Body, 8<<20))
		_ = response.Body.Close()
		if readErr != nil {
			return nil, fmt.Errorf("读取 Registry 响应: %w", readErr)
		}
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			return nil, fmt.Errorf("Registry HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(body)))
		}
		var page tagResponse
		if err := json.Unmarshal(body, &page); err != nil {
			return nil, fmt.Errorf("解析 Registry tag 响应: %w", err)
		}
		for _, tag := range page.Tags {
			tags = append(tags, TagInfo{Name: tag})
		}
		nextURL = parseNextLink(response.Header.Get("Link"), nextURL)
	}
	return deduplicateTags(tags), nil
}

func (l *RemoteLister) authorizedGet(ctx context.Context, target *url.URL, reference ImageReference, creds credentials) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json")
	if creds.RegistryToken != "" {
		request.Header.Set("Authorization", "Bearer "+creds.RegistryToken)
	} else if creds.Username != "" {
		request.SetBasicAuth(creds.Username, creds.Password)
	}
	response, err := l.client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("请求 Registry %s: %w", target, err)
	}
	if response.StatusCode != http.StatusUnauthorized {
		return response, nil
	}
	challenge := response.Header.Get("WWW-Authenticate")
	_ = response.Body.Close()
	scheme, parameters := parseAuthChallenge(challenge)
	switch strings.ToLower(scheme) {
	case "bearer":
		token, err := l.fetchBearerToken(ctx, parameters, reference, creds)
		if err != nil {
			return nil, err
		}
		retry, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
		if err != nil {
			return nil, err
		}
		retry.Header.Set("Accept", "application/json")
		retry.Header.Set("Authorization", "Bearer "+token)
		return l.client.Do(retry)
	case "basic":
		if creds.Username == "" {
			return nil, fmt.Errorf("Registry 要求 Basic 认证，但未找到 %s 凭据", reference.Registry)
		}
		retry, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
		if err != nil {
			return nil, err
		}
		retry.SetBasicAuth(creds.Username, creds.Password)
		return l.client.Do(retry)
	default:
		return nil, fmt.Errorf("Registry 返回 401，但认证挑战无效: %q", challenge)
	}
}

func (l *RemoteLister) fetchBearerToken(ctx context.Context, parameters map[string]string, reference ImageReference, creds credentials) (string, error) {
	realm := parameters["realm"]
	if realm == "" {
		return "", errors.New("Bearer 认证挑战缺少 realm")
	}
	tokenURL, err := url.Parse(realm)
	if err != nil {
		return "", fmt.Errorf("无效 token realm: %w", err)
	}
	query := tokenURL.Query()
	if service := parameters["service"]; service != "" {
		query.Set("service", service)
	}
	scope := parameters["scope"]
	if scope == "" {
		scope = "repository:" + reference.Repository + ":pull"
	}
	query.Set("scope", scope)
	tokenURL.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, tokenURL.String(), nil)
	if err != nil {
		return "", err
	}
	if creds.Username != "" {
		request.SetBasicAuth(creds.Username, creds.Password)
	}
	response, err := l.client.Do(request)
	if err != nil {
		return "", fmt.Errorf("请求 Registry token: %w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	if err != nil {
		return "", err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", fmt.Errorf("Registry token HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(body)))
	}
	var parsed tokenResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", fmt.Errorf("解析 Registry token: %w", err)
	}
	if parsed.Token != "" {
		return parsed.Token, nil
	}
	if parsed.AccessToken != "" {
		return parsed.AccessToken, nil
	}
	return "", errors.New("Registry token 响应未包含 token")
}

func parseAuthChallenge(value string) (string, map[string]string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	scheme, rest, found := strings.Cut(value, " ")
	if !found {
		return value, map[string]string{}
	}
	result := make(map[string]string)
	for len(rest) > 0 {
		rest = strings.TrimLeft(rest, " ,")
		if rest == "" {
			break
		}
		key, remaining, ok := strings.Cut(rest, "=")
		if !ok {
			break
		}
		key = strings.TrimSpace(key)
		rest = strings.TrimLeft(remaining, " ")
		var valuePart string
		if strings.HasPrefix(rest, `"`) {
			rest = rest[1:]
			var builder strings.Builder
			escaped := false
			index := 0
			for ; index < len(rest); index++ {
				ch := rest[index]
				if escaped {
					builder.WriteByte(ch)
					escaped = false
					continue
				}
				if ch == '\\' {
					escaped = true
					continue
				}
				if ch == '"' {
					index++
					break
				}
				builder.WriteByte(ch)
			}
			valuePart = builder.String()
			rest = rest[index:]
		} else {
			index := strings.IndexByte(rest, ',')
			if index < 0 {
				valuePart = strings.TrimSpace(rest)
				rest = ""
			} else {
				valuePart = strings.TrimSpace(rest[:index])
				rest = rest[index+1:]
			}
		}
		result[strings.ToLower(key)] = valuePart
	}
	return scheme, result
}

func loadCredentials(registry string) credentials {
	configDir := strings.TrimSpace(os.Getenv("DOCKER_CONFIG"))
	if configDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return credentials{}
		}
		configDir = filepath.Join(home, ".docker")
	}
	content, err := os.ReadFile(filepath.Join(configDir, "config.json"))
	if err != nil {
		return credentials{}
	}
	var cfg dockerConfig
	if json.Unmarshal(content, &cfg) != nil {
		return credentials{}
	}
	keys := []string{registry, "https://" + registry, "http://" + registry}
	if registry == "registry-1.docker.io" {
		keys = append(keys, "https://index.docker.io/v1/", "docker.io", "index.docker.io")
	}
	for _, key := range keys {
		entry, ok := cfg.Auths[key]
		if !ok {
			continue
		}
		creds := credentials{Username: entry.Username, Password: entry.Password, RegistryToken: entry.RegistryToken}
		if creds.RegistryToken == "" {
			creds.RegistryToken = entry.IdentityToken
		}
		if entry.Auth != "" && creds.Username == "" {
			decoded, err := base64.StdEncoding.DecodeString(entry.Auth)
			if err == nil {
				creds.Username, creds.Password, _ = strings.Cut(string(decoded), ":")
			}
		}
		return creds
	}
	return credentials{}
}

func escapeRepository(repository string) string {
	parts := strings.Split(repository, "/")
	for index, part := range parts {
		parts[index] = url.PathEscape(part)
	}
	return strings.Join(parts, "/")
}

func parseNextLink(link string, current *url.URL) *url.URL {
	for _, item := range strings.Split(link, ",") {
		item = strings.TrimSpace(item)
		if !strings.Contains(item, `rel="next"`) && !strings.Contains(item, "rel=next") {
			continue
		}
		start := strings.IndexByte(item, '<')
		end := strings.IndexByte(item, '>')
		if start < 0 || end <= start {
			continue
		}
		next, err := url.Parse(item[start+1 : end])
		if err != nil {
			continue
		}
		return current.ResolveReference(next)
	}
	return nil
}

func isInsecureRegistry(registry string) bool {
	for _, item := range strings.Split(os.Getenv("COMPOSE_UPDATER_INSECURE_REGISTRIES"), ",") {
		if strings.EqualFold(strings.TrimSpace(item), registry) {
			return true
		}
	}
	return false
}

func deduplicateTags(values []TagInfo) []TagInfo {
	positions := make(map[string]int, len(values))
	result := make([]TagInfo, 0, len(values))
	for _, value := range values {
		if value.Name == "" {
			continue
		}
		if position, ok := positions[value.Name]; ok {
			if value.LastUpdated.After(result[position].LastUpdated) {
				result[position] = value
			}
			continue
		}
		positions[value.Name] = len(result)
		result = append(result, value)
	}
	return result
}
