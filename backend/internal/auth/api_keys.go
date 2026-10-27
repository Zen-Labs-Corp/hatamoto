// Package auth provides authentication and authorization functionality.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/database"
)

// API key errors.
var (
	ErrAPIKeyNotFound    = errors.New("api key not found")
	ErrAPIKeyExpired     = errors.New("api key has expired")
	ErrAPIKeyRevoked     = errors.New("api key has been revoked")
	ErrAPIKeyRateLimit   = errors.New("api key rate limit exceeded")
	ErrAPIKeyInvalidHash = errors.New("invalid api key")
	ErrAPIKeyCreatorGone = errors.New("api key creator is missing or no longer an owner/editor")
	ErrInvalidScope      = errors.New("invalid scope")
	ErrInvalidRateLimit  = errors.New("rate_limit must be a positive integer")
)

// APIKeyScope represents a permission scope for an API key.
type APIKeyScope string

// Available API key scopes.
const (
	ScopeReadEntities     APIKeyScope = "read:entities"
	ScopeWriteEntities    APIKeyScope = "write:entities"
	ScopeReadCitations    APIKeyScope = "read:citations"
	ScopeReadReports      APIKeyScope = "read:reports"
	ScopeWriteReports     APIKeyScope = "write:reports"
	ScopeReadQuerySuites  APIKeyScope = "read:query_suites"
	ScopeWriteQuerySuites APIKeyScope = "write:query_suites"
	// Site-structure write scopes. They are separate from write:entities so
	// keys issued for publishing content never gain them.
	ScopeWriteMedia     APIKeyScope = "write:media"
	ScopeWriteMenus     APIKeyScope = "write:menus"
	ScopeWriteRedirects APIKeyScope = "write:redirects"
	ScopeWriteTerms     APIKeyScope = "write:terms"
	ScopeAll            APIKeyScope = "*"
)

// ValidScopes returns all valid API key scopes.
func ValidScopes() []APIKeyScope {
	return []APIKeyScope{
		ScopeReadEntities,
		ScopeWriteEntities,
		ScopeReadCitations,
		ScopeReadReports,
		ScopeWriteReports,
		ScopeReadQuerySuites,
		ScopeWriteQuerySuites,
		ScopeWriteMedia,
		ScopeWriteMenus,
		ScopeWriteRedirects,
		ScopeWriteTerms,
		ScopeAll,
	}
}

// IsValidScope checks if a scope string is valid.
func IsValidScope(scope string) bool {
	for _, s := range ValidScopes() {
		if string(s) == scope {
			return true
		}
	}
	return false
}

// APIKey represents an API key record.
type APIKey struct {
	ID           uuid.UUID     `json:"id"`
	Name         string        `json:"name"`
	Description  string        `json:"description,omitempty"`
	KeyPrefix    string        `json:"key_prefix"`
	KeyHash      string        `json:"-"` // Never expose the hash
	Scopes       []APIKeyScope `json:"scopes"`
	RateLimit    int           `json:"rate_limit"`
	ExpiresAt    *time.Time    `json:"expires_at,omitempty"`
	LastUsedAt   *time.Time    `json:"last_used_at,omitempty"`
	LastUsedIP   string        `json:"last_used_ip,omitempty"`
	RequestCount int64         `json:"request_count"`
	IsActive     bool          `json:"is_active"`
	CreatedBy    *uuid.UUID    `json:"created_by,omitempty"`
	CreatedAt    time.Time     `json:"created_at"`
	UpdatedAt    time.Time     `json:"updated_at"`
	RevokedAt    *time.Time    `json:"revoked_at,omitempty"`
	RevokedBy    *uuid.UUID    `json:"revoked_by,omitempty"`

	// CreatorRole is the creator's CURRENT users.role, filled by ValidateKey
	// only ("" when the user is missing or the key was not validated). A key
	// acts with its creator's authority, never more.
	CreatorRole string `json:"-"`
}

