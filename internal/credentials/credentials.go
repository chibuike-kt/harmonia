package credentials

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/chibuike-kt/harmonia/internal/agent"
	"github.com/chibuike-kt/harmonia/internal/provider"
	"github.com/chibuike-kt/harmonia/internal/provider/anthropic"
	"github.com/chibuike-kt/harmonia/internal/provider/openai"
)

// ErrEncryptionNotConfigured is returned when Connect is called without
// a Cipher — the deployment hasn't set HARMONIA_CREDENTIAL_ENCRYPTION_KEY.
var ErrEncryptionNotConfigured = errors.New("credentials: encryption is not configured")

// ErrVerificationFailed wraps the provider's own rejection of a key —
// distinguished from a server-side failure so the handler can 400
// instead of 500.
var ErrVerificationFailed = errors.New("credentials: provider verification failed")

// ErrNoCredential is returned by Resolve when the room's owner exists
// but has never connected a credential for the requested provider — not
// a bad key, just none stored. This is also what a nil ownerID resolves
// to: a pre-Phase-2 room with no owner can't have a BYOK credential
// either, so it collapses into the same case rather than a separate one.
// Distinguished from a generic failure so a caller can respond with
// something a human can act on. Not currently wired to any HTTP
// endpoint — Milestone 1/Phase 2 never added a server-side call site
// that resolves a provider client for a live request (that's Phase 4's
// tool execution) — but when one exists, it should map this to 424
// Failed Dependency ("the request itself was fine; a required upstream
// dependency — a connected provider credential — is missing"), not a
// generic 500, with a message telling the caller to connect one via
// POST /v1/credentials.
var ErrNoCredential = errors.New("credentials: no credential connected for this provider")

// verificationTimeout bounds the live provider call Connect makes before
// storing a key — long enough for a real round trip, short enough that
// a slow or hung provider doesn't hang the request indefinitely.
const verificationTimeout = 20 * time.Second

// verificationPrompt is deliberately tiny. This call exists to prove the
// key is live, not to generate anything useful — "cheap", per ADR-002.
const verificationPrompt = "Reply with OK."

// keyHintLen is how much of the plaintext key is ever shown back — the
// same "last four characters" convention ADR-002 specifies.
const keyHintLen = 4

