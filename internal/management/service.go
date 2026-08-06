package management

import (
	"context"
	"fmt"
	"net/mail"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/Relayward/relayward/internal/auth"
	"github.com/Relayward/relayward/internal/secretbox"
	"github.com/Relayward/relayward/internal/store"
)

const registrationTokenLifetime = 15 * time.Minute

type Service struct {
	store             *store.Store
	secrets           *secretbox.Manager
	now               func() time.Time
	agentReleases     agentReleaseProvider
	pluginMu          sync.Mutex
	pluginReleases    pluginReleaseClient
	pluginArtifacts   pluginArtifactStore
	pluginRuntime     centerPluginRuntime
	subscriptionLocks sync.Map
}

func (service *Service) subscriptionLock(authorizationID string) *sync.Mutex {
	value, _ := service.subscriptionLocks.LoadOrStore(authorizationID, &sync.Mutex{})
	return value.(*sync.Mutex)
}

func NewService(database *store.Store, secrets *secretbox.Manager) *Service {
	return &Service{store: database, secrets: secrets, now: time.Now}
}

type NodeInput struct {
	Name          string
	PublicAddress string
	Enabled       bool
}

type RegistrationToken struct {
	Token     string
	ExpiresAt time.Time
}

func (service *Service) ListNodes(ctx context.Context) ([]store.Node, error) {
	return service.store.ListNodes(ctx)
}

func (service *Service) Node(ctx context.Context, id string) (store.Node, error) {
	if err := validateID("node_id", id); err != nil {
		return store.Node{}, err
	}
	return service.store.NodeByID(ctx, id)
}

func (service *Service) CreateNode(ctx context.Context, input NodeInput) (store.Node, error) {
	value, err := normalizeNode(uuid.NewString(), input)
	if err != nil {
		return store.Node{}, err
	}
	now := service.currentTime()
	value.CreatedAt = now
	value.UpdatedAt = now
	if err := service.store.CreateNode(ctx, value, now); err != nil {
		return store.Node{}, err
	}
	return value, nil
}

func (service *Service) UpdateNode(ctx context.Context, id string, input NodeInput) (store.Node, error) {
	if err := validateID("node_id", id); err != nil {
		return store.Node{}, err
	}
	value, err := normalizeNode(id, input)
	if err != nil {
		return store.Node{}, err
	}
	now := service.currentTime()
	if err := service.store.UpdateNode(ctx, value, now); err != nil {
		return store.Node{}, err
	}
	return service.store.NodeByID(ctx, id)
}

func (service *Service) DeleteNode(ctx context.Context, id string) error {
	if err := validateID("node_id", id); err != nil {
		return err
	}
	return service.store.DeleteNode(ctx, id, service.currentTime())
}

func (service *Service) RevokeNodeCredential(ctx context.Context, id string) (store.Node, error) {
	if err := validateID("node_id", id); err != nil {
		return store.Node{}, err
	}
	if err := service.store.RevokeNodeCredential(ctx, id, service.currentTime()); err != nil {
		return store.Node{}, err
	}
	return service.store.NodeByID(ctx, id)
}

func (service *Service) CreateRegistrationToken(ctx context.Context, nodeID string) (RegistrationToken, error) {
	if err := validateID("node_id", nodeID); err != nil {
		return RegistrationToken{}, err
	}
	value, err := auth.NewToken(32)
	if err != nil {
		return RegistrationToken{}, fmt.Errorf("generate registration token: %w", err)
	}
	token := "rwr_" + value
	now := service.currentTime()
	expiresAt := now.Add(registrationTokenLifetime)
	if err := service.store.CreateNodeRegistrationToken(ctx, nodeID, auth.TokenHash(token), expiresAt, now); err != nil {
		return RegistrationToken{}, err
	}
	return RegistrationToken{Token: token, ExpiresAt: expiresAt}, nil
}

type UserInput struct {
	DisplayName string
	Email       *string
	Telegram    *string
	Note        string
}

func (service *Service) ListUsers(ctx context.Context) ([]store.User, error) {
	return service.store.ListUsers(ctx)
}

func (service *Service) User(ctx context.Context, id string) (store.User, error) {
	if err := validateID("user_id", id); err != nil {
		return store.User{}, err
	}
	return service.store.UserByID(ctx, id)
}

