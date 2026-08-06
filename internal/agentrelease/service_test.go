package agentrelease

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Relayward/relayward/internal/githubrelease"
)

func TestServiceValidatesAndCachesLatestRelease(t *testing.T) {
	now := time.Date(2026, time.August, 5, 10, 0, 0, 0, time.UTC)
	source := &sourceStub{release: validRelease(now)}
	service, err := New(source)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	service.now = func() time.Time { return now }

	first, err := service.Latest(context.Background())
	if err != nil || first.Version != "1.2.3" || first.CheckedAt != now {
		t.Fatalf("Latest() = %+v, %v", first, err)
	}
	if _, err := service.Latest(context.Background()); err != nil || source.calls != 1 {
		t.Fatalf("cached Latest() calls = %d, error = %v", source.calls, err)
	}
	now = now.Add(defaultCacheLife)
	if _, err := service.Latest(context.Background()); err != nil || source.calls != 2 {
		t.Fatalf("expired Latest() calls = %d, error = %v", source.calls, err)
	}
}

func TestServiceRejectsIncompleteReleaseWithoutCaching(t *testing.T) {
	now := time.Now().UTC()
	value := validRelease(now)
	delete(value.Assets, ManifestAsset)
	source := &sourceStub{release: value}
	service, _ := New(source)
	if _, err := service.Latest(context.Background()); err == nil {
		t.Fatal("Latest() accepted a release without its manifest")
	}
	if _, err := service.Latest(context.Background()); err == nil || source.calls != 2 {
		t.Fatalf("invalid release was cached: calls = %d, error = %v", source.calls, err)
	}
}

func TestServiceDoesNotHideSourceFailureWithExpiredCache(t *testing.T) {
	now := time.Now().UTC()
	source := &sourceStub{release: validRelease(now)}
	service, _ := New(source)
	service.now = func() time.Time { return now }
	if _, err := service.Latest(context.Background()); err != nil {
		t.Fatalf("initial Latest() error = %v", err)
	}
	now = now.Add(defaultCacheLife)
	source.err = errors.New("offline")
	if _, err := service.Latest(context.Background()); err == nil {
		t.Fatal("Latest() silently returned stale data")
	}
}

func TestCompareSemanticVersions(t *testing.T) {
	tests := []struct {
		left, right string
		want        int
	}{
		{left: "1.2.3", right: "1.2.3", want: 0},
		{left: "1.2.3", right: "1.2.4", want: -1},
		{left: "2.0.0", right: "1.99.99", want: 1},
		{left: "1.0.0-rc.2", right: "1.0.0-rc.10", want: -1},
		{left: "1.0.0-rc.1", right: "1.0.0", want: -1},
		{left: "1.0.0+one", right: "1.0.0+two", want: 0},
	}
	for _, test := range tests {
		got, err := Compare(test.left, test.right)
		if err != nil || got != test.want {
			t.Errorf("Compare(%q, %q) = %d, %v; want %d", test.left, test.right, got, err, test.want)
		}
	}
	if _, err := Compare("dev", "1.0.0"); err == nil {
		t.Fatal("Compare() accepted a development version")
	}
}

type sourceStub struct {
	release githubrelease.StableRelease
	err     error
	calls   int
}

func (source *sourceStub) LatestStable(context.Context, string, string) (githubrelease.StableRelease, error) {
	source.calls++
	return source.release, source.err
}

func validRelease(publishedAt time.Time) githubrelease.StableRelease {
	return githubrelease.StableRelease{
		ID: 1, Repository: githubrelease.Repository{Owner: "Relayward", Name: "relayward-agent"},
		Tag: "v1.2.3", Version: "1.2.3", PublishedAt: publishedAt,
		Assets: map[string]githubrelease.Asset{
			ManifestAsset:   {ID: 2, Name: ManifestAsset, Size: 100},
			LinuxAMD64Asset: {ID: 3, Name: LinuxAMD64Asset, Size: 1_000},
		},
	}
}
