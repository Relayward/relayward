package githubrelease

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Relayward/relayward-sdk/contract"
	"github.com/Relayward/relayward-sdk/manifest"
)

const (
	ManifestAssetName       = "relayward-plugin.json"
	maximumManifestBytes    = 1 << 20
	maximumReleaseJSON      = 2 << 20
	maximumReleasePages     = 10
	releasesPerPage         = 100
	maximumRedirects        = 5
	defaultRequestTimeout   = 30 * time.Second
	artifactDownloadTimeout = 5 * time.Minute
)

var (
	ErrNotFound     = errors.New("GitHub release not found")
	ErrUnauthorized = errors.New("GitHub repository authorization failed")
	ErrRateLimited  = errors.New("GitHub API rate limit exceeded")
	repositoryPart  = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)
)

type Repository struct {
	Owner string
	Name  string
}

func (repository Repository) URL() string {
	return "https://github.com/" + repository.Owner + "/" + repository.Name
}

type Asset struct {
	ID   int64
	Name string
	Size int64
}

type Release struct {
	ID         int64
	Repository Repository
	Tag        string
	Manifest   manifest.Manifest
	Assets     map[manifest.ArtifactRole]Asset
}

type StableRelease struct {
	ID          int64
	Repository  Repository
	Tag         string
	Version     string
	PublishedAt time.Time
	Assets      map[string]Asset
}

type ReleaseVersion struct {
	Tag         string
	Version     string
	PublishedAt time.Time
}

type Client struct {
	httpClient          *http.Client
	apiBase             *url.URL
	validateArtifactURL func(*url.URL) error
}

func NewClient(client *http.Client) *Client {
	base, _ := url.Parse("https://api.github.com")
	return newClient(client, base, validateGitHubArtifactURL)
}

func newClient(client *http.Client, apiBase *url.URL, validateArtifactURL func(*url.URL) error) *Client {
	if client == nil {
		client = &http.Client{}
	}
	copyClient := *client
	copyClient.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	return &Client{httpClient: &copyClient, apiBase: apiBase, validateArtifactURL: validateArtifactURL}
}

func ParseRepository(raw string) (Repository, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme != "https" || !strings.EqualFold(parsed.Host, "github.com") ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return Repository{}, errors.New("repository must be an HTTPS github.com URL without credentials")
	}
	parts := strings.Split(strings.Trim(parsed.EscapedPath(), "/"), "/")
	if len(parts) != 2 {
		return Repository{}, errors.New("repository must contain exactly an owner and repository")
	}
	owner, err := url.PathUnescape(parts[0])
	if err != nil {
		return Repository{}, errors.New("repository owner is invalid")
	}
	name, err := url.PathUnescape(parts[1])
	if err != nil {
		return Repository{}, errors.New("repository name is invalid")
	}
	name = strings.TrimSuffix(name, ".git")
	if !validRepositoryPart(owner) || !validRepositoryPart(name) {
		return Repository{}, errors.New("repository owner or name is invalid")
	}
	return Repository{Owner: owner, Name: name}, nil
}

