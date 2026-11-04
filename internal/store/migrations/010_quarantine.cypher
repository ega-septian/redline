// Quarantine is keyed by @TC- ID when present, so a rename keeps the quarantine.
CREATE CONSTRAINT quarantine_key IF NOT EXISTS FOR (n:Quarantine) REQUIRE n.key IS UNIQUE;
CREATE INDEX quarantine_until IF NOT EXISTS FOR (n:Quarantine) ON (n.until);
