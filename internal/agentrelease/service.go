package agentrelease

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Relayward/relayward-sdk/contract"

	"github.com/Relayward/relayward/internal/githubrelease"
)

const (
	RepositoryURL    = "https://github.com/Relayward/relayward-agent"
	ManifestAsset    = "relayward-agent-manifest.json"
	LinuxAMD64Asset  = "relayward-agent-linux-amd64"
	defaultCacheLife = 15 * time.Minute
)

type Source interface {
	LatestStable(context.Context, string, string) (githubrelease.StableRelease, error)
}

type Release struct {
	Version     string
	Tag         string
	PublishedAt time.Time
	CheckedAt   time.Time
}

type Service struct {
	source    Source
	now       func() time.Time
	cacheLife time.Duration
	mu        sync.Mutex
	cached    Release
	expiresAt time.Time
}

func New(source Source) (*Service, error) {
	if source == nil {
		return nil, errors.New("Agent release source is required")
	}
	return &Service{source: source, now: time.Now, cacheLife: defaultCacheLife}, nil
}

func (service *Service) Latest(ctx context.Context) (Release, error) {
	now := service.now().UTC()
	service.mu.Lock()
	if service.cached.Version != "" && now.Before(service.expiresAt) {
		cached := service.cached
		service.mu.Unlock()
		return cached, nil
	}
	service.mu.Unlock()

	value, err := service.source.LatestStable(ctx, RepositoryURL, "")
	if err != nil {
		return Release{}, fmt.Errorf("read latest Agent release: %w", err)
	}
	if value.Repository.URL() != RepositoryURL || value.Tag != "v"+value.Version {
		return Release{}, errors.New("latest Agent release identity is invalid")
	}
	for _, name := range []string{ManifestAsset, LinuxAMD64Asset} {
		asset, exists := value.Assets[name]
		if !exists || asset.Size < 1 {
			return Release{}, fmt.Errorf("latest Agent release is missing %s", name)
		}
	}
	release := Release{
		Version: value.Version, Tag: value.Tag, PublishedAt: value.PublishedAt.UTC(), CheckedAt: now,
	}
	service.mu.Lock()
	service.cached = release
	service.expiresAt = now.Add(service.cacheLife)
	service.mu.Unlock()
	return release, nil
}

func Compare(current, latest string) (int, error) {
	left, err := parseVersion(current)
	if err != nil {
		return 0, fmt.Errorf("current version: %w", err)
	}
	right, err := parseVersion(latest)
	if err != nil {
		return 0, fmt.Errorf("latest version: %w", err)
	}
	for index := range left.core {
		if compared := compareNumeric(left.core[index], right.core[index]); compared != 0 {
			return compared, nil
		}
	}
	switch {
	case len(left.pre) == 0 && len(right.pre) == 0:
		return 0, nil
	case len(left.pre) == 0:
		return 1, nil
	case len(right.pre) == 0:
		return -1, nil
	}
	for index := 0; index < len(left.pre) && index < len(right.pre); index++ {
		leftNumeric, rightNumeric := numeric(left.pre[index]), numeric(right.pre[index])
		switch {
		case leftNumeric && rightNumeric:
			if compared := compareNumeric(left.pre[index], right.pre[index]); compared != 0 {
				return compared, nil
			}
		case leftNumeric:
			return -1, nil
		case rightNumeric:
			return 1, nil
		case left.pre[index] < right.pre[index]:
			return -1, nil
		case left.pre[index] > right.pre[index]:
			return 1, nil
		}
	}
	switch {
	case len(left.pre) < len(right.pre):
		return -1, nil
	case len(left.pre) > len(right.pre):
		return 1, nil
	default:
		return 0, nil
	}
}

type version struct {
	core [3]string
	pre  []string
}

func parseVersion(value string) (version, error) {
	if err := contract.ValidateSemanticVersion(value); err != nil {
		return version{}, err
	}
	withoutBuild := strings.SplitN(value, "+", 2)[0]
	parts := strings.SplitN(withoutBuild, "-", 2)
	core := strings.Split(parts[0], ".")
	parsed := version{core: [3]string{core[0], core[1], core[2]}}
	if len(parts) == 2 {
		parsed.pre = strings.Split(parts[1], ".")
	}
	return parsed, nil
}

func numeric(value string) bool {
	for _, character := range value {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}

func compareNumeric(left, right string) int {
	switch {
	case len(left) < len(right):
		return -1
	case len(left) > len(right):
		return 1
	case left < right:
		return -1
	case left > right:
		return 1
	default:
		return 0
	}
}