func (client *Client) Inspect(ctx context.Context, rawRepository, version, token string) (Release, error) {
	ctx, cancel := context.WithTimeout(ctx, defaultRequestTimeout)
	defer cancel()
	repository, err := ParseRepository(rawRepository)
	if err != nil {
		return Release{}, err
	}
	if err := validateToken(token); err != nil {
		return Release{}, err
	}
	endpoint := client.repositoryEndpoint(repository) + "/releases/latest"
	if version = strings.TrimSpace(version); version != "" {
		if err := contract.ValidateSemanticVersion(version); err != nil {
			return Release{}, fmt.Errorf("version: %w", err)
		}
		endpoint = client.repositoryEndpoint(repository) + "/releases/tags/" + url.PathEscape("v"+version)
	}
	var response releaseResponse
	if err := client.getJSON(ctx, endpoint, token, &response); err != nil {
		return Release{}, err
	}
	if response.ID < 1 || response.Draft || response.Prerelease || !strings.HasPrefix(response.TagName, "v") {
		return Release{}, errors.New("GitHub release is not a published stable release")
	}
	releaseVersion := strings.TrimPrefix(response.TagName, "v")
	if err := contract.ValidateSemanticVersion(releaseVersion); err != nil || (version != "" && releaseVersion != version) {
		return Release{}, errors.New("GitHub release tag is not the requested semantic version")
	}
	assetsByName := make(map[string]Asset, len(response.Assets))
	for _, candidate := range response.Assets {
		if candidate.ID < 1 || candidate.Name == "" || candidate.Size < 0 {
			return Release{}, errors.New("GitHub release contains invalid asset metadata")
		}
		if _, exists := assetsByName[candidate.Name]; exists {
			return Release{}, fmt.Errorf("GitHub release contains duplicate asset %q", candidate.Name)
		}
		assetsByName[candidate.Name] = Asset{ID: candidate.ID, Name: candidate.Name, Size: candidate.Size}
	}
	manifestAsset, exists := assetsByName[ManifestAssetName]
	if !exists {
		return Release{}, fmt.Errorf("GitHub release does not contain %s", ManifestAssetName)
	}
	rawManifest, err := client.downloadAssetBytes(ctx, repository, manifestAsset.ID, token, maximumManifestBytes)
	if err != nil {
		return Release{}, fmt.Errorf("download plugin manifest: %w", err)
	}
	pluginManifest, err := manifest.Decode(bytes.NewReader(rawManifest))
	if err != nil {
		return Release{}, err
	}
	if pluginManifest.Version != releaseVersion {
		return Release{}, errors.New("plugin manifest version does not match the GitHub release tag")
	}
	if err := manifest.CheckCompatibility(pluginManifest, contract.SupportedAPIs{
		Control: []uint32{contract.ControlAPIMajor}, Agent: []uint32{contract.AgentAPIMajor}, UI: []uint32{contract.UIAPIMajor},
	}); err != nil {
		return Release{}, fmt.Errorf("plugin is incompatible: %w", err)
	}
	artifacts := make(map[manifest.ArtifactRole]Asset, len(pluginManifest.Artifacts))
	for _, declared := range pluginManifest.Artifacts {
		asset, exists := assetsByName[declared.File]
		if !exists {
			return Release{}, fmt.Errorf("GitHub release does not contain declared asset %q", declared.File)
		}
		if asset.Size != declared.Size {
			return Release{}, fmt.Errorf("GitHub asset %q size does not match the manifest", declared.File)
		}
		artifacts[declared.Role] = asset
	}
	return Release{ID: response.ID, Repository: repository, Tag: response.TagName, Manifest: pluginManifest, Assets: artifacts}, nil
}

func (client *Client) LatestStable(ctx context.Context, rawRepository, token string) (StableRelease, error) {
	ctx, cancel := context.WithTimeout(ctx, defaultRequestTimeout)
	defer cancel()
	repository, err := ParseRepository(rawRepository)
	if err != nil {
		return StableRelease{}, err
	}
	if err := validateToken(token); err != nil {
		return StableRelease{}, err
	}
	var response releaseResponse
	if err := client.getJSON(ctx, client.repositoryEndpoint(repository)+"/releases/latest", token, &response); err != nil {
		return StableRelease{}, err
	}
	if response.ID < 1 || response.Draft || response.Prerelease || response.PublishedAt.IsZero() || !strings.HasPrefix(response.TagName, "v") {
		return StableRelease{}, errors.New("GitHub release is not a published stable release")
	}
	version := strings.TrimPrefix(response.TagName, "v")
	if err := contract.ValidateSemanticVersion(version); err != nil || strings.Contains(strings.SplitN(version, "+", 2)[0], "-") {
		return StableRelease{}, errors.New("GitHub release tag is not a stable semantic version")
	}
	assets := make(map[string]Asset, len(response.Assets))
	for _, candidate := range response.Assets {
		if candidate.ID < 1 || candidate.Name == "" || candidate.Size < 0 {
			return StableRelease{}, errors.New("GitHub release contains invalid asset metadata")
		}
		if _, exists := assets[candidate.Name]; exists {
			return StableRelease{}, fmt.Errorf("GitHub release contains duplicate asset %q", candidate.Name)
		}
		assets[candidate.Name] = Asset{ID: candidate.ID, Name: candidate.Name, Size: candidate.Size}
	}
	return StableRelease{
		ID: response.ID, Repository: repository, Tag: response.TagName, Version: version,
		PublishedAt: response.PublishedAt.UTC(), Assets: assets,
	}, nil
}

