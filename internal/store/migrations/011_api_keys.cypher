// API keys created from Config. Only the SHA-256 hash and a display prefix are stored; the secret is
// shown once when it is created. Revoked keys stay in the graph so the audit trail keeps the name.
CREATE CONSTRAINT api_key_id IF NOT EXISTS FOR (n:ApiKey) REQUIRE n.id IS UNIQUE;
CREATE CONSTRAINT api_key_hash IF NOT EXISTS FOR (n:ApiKey) REQUIRE n.hash IS UNIQUE;
CREATE CONSTRAINT api_key_name IF NOT EXISTS FOR (n:ApiKey) REQUIRE n.name IS UNIQUE;