// Credential is what the API ever exposes about a connected provider:
// never the plaintext key, never the encrypted blob or nonce.
type Credential struct {
	ID         uuid.UUID  `json:"id"`
	Provider   string     `json:"provider"`
	KeyHint    string     `json:"key_hint"`
	VerifiedAt *time.Time `json:"verified_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
}

// newProviderClientFunc builds the provider.Agent Connect uses to verify
// a key live. A field on Store rather than a package-level function so
// tests can substitute a fake and never touch the real anthropic/openai
// clients or the network — the same seam internal/provider's Agent
// interface exists for.
type newProviderClientFunc func(providerName agent.Provider, apiKey string) (provider.Agent, error)

func defaultProviderClient(providerName agent.Provider, apiKey string) (provider.Agent, error) {
	switch providerName {
	case agent.ProviderAnthropic:
		return anthropic.New(apiKey), nil
	case agent.ProviderOpenAI:
		return openai.New(apiKey), nil
	default:
		return nil, fmt.Errorf("credentials: unsupported provider %q", providerName)
	}
}

type Store struct {
	pool              *pgxpool.Pool
	cipher            *Cipher
	newProviderClient newProviderClientFunc
}

// NewStore builds a Store. cipher may be nil — a deployment that hasn't
// configured HARMONIA_CREDENTIAL_ENCRYPTION_KEY yet — in which case
// Connect fails cleanly with ErrEncryptionNotConfigured rather than
// panicking; List and Delete don't need it and work regardless.
func NewStore(pool *pgxpool.Pool, cipher *Cipher) *Store {
	return &Store{pool: pool, cipher: cipher, newProviderClient: defaultProviderClient}
}

// Connect verifies apiKey with a live call to providerName's API, then
// encrypts and upserts it for userID. A failed verification leaves the
// existing credential (if any) untouched and returns ErrVerificationFailed
// — a bad key never overwrites a working one, and never gets stored at
// all. Reconnecting the same provider replaces its credential in place
// (same row, same id) per the table's UNIQUE(user_id, provider).
func (s *Store) Connect(ctx context.Context, userID uuid.UUID, providerName agent.Provider, apiKey string) (Credential, error) {
	if s.cipher == nil {
		return Credential{}, ErrEncryptionNotConfigured
	}

	client, err := s.newProviderClient(providerName, apiKey)
	if err != nil {
		return Credential{}, err
	}

	verifyCtx, cancel := context.WithTimeout(ctx, verificationTimeout)
	defer cancel()
	if _, err := client.Generate(verifyCtx, provider.GenerateRequest{
		Messages: []provider.Message{{Role: "user", Content: verificationPrompt}},
	}); err != nil {
		return Credential{}, fmt.Errorf("%w: %v", ErrVerificationFailed, err)
	}

	ciphertext, nonce, err := s.cipher.Encrypt(apiKey)
	if err != nil {
		return Credential{}, fmt.Errorf("credentials: encrypt key: %w", err)
	}

	var cred Credential
	err = s.pool.QueryRow(ctx, `
		INSERT INTO provider_credentials (user_id, provider, encrypted_key, nonce, key_hint, verified_at)
		VALUES ($1, $2, $3, $4, $5, now())
		ON CONFLICT (user_id, provider) DO UPDATE
		SET encrypted_key = EXCLUDED.encrypted_key, nonce = EXCLUDED.nonce,
			key_hint = EXCLUDED.key_hint, verified_at = EXCLUDED.verified_at
		RETURNING id, provider, key_hint, verified_at, created_at
	`, userID, string(providerName), ciphertext, nonce, keyHint(apiKey)).Scan(
		&cred.ID, &cred.Provider, &cred.KeyHint, &cred.VerifiedAt, &cred.CreatedAt,
	)
	if err != nil {
		return Credential{}, err
	}
	return cred, nil
}

// Resolve returns a working provider.Agent for providerName, built from
// ownerID's decrypted BYOK credential. This is pure BYOK resolution —
// it has no idea an env var fallback exists; that's a deployment-time
// concern for whoever calls Resolve, not this package's job (see
// ADR-002: the platform is never supposed to fall back to a shared key
// as its real credential path, only as an explicit, clearly-marked
// dev/CI convenience at the call site).
//
// ownerID is a *uuid.UUID because room.Room.OwnerID is one — nil means
// a pre-Phase-2 room with no owner, which can't have a BYOK credential
// any more than an owner who simply never connected one, so both cases
// return ErrNoCredential. Returns ErrEncryptionNotConfigured if this
// deployment hasn't set HARMONIA_CREDENTIAL_ENCRYPTION_KEY.
func (s *Store) Resolve(ctx context.Context, ownerID *uuid.UUID, providerName agent.Provider) (provider.Agent, error) {
	if ownerID == nil {
		return nil, ErrNoCredential
	}
	if s.cipher == nil {
		return nil, ErrEncryptionNotConfigured
	}

	var encryptedKey, nonce []byte
	err := s.pool.QueryRow(ctx, `
		SELECT encrypted_key, nonce FROM provider_credentials WHERE user_id = $1 AND provider = $2
	`, *ownerID, string(providerName)).Scan(&encryptedKey, &nonce)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNoCredential
	}
	if err != nil {
		return nil, err
	}

	apiKey, err := s.cipher.Decrypt(encryptedKey, nonce)
	if err != nil {
		return nil, fmt.Errorf("credentials: decrypt stored key: %w", err)
	}

	return s.newProviderClient(providerName, apiKey)
}

// List returns userID's connected providers, most recent first — hints
// only, never the encrypted blob.
func (s *Store) List(ctx context.Context, userID uuid.UUID) ([]Credential, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, provider, key_hint, verified_at, created_at
		FROM provider_credentials WHERE user_id = $1 ORDER BY created_at DESC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var creds []Credential
	for rows.Next() {
		var c Credential
		if err := rows.Scan(&c.ID, &c.Provider, &c.KeyHint, &c.VerifiedAt, &c.CreatedAt); err != nil {
			return nil, err
		}
		creds = append(creds, c)
	}
	return creds, rows.Err()
}

// Delete removes userID's credential for providerName, if any. Reports
// whether a row actually existed, so the caller can 404 on "nothing to
// delete" without a separate lookup.
func (s *Store) Delete(ctx context.Context, userID uuid.UUID, providerName agent.Provider) (bool, error) {
	tag, err := s.pool.Exec(ctx, `
		DELETE FROM provider_credentials WHERE user_id = $1 AND provider = $2
	`, userID, string(providerName))
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// keyHint returns the last few characters of key — enough for a user to
// recognize which key they connected, never enough to reconstruct it.
func keyHint(key string) string {
	if len(key) <= keyHintLen {
		return key
	}
	return key[len(key)-keyHintLen:]
}