func (service *Service) CreateUser(ctx context.Context, input UserInput) (store.User, error) {
	value, err := normalizeUser(uuid.NewString(), input)
	if err != nil {
		return store.User{}, err
	}
	now := service.currentTime()
	value.CreatedAt = now
	value.UpdatedAt = now
	if err := service.store.CreateUser(ctx, value, now); err != nil {
		return store.User{}, err
	}
	return value, nil
}

func (service *Service) UpdateUser(ctx context.Context, id string, input UserInput) (store.User, error) {
	if err := validateID("user_id", id); err != nil {
		return store.User{}, err
	}
	value, err := normalizeUser(id, input)
	if err != nil {
		return store.User{}, err
	}
	if err := service.store.UpdateUser(ctx, value, service.currentTime()); err != nil {
		return store.User{}, err
	}
	return service.store.UserByID(ctx, id)
}

func (service *Service) DeleteUser(ctx context.Context, id string) error {
	if err := validateID("user_id", id); err != nil {
		return err
	}
	return service.store.DeleteUser(ctx, id, service.currentTime())
}

func normalizeNode(id string, input NodeInput) (store.Node, error) {
	name, err := normalizedRequired("name", input.Name, 100)
	if err != nil {
		return store.Node{}, err
	}
	address, err := normalizedOptional("public_address", input.PublicAddress, 255)
	if err != nil {
		return store.Node{}, err
	}
	return store.Node{ID: id, Name: name, PublicAddress: address, Enabled: input.Enabled}, nil
}

func normalizeUser(id string, input UserInput) (store.User, error) {
	displayName, err := normalizedRequired("display_name", input.DisplayName, 100)
	if err != nil {
		return store.User{}, err
	}
	email, err := normalizedPointer("email", input.Email, 320)
	if err != nil {
		return store.User{}, err
	}
	if email != nil {
		parsed, parseErr := mail.ParseAddress(*email)
		if parseErr != nil || parsed.Address != *email {
			return store.User{}, invalid("email", "must be a plain email address")
		}
	}
	telegram, err := normalizedPointer("telegram", input.Telegram, 128)
	if err != nil {
		return store.User{}, err
	}
	note, err := normalizedMultiline("note", input.Note, 4096)
	if err != nil {
		return store.User{}, err
	}
	return store.User{ID: id, DisplayName: displayName, Email: email, Telegram: telegram, Note: note}, nil
}

func validateID(field, value string) error {
	if _, err := uuid.Parse(value); err != nil {
		return invalid(field, "must be a valid UUID")
	}
	return nil
}

func normalizedRequired(field, value string, maxRunes int) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", invalid(field, "is required")
	}
	if err := validateText(field, value, maxRunes); err != nil {
		return "", err
	}
	return value, nil
}

func normalizedOptional(field, value string, maxRunes int) (string, error) {
	value = strings.TrimSpace(value)
	if err := validateText(field, value, maxRunes); err != nil {
		return "", err
	}
	return value, nil
}

func normalizedMultiline(field, value string, maxRunes int) (string, error) {
	value = strings.TrimSpace(value)
	if !utf8.ValidString(value) {
		return "", invalid(field, "must be valid UTF-8")
	}
	if utf8.RuneCountInString(value) > maxRunes {
		return "", invalid(field, fmt.Sprintf("must contain at most %d characters", maxRunes))
	}
	for _, character := range value {
		if unicode.IsControl(character) && character != '\n' && character != '\t' {
			return "", invalid(field, "must not contain control characters other than newlines and tabs")
		}
	}
	return value, nil
}

func normalizedPointer(field string, value *string, maxRunes int) (*string, error) {
	if value == nil {
		return nil, nil
	}
	normalized, err := normalizedOptional(field, *value, maxRunes)
	if err != nil {
		return nil, err
	}
	if normalized == "" {
		return nil, nil
	}
	return &normalized, nil
}

func validateText(field, value string, maxRunes int) error {
	if !utf8.ValidString(value) {
		return invalid(field, "must be valid UTF-8")
	}
	if utf8.RuneCountInString(value) > maxRunes {
		return invalid(field, fmt.Sprintf("must contain at most %d characters", maxRunes))
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return invalid(field, "must not contain control characters")
		}
	}
	return nil
}

func (service *Service) currentTime() time.Time {
	return service.now().UTC().Truncate(time.Second)
}
