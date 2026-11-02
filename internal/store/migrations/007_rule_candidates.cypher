// Rule candidates: AI proposals that are not used at runtime until proven and promoted.
CREATE CONSTRAINT rule_candidate_id IF NOT EXISTS FOR (n:RuleCandidate) REQUIRE n.id IS UNIQUE;
CREATE CONSTRAINT rule_candidate_unique IF NOT EXISTS FOR (n:RuleCandidate) REQUIRE n.uniqueKey IS UNIQUE;
CREATE INDEX rule_candidate_status IF NOT EXISTS FOR (n:RuleCandidate) ON (n.status);
CREATE INDEX rule_candidate_fingerprint IF NOT EXISTS FOR (n:RuleCandidate) ON (n.fingerprint);
