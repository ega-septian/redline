// Uploaded trace/screenshot metadata.
CREATE CONSTRAINT artifact_id IF NOT EXISTS FOR (n:Artifact) REQUIRE n.id IS UNIQUE;
CREATE INDEX artifact_run IF NOT EXISTS FOR (n:Artifact) ON (n.runId);