func (client *Client) ListStableVersions(ctx context.Context, rawRepository, token string) ([]ReleaseVersion, error) {
	ctx, cancel := context.WithTimeout(ctx, defaultRequestTimeout)
	defer cancel()
	repository, err := ParseRepository(rawRepository)
	if err != nil {
		return nil, err
	}
	if err := validateToken(token); err != nil {
		return nil, err
	}
	versions := make([]ReleaseVersion, 0)
	seen := make(map[string]struct{})
	for page := 1; page <= maximumReleasePages; page++ {
		endpoint := client.repositoryEndpoint(repository) + "/releases?per_page=" + strconv.Itoa(releasesPerPage) +
			"&page=" + strconv.Itoa(page)
		var response []releaseResponse
		if err := client.getJSON(ctx, endpoint, token, &response); err != nil {
			return nil, err
		}
		for _, candidate := range response {
			version, valid := stablePluginVersion(candidate)
			if !valid {
				continue
			}
			if _, duplicate := seen[version]; duplicate {
				continue
			}
			seen[version] = struct{}{}
			versions = append(versions, ReleaseVersion{
				Tag: candidate.TagName, Version: version, PublishedAt: candidate.PublishedAt.UTC(),
			})
		}
		if len(response) < releasesPerPage {
			return versions, nil
		}
	}
	return nil, errors.New("GitHub repository has too many releases")
}

func (client *Client) DownloadAsset(ctx context.Context, repository Repository, asset Asset, token, expectedSHA256 string, destination io.Writer) error {
	ctx, cancel := context.WithTimeout(ctx, artifactDownloadTimeout)
	defer cancel()
	if asset.ID < 1 || asset.Size < 1 {
		return errors.New("asset metadata is invalid")
	}
	if err := validateToken(token); err != nil {
		return err
	}
	response, err := client.openAsset(ctx, repository, asset.ID, token, true)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.ContentLength >= 0 && response.ContentLength != asset.Size {
		return errors.New("downloaded asset size does not match release metadata")
	}
	hash := sha256.New()
	written, err := io.Copy(io.MultiWriter(destination, hash), io.LimitReader(response.Body, asset.Size+1))
	if err != nil {
		return errors.New("read GitHub release asset")
	}
	if written != asset.Size {
		return errors.New("downloaded asset size does not match release metadata")
	}
	if actual := hex.EncodeToString(hash.Sum(nil)); actual != expectedSHA256 {
		return errors.New("downloaded asset SHA-256 does not match the manifest")
	}
	return nil
}

func (client *Client) ResolveAssetURL(ctx context.Context, repository Repository, assetID int64, token string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, defaultRequestTimeout)
	defer cancel()
	if assetID < 1 || token == "" {
		return "", errors.New("a private asset and GitHub token are required")
	}
	if err := validateToken(token); err != nil {
		return "", err
	}
	endpoint := client.assetEndpoint(repository, assetID)
	request, err := client.newRequest(ctx, endpoint, token)
	if err != nil {
		return "", err
	}
	response, err := client.httpClient.Do(request)
	if err != nil {
		return "", errors.New("request GitHub release asset")
	}
	defer response.Body.Close()
	if response.StatusCode < 300 || response.StatusCode > 399 {
		return "", githubStatusError(response.StatusCode)
	}
	location, err := response.Location()
	if err != nil || client.validateArtifactURL(location) != nil {
		return "", errors.New("GitHub returned an invalid release asset redirect")
	}
	return location.String(), nil
}

func (client *Client) getJSON(ctx context.Context, endpoint, token string, destination any) error {
	request, err := client.newRequest(ctx, endpoint, token)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	response, err := client.httpClient.Do(request)
	if err != nil {
		return errors.New("request GitHub API")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return githubStatusError(response.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, maximumReleaseJSON+1))
	if err != nil {
		return errors.New("read GitHub API response")
	}
	if len(raw) > maximumReleaseJSON {
		return errors.New("GitHub API response is too large")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if err := decoder.Decode(destination); err != nil {
		return errors.New("decode GitHub API response")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("decode GitHub API response")
	}
	return nil
}

func (client *Client) downloadAssetBytes(ctx context.Context, repository Repository, assetID int64, token string, maximum int64) ([]byte, error) {
	response, err := client.openAsset(ctx, repository, assetID, token, true)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, maximum+1))
	if err != nil {
		return nil, errors.New("read GitHub release asset")
	}
	if int64(len(raw)) > maximum {
		return nil, errors.New("GitHub release asset is too large")
	}
	return raw, nil
}

