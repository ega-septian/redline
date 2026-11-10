package store

import (
	"context"
	"errors"
	"time"

	"github.com/neo4j/neo4j-go-driver/v5/neo4j"
)

// ErrAPIKeyExists is returned when a key name is already used. Revoked keys keep their name.
var ErrAPIKeyExists = errors.New("api key name already exists")

// APIKey is a machine credential created from Config. Hash is never returned to clients.
type APIKey struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Prefix     string     `json:"prefix"`
	Hash       string     `json:"-"`
	CreatedBy  string     `json:"created_by,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
}

// CreateAPIKey stores the hash and display prefix of a new key. The caller generates the secret;
// the hash is the only copy Redline keeps.
func (s *Store) CreateAPIKey(ctx context.Context, key APIKey) (APIKey, error) {
	res, err := s.query(ctx, `
		MERGE (lock:ServerSetting {id: 'api-keys'})
		ON CREATE SET lock.createdAt = datetime()
		SET lock.updatedAt = datetime()
		WITH lock
		OPTIONAL MATCH (existing:APIKey {name: $name})
		WITH lock, count(existing) AS n
		WHERE n = 0
		CREATE (k:APIKey {id: randomUUID(), name: $name, prefix: $prefix, hash: $hash,
			createdBy: $createdBy, createdAt: datetime()})
		RETURN k`,
		map[string]any{
			"name": key.Name, "prefix": key.Prefix, "hash": key.Hash, "createdBy": key.CreatedBy,
		})
	if err != nil {
		var neoErr *neo4j.Neo4jError
		if errors.As(err, &neoErr) && neoErr.Code == "Neo.ClientError.Schema.ConstraintValidationFailed" {
			return APIKey{}, ErrAPIKeyExists
		}
		return APIKey{}, err
	}
	if len(res.Records) == 0 {
		return APIKey{}, ErrAPIKeyExists
	}
	node, _ := res.Records[0].Get("k")
	return toAPIKey(node.(neo4j.Node).Props), nil
}

// ListAPIKeys returns active and revoked keys, newest first. The hash is not included in the struct's
// JSON shape, so it cannot leak through the API.
func (s *Store) ListAPIKeys(ctx context.Context) ([]APIKey, error) {
	res, err := s.query(ctx, `MATCH (k:APIKey) RETURN k ORDER BY k.createdAt DESC`, nil)
	if err != nil {
		return nil, err
	}
	out := make([]APIKey, 0, len(res.Records))
	for _, record := range res.Records {
		node, _ := record.Get("k")
		out = append(out, toAPIKey(node.(neo4j.Node).Props))
	}
	return out, nil
}

// RevokeAPIKey marks a key as revoked. Revoking twice keeps the first timestamp.
func (s *Store) RevokeAPIKey(ctx context.Context, id string) (APIKey, error) {
	res, err := s.query(ctx, `
		MATCH (k:APIKey {id: $id})
		SET k.revokedAt = CASE WHEN k.revokedAt IS NULL THEN datetime() ELSE k.revokedAt END
		RETURN k`,
		map[string]any{"id": id})
	if err != nil {
		return APIKey{}, err
	}
	if len(res.Records) == 0 {
		return APIKey{}, ErrNotFound
	}
	node, _ := res.Records[0].Get("k")
	return toAPIKey(node.(neo4j.Node).Props), nil
}

// APIKeyName validates a key hash and records its last use. Revoked keys are rejected.
func (s *Store) APIKeyName(ctx context.Context, hash string) (string, error) {
	res, err := s.query(ctx, `
		MATCH (k:APIKey {hash: $hash})
		WHERE k.revokedAt IS NULL
		SET k.lastUsedAt = datetime()
		RETURN k.name AS name`,
		map[string]any{"hash": hash})
	if err != nil {
		return "", err
	}
	if len(res.Records) == 0 {
		return "", ErrNotFound
	}
	return recString(res.Records[0], "name"), nil
}

// APIKeyCount counts every key, including revoked ones. Once any key has existed, the API stays
// protected after a restart even if every key is revoked.
func (s *Store) APIKeyCount(ctx context.Context) (int, error) {
	res, err := s.query(ctx, `MATCH (k:APIKey) RETURN count(k) AS n`, nil)
	if err != nil {
		return 0, err
	}
	if len(res.Records) == 0 {
		return 0, nil
	}
	return recInt(res.Records[0], "n"), nil
}

func toAPIKey(p map[string]any) APIKey {
	return APIKey{
		ID: str(p, "id"), Name: str(p, "name"), Prefix: str(p, "prefix"), Hash: str(p, "hash"),
		CreatedBy: str(p, "createdBy"), CreatedAt: local(p["createdAt"]),
		LastUsedAt: localPtr(p["lastUsedAt"]), RevokedAt: localPtr(p["revokedAt"]),
	}
}

func localPtr(v any) *time.Time {
	if v == nil {
		return nil
	}
	t := local(v)
	if t.IsZero() {
		return nil
	}
	return &t
}