// CreatorCanAct reports whether the key's creator still holds a role that may
// edit content (owner or editor). An unknown or empty role fails closed.
func (k *APIKey) CreatorCanAct() bool {
	return k.CreatorRole == "owner" || k.CreatorRole == "editor"
}

// APIKeyWithSecret is returned only when creating a new key.
// The plain key is shown once and never stored.
type APIKeyWithSecret struct {
	APIKey
	PlainKey string `json:"key"` // Only returned on creation
}

// CreateAPIKeyInput contains parameters for creating a new API key.
type CreateAPIKeyInput struct {
	Name        string        `json:"name"`
	Description string        `json:"description,omitempty"`
	Scopes      []APIKeyScope `json:"scopes"`
	RateLimit   int           `json:"rate_limit,omitempty"`
	ExpiresAt   *time.Time    `json:"expires_at,omitempty"`
}

// UpdateAPIKeyInput contains parameters for updating an API key.
type UpdateAPIKeyInput struct {
	Name        *string       `json:"name,omitempty"`
	Description *string       `json:"description,omitempty"`
	Scopes      []APIKeyScope `json:"scopes,omitempty"`
	RateLimit   *int          `json:"rate_limit,omitempty"`
	IsActive    *bool         `json:"is_active,omitempty"`
}

// APIKeyService manages API keys.
type APIKeyService struct {
	db     *database.DB
	logger *slog.Logger
}

// NewAPIKeyService creates a new API key service.
func NewAPIKeyService(db *database.DB, logger *slog.Logger) *APIKeyService {
	return &APIKeyService{
		db:     db,
		logger: logger,
	}
}

// generateAPIKey creates a new random API key.
// Format: nxk_<32 random hex chars> (e.g., nxk_a1b2c3d4e5f6...)
func generateAPIKey() (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("failed to generate random bytes: %w", err)
	}
	return "nxk_" + hex.EncodeToString(bytes), nil
}

// hashAPIKey creates a SHA-256 hash of the API key.
func hashAPIKey(key string) string {
	hash := sha256.Sum256([]byte(key))
	return hex.EncodeToString(hash[:])
}

// extractPrefix extracts the prefix from an API key for identification.
func extractPrefix(key string) string {
	if len(key) < 12 {
		return key
	}
	// Return "nxk_" + first 8 chars of the random part
	return key[:12]
}

// apiKeyColumns lists every hatamoto_api_keys column scanned into an APIKey.
// Centralised so the SELECT/RETURNING strings stay in sync. tenant_id
// was dropped by Path B migration 079.
//
// Nullable columns (description, scopes, rate_limit, last_used_ip,
// request_count, is_active) are COALESCEd so rows with NULLs scan into the
// non-pointer APIKey fields. NULL scopes collapse to none (fail closed).
const apiKeyColumns = `id, name, COALESCE(description, ''), key_prefix, key_hash,
		COALESCE(scopes, '[]'::jsonb), COALESCE(rate_limit, 1000), expires_at, last_used_at, COALESCE(last_used_ip, ''),
		COALESCE(request_count, 0), COALESCE(is_active, false), created_by, created_at, updated_at,
		revoked_at, revoked_by`

// DefaultRateLimit is the hourly request limit applied when none (or a
// non-positive one) is configured.
const DefaultRateLimit = 1000

// NormalizeRateLimit maps non-positive limits to DefaultRateLimit.
func NormalizeRateLimit(n int) int {
	if n <= 0 {
		return DefaultRateLimit
	}
	return n
}

// scanAPIKey fills an APIKey from a row matching apiKeyColumns order.
func scanAPIKey(row interface {
	Scan(...any) error
}, key *APIKey) error {
	return row.Scan(
		&key.ID, &key.Name, &key.Description,
		&key.KeyPrefix, &key.KeyHash, &key.Scopes, &key.RateLimit,
		&key.ExpiresAt, &key.LastUsedAt, &key.LastUsedIP,
		&key.RequestCount, &key.IsActive, &key.CreatedBy,
		&key.CreatedAt, &key.UpdatedAt, &key.RevokedAt, &key.RevokedBy,
	)
}