func (client *Client) openAsset(ctx context.Context, repository Repository, assetID int64, token string, follow bool) (*http.Response, error) {
	endpoint := client.assetEndpoint(repository, assetID)
	for redirects := 0; ; redirects++ {
		requestToken := ""
		if redirects == 0 {
			requestToken = token
		}
		request, err := client.newRequest(ctx, endpoint, requestToken)
		if err != nil {
			return nil, err
		}
		request.Header.Set("Accept", "application/octet-stream")
		response, err := client.httpClient.Do(request)
		if err != nil {
			return nil, errors.New("request GitHub release asset")
		}
		if response.StatusCode == http.StatusOK {
			return response, nil
		}
		if !follow || response.StatusCode < 300 || response.StatusCode > 399 || redirects >= maximumRedirects {
			response.Body.Close()
			return nil, githubStatusError(response.StatusCode)
		}
		location, err := response.Location()
		response.Body.Close()
		if err != nil || client.validateArtifactURL(location) != nil {
			return nil, errors.New("GitHub returned an invalid release asset redirect")
		}
		endpoint = location.String()
	}
}

func (client *Client) newRequest(ctx context.Context, endpoint, token string) (*http.Request, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, errors.New("create GitHub request")
	}
	request.Header.Set("User-Agent", "Relayward")
	request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	return request, nil
}

func (client *Client) repositoryEndpoint(repository Repository) string {
	return strings.TrimRight(client.apiBase.String(), "/") + "/repos/" + url.PathEscape(repository.Owner) + "/" + url.PathEscape(repository.Name)
}

func (client *Client) assetEndpoint(repository Repository, assetID int64) string {
	return client.repositoryEndpoint(repository) + "/releases/assets/" + strconv.FormatInt(assetID, 10)
}

func validRepositoryPart(value string) bool {
	return repositoryPart.MatchString(value) && value != "." && value != ".."
}

func validateToken(value string) error {
	if value != strings.TrimSpace(value) || len(value) > 1024 || strings.ContainsAny(value, "\x00\r\n") {
		return errors.New("GitHub token is invalid")
	}
	return nil
}

func validateGitHubArtifactURL(value *url.URL) error {
	if value == nil || value.Scheme != "https" || value.User != nil || value.Fragment != "" || value.Port() != "" {
		return errors.New("release asset URL is invalid")
	}
	switch strings.ToLower(value.Hostname()) {
	case "github.com", "release-assets.githubusercontent.com", "objects.githubusercontent.com":
		return nil
	default:
		return errors.New("release asset host is not allowed")
	}
}

func githubStatusError(statusCode int) error {
	switch statusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		if statusCode == http.StatusForbidden {
			return ErrRateLimited
		}
		return ErrUnauthorized
	case http.StatusNotFound:
		return ErrNotFound
	default:
		return fmt.Errorf("GitHub request failed with HTTP %d", statusCode)
	}
}

type releaseResponse struct {
	ID          int64     `json:"id"`
	TagName     string    `json:"tag_name"`
	Draft       bool      `json:"draft"`
	Prerelease  bool      `json:"prerelease"`
	PublishedAt time.Time `json:"published_at"`
	Assets      []struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
		Size int64  `json:"size"`
	} `json:"assets"`
}

func stablePluginVersion(value releaseResponse) (string, bool) {
	if value.ID < 1 || value.Draft || value.Prerelease || value.PublishedAt.IsZero() || !strings.HasPrefix(value.TagName, "v") {
		return "", false
	}
	version := strings.TrimPrefix(value.TagName, "v")
	if err := contract.ValidateSemanticVersion(version); err != nil || strings.Contains(strings.SplitN(version, "+", 2)[0], "-") {
		return "", false
	}
	for _, asset := range value.Assets {
		if asset.ID > 0 && asset.Name == ManifestAssetName && asset.Size > 0 {
			return version, true
		}
	}
	return "", false
}
