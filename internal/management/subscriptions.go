package management

import (
	"context"
	"strings"

	"github.com/Relayward/relayward/internal/auth"
	"github.com/Relayward/relayward/internal/store"
)

const subscriptionTokenLength = 47

func (service *Service) Subscription(ctx context.Context, token string) (store.SubscriptionSnapshot, error) {
	if len(token) != subscriptionTokenLength || !strings.HasPrefix(token, "rws_") {
		return store.SubscriptionSnapshot{}, store.ErrNotFound
	}
	return service.store.SubscriptionByTokenHash(ctx, auth.TokenHash(token))
}