// Create generates a new API key.
func (s *APIKeyService) Create(ctx context.Context, createdBy *uuid.UUID, input *CreateAPIKeyInput) (*APIKeyWithSecret, error) {
	// Validate scopes
	if len(input.Scopes) == 0 {
		input.Scopes = []APIKeyScope{ScopeReadEntities}
	}
	for _, scope := range input.Scopes {
		if !IsValidScope(string(scope)) {
			return nil, fmt.Errorf("%w: %s", ErrInvalidScope, scope)
		}
	}

	// Set default rate limit
	input.RateLimit = NormalizeRateLimit(input.RateLimit)

	// Generate key
	plainKey, err := generateAPIKey()
	if err != nil {
		return nil, err
	}

	keyHash := hashAPIKey(plainKey)
	keyPrefix := extractPrefix(plainKey)

	conn, err := s.db.GetConn(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get connection: %w", err)
	}
	defer conn.Release()

	var apiKey APIKey
	query := `
		INSERT INTO hatamoto_api_keys (
			name, description, key_prefix, key_hash,
			scopes, rate_limit, expires_at, created_by
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING ` + apiKeyColumns

	err = scanAPIKey(conn.QueryRow(ctx, query,
		input.Name, input.Description, keyPrefix, keyHash,
		input.Scopes, input.RateLimit, input.ExpiresAt, createdBy,
	), &apiKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create api key: %w", err)
	}

	s.logger.Info("created api key",
		slog.String("api_key_id", apiKey.ID.String()),
		slog.String("name", input.Name),
	)

	return &APIKeyWithSecret{
		APIKey:   apiKey,
		PlainKey: plainKey,
	}, nil
}

// GetByID retrieves an API key by ID.
func (s *APIKeyService) GetByID(ctx context.Context, id uuid.UUID) (*APIKey, error) {
	conn, err := s.db.GetConn(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get connection: %w", err)
	}
	defer conn.Release()

	var apiKey APIKey
	query := `SELECT ` + apiKeyColumns + ` FROM hatamoto_api_keys WHERE id = $1`

	if err := scanAPIKey(conn.QueryRow(ctx, query, id), &apiKey); err != nil {
		return nil, ErrAPIKeyNotFound
	}

	return &apiKey, nil
}

// List retrieves all API keys.
func (s *APIKeyService) List(ctx context.Context) ([]*APIKey, error) {
	conn, err := s.db.GetConn(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get connection: %w", err)
	}
	defer conn.Release()

	query := `SELECT ` + apiKeyColumns + ` FROM hatamoto_api_keys ORDER BY created_at DESC`

	rows, err := conn.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query api keys: %w", err)
	}
	defer rows.Close()

	var keys []*APIKey
	for rows.Next() {
		var apiKey APIKey
		if err := scanAPIKey(rows, &apiKey); err != nil {
			return nil, fmt.Errorf("failed to scan api key: %w", err)
		}
		keys = append(keys, &apiKey)
	}

	return keys, rows.Err()
}

