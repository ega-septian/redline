package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/neo4j/neo4j-go-driver/v5/neo4j"
)

// ErrAdminExists is returned when first-run registration runs after an account already exists.
var ErrAdminExists = errors.New("admin account already exists")

// User is a UI account. Passwords are stored as bcrypt hashes; API keys are not stored here.
type User struct {
	Username     string    `json:"username"`
	PasswordHash string    `json:"-"`
	Role         string    `json:"role"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// UserCount returns the number of UI accounts.
func (s *Store) UserCount(ctx context.Context) (int, error) {
	res, err := s.query(ctx, `MATCH (u:User) RETURN count(u) AS n`, nil)
	if err != nil {
		return 0, err
	}
	if len(res.Records) == 0 {
		return 0, nil
	}
	return recInt(res.Records[0], "n"), nil
}

// CreateAdmin creates the first UI account. Passwords must already be hashed.
// The lock node serialises concurrent registrations, so only one admin can be created.
func (s *Store) CreateAdmin(ctx context.Context, username, passwordHash string) (User, error) {
	res, err := s.query(ctx, `
		MERGE (lock:ServerSetting {id: 'users'})
		ON CREATE SET lock.createdAt = datetime()
		SET lock.updatedAt = datetime()
		WITH lock
		OPTIONAL MATCH (u:User)
		WITH lock, count(u) AS n
		WHERE n = 0
		CREATE (u:User {username: $username, passwordHash: $hash, role: 'admin',
			createdAt: datetime(), updatedAt: datetime()})
		RETURN u`,
		map[string]any{"username": username, "hash": passwordHash})
	if err != nil {
		return User{}, err
	}
	if len(res.Records) == 0 {
		return User{}, ErrAdminExists
	}
	node, _ := res.Records[0].Get("u")
	return toUser(node.(neo4j.Node).Props), nil
}

// User returns one UI account, or ErrNotFound.
func (s *Store) User(ctx context.Context, username string) (User, error) {
	res, err := s.query(ctx, `MATCH (u:User {username: $username}) RETURN u`,
		map[string]any{"username": username})
	if err != nil {
		return User{}, err
	}
	if len(res.Records) == 0 {
		return User{}, ErrNotFound
	}
	node, _ := res.Records[0].Get("u")
	return toUser(node.(neo4j.Node).Props), nil
}

// UpdatePassword replaces the bcrypt hash for one user.
func (s *Store) UpdatePassword(ctx context.Context, username, passwordHash string) error {
	res, err := s.query(ctx, `
		MATCH (u:User {username: $username})
		SET u.passwordHash = $hash, u.updatedAt = datetime()
		RETURN count(u) AS n`,
		map[string]any{"username": username, "hash": passwordHash})
	if err != nil {
		return err
	}
	if len(res.Records) == 0 || recInt64(res.Records[0], "n") == 0 {
		return ErrNotFound
	}
	return nil
}

// AuthSecret returns the stable session-signing secret, creating it on first use.
func (s *Store) AuthSecret(ctx context.Context) (string, error) {
	res, err := s.query(ctx, `
		MERGE (s:ServerSetting {id: 'auth'})
		ON CREATE SET s.sessionSecret = randomUUID() + randomUUID()
		WITH s
		FOREACH (_ IN CASE WHEN s.sessionSecret IS NULL OR s.sessionSecret = '' THEN [1] ELSE [] END |
			SET s.sessionSecret = randomUUID() + randomUUID())
		RETURN s.sessionSecret AS secret`, nil)
	if err != nil {
		return "", err
	}
	if len(res.Records) == 0 {
		return "", fmt.Errorf("session secret is not available")
	}
	return recString(res.Records[0], "secret"), nil
}

func toUser(p map[string]any) User {
	return User{
		Username: str(p, "username"), PasswordHash: str(p, "passwordHash"),
		Role: str(p, "role"), CreatedAt: local(p["createdAt"]), UpdatedAt: local(p["updatedAt"]),
	}
}