// Update modifies an existing API key.
func (s *APIKeyService) Update(ctx context.Context, id uuid.UUID, input *UpdateAPIKeyInput) (*APIKey, error) {
	if input.RateLimit != nil && *input.RateLimit <= 0 {
		return nil, ErrInvalidRateLimit
	}

	// Validate scopes if provided
	if len(input.Scopes) > 0 {
		for _, scope := range input.Scopes {
			if !IsValidScope(string(scope)) {
				return nil, fmt.Errorf("%w: %s", ErrInvalidScope, scope)
			}
		}
	}

	conn, err := s.db.GetConn(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get connection: %w", err)
	}
	defer conn.Release()

	// Build dynamic update query
	var setParts []string
	var args []any
	argIdx := 1

	if input.Name != nil {
		setParts = append(setParts, fmt.Sprintf("name = $%d", argIdx))
		args = append(args, *input.Name)
		argIdx++
	}
	if input.Description != nil {
		setParts = append(setParts, fmt.Sprintf("description = $%d", argIdx))
		args = append(args, *input.Description)
		argIdx++
	}
	if len(input.Scopes) > 0 {
		setParts = append(setParts, fmt.Sprintf("scopes = $%d", argIdx))
		args = append(args, input.Scopes)
		argIdx++
	}
	if input.RateLimit != nil {
		setParts = append(setParts, fmt.Sprintf("rate_limit = $%d", argIdx))
		args = append(args, *input.RateLimit)
		argIdx++
	}
	if input.IsActive != nil {
		setParts = append(setParts, fmt.Sprintf("is_active = $%d", argIdx))
		args = append(args, *input.IsActive)
		argIdx++
	}

	if len(setParts) == 0 {
		return s.GetByID(ctx, id)
	}

	args = append(args, id)
	query := fmt.Sprintf(`
		UPDATE hatamoto_api_keys
		SET %s, updated_at = NOW()
		WHERE id = $%d
		RETURNING `+apiKeyColumns, strings.Join(setParts, ", "), argIdx)

	var apiKey APIKey
	if err := scanAPIKey(conn.QueryRow(ctx, query, args...), &apiKey); err != nil {
		return nil, fmt.Errorf("failed to update api key: %w", err)
	}

	s.logger.Info("updated api key",
		slog.String("api_key_id", apiKey.ID.String()),
	)

	return &apiKey, nil
}

// Revoke marks an API key as revoked.
func (s *APIKeyService) Revoke(ctx context.Context, id uuid.UUID, revokedBy *uuid.UUID) error {
	conn, err := s.db.GetConn(ctx)
	if err != nil {
		return fmt.Errorf("failed to get connection: %w", err)
	}
	defer conn.Release()

	query := `
		UPDATE hatamoto_api_keys
		SET is_active = FALSE, revoked_at = NOW(), revoked_by = $2, updated_at = NOW()
		WHERE id = $1
	`

	result, err := conn.Exec(ctx, query, id, revokedBy)
	if err != nil {
		return fmt.Errorf("failed to revoke api key: %w", err)
	}

	if result.RowsAffected() == 0 {
		return ErrAPIKeyNotFound
	}

	s.logger.Info("revoked api key",
		slog.String("api_key_id", id.String()),
	)

	return nil
}

// Delete permanently removes an API key.
func (s *APIKeyService) Delete(ctx context.Context, id uuid.UUID) error {
	conn, err := s.db.GetConn(ctx)
	if err != nil {
		return fmt.Errorf("failed to get connection: %w", err)
	}
	defer conn.Release()

	query := `DELETE FROM hatamoto_api_keys WHERE id = $1`
	result, err := conn.Exec(ctx, query, id)
	if err != nil {
		return fmt.Errorf("failed to delete api key: %w", err)
	}

	if result.RowsAffected() == 0 {
		return ErrAPIKeyNotFound
	}

	s.logger.Info("deleted api key",
		slog.String("api_key_id", id.String()),
	)

	return nil
}

// ValidateKey validates an API key and returns the associated key record.
// This is the main authentication method for API key auth.
func (s *APIKeyService) ValidateKey(ctx context.Context, plainKey string) (*APIKey, error) {
	if !strings.HasPrefix(plainKey, "nxk_") {
		return nil, ErrAPIKeyInvalidHash
	}

	keyHash := hashAPIKey(plainKey)

	conn, err := s.db.GetConn(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get connection: %w", err)
	}
	defer conn.Release()

	var apiKey APIKey
	// The creator's current role rides in the same row ('' when the user is
	// gone or created_by is NULL), so there is no second round-trip.
	query := `SELECT ` + apiKeyColumns + `,
		COALESCE((SELECT u.role FROM hatamoto_users u WHERE u.id = hatamoto_api_keys.created_by), '')
		FROM hatamoto_api_keys WHERE key_hash = $1`

	if err := scanAPIKey(withExtraDest{conn.QueryRow(ctx, query, keyHash), &apiKey.CreatorRole}, &apiKey); err != nil {
		return nil, ErrAPIKeyNotFound
	}

	// Check if revoked
	if apiKey.RevokedAt != nil {
		return nil, ErrAPIKeyRevoked
	}

	// Check if active
	if !apiKey.IsActive {
		return nil, ErrAPIKeyRevoked
	}

	// Check expiration
	if apiKey.ExpiresAt != nil && apiKey.ExpiresAt.Before(time.Now()) {
		return nil, ErrAPIKeyExpired
	}

	// The creator must still be an owner/editor: demotion or deletion revokes
	// the key's effective power without touching the key.
	if !apiKey.CreatorCanAct() {
		return nil, ErrAPIKeyCreatorGone
	}

	return &apiKey, nil
}

// withExtraDest appends one more scan destination to a row's Scan.
type withExtraDest struct {
	row interface {
		Scan(...any) error
	}
	extra any
}

func (w withExtraDest) Scan(dest ...any) error {
	return w.row.Scan(append(dest, w.extra)...)
}

// RecordUsage updates the last used timestamp and increments the request count.
func (s *APIKeyService) RecordUsage(ctx context.Context, keyID uuid.UUID, ipAddress string) error {
	conn, err := s.db.GetConn(ctx)
	if err != nil {
		return fmt.Errorf("failed to get connection: %w", err)
	}
	defer conn.Release()

	query := `
		UPDATE hatamoto_api_keys
		SET last_used_at = NOW(), last_used_ip = $2, request_count = request_count + 1
		WHERE id = $1
	`

	_, err = conn.Exec(ctx, query, keyID, ipAddress)
	return err
}

// HasScope checks if an API key has a specific scope.
func (k *APIKey) HasScope(scope APIKeyScope) bool {
	for _, s := range k.Scopes {
		if s == ScopeAll || s == scope {
			return true
		}
		// Check read permission for write scope
		if strings.HasPrefix(string(scope), "read:") {
			writeScope := strings.Replace(string(scope), "read:", "write:", 1)
			if string(s) == writeScope {
				return true
			}
		}
	}
	return false
}

// CanRead checks if the key has read access to a resource type.
func (k *APIKey) CanRead(resource string) bool {
	return k.HasScope(APIKeyScope("read:"+resource)) || k.HasScope(APIKeyScope("write:"+resource))
}

// CanWrite checks if the key has write access to a resource type.
func (k *APIKey) CanWrite(resource string) bool {
	return k.HasScope(APIKeyScope("write:" + resource))
}

// Regenerate creates a new API key while keeping the same record.
// Returns the new plain key (shown once).
func (s *APIKeyService) Regenerate(ctx context.Context, id uuid.UUID) (string, error) {
	plainKey, err := generateAPIKey()
	if err != nil {
		return "", err
	}

	keyHash := hashAPIKey(plainKey)
	keyPrefix := extractPrefix(plainKey)

	conn, err := s.db.GetConn(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get connection: %w", err)
	}
	defer conn.Release()

	query := `
		UPDATE hatamoto_api_keys
		SET key_hash = $2, key_prefix = $3, updated_at = NOW()
		WHERE id = $1
	`

	result, err := conn.Exec(ctx, query, id, keyHash, keyPrefix)
	if err != nil {
		return "", fmt.Errorf("failed to regenerate api key: %w", err)
	}

	if result.RowsAffected() == 0 {
		return "", ErrAPIKeyNotFound
	}

	s.logger.Info("regenerated api key",
		slog.String("api_key_id", id.String()),
	)

	return plainKey, nil
}
