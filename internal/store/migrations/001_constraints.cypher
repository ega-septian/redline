// Skema awal Redline di Neo4j: constraint keunikan dan index untuk node utama.
//
// Graph-nya (diisi bertahap di branch feature/neo4j):
//   (:Run)-[:HAS]->(:Result)-[:OF]->(:Test {key, caseId})
//   (:Test)-[:USES]->(:File)           (:Test)-[:CALLS]->(:Endpoint)
//   (:Failure)-[:OF]->(:Test)          (:Failure)-[:SEEN_IN]->(:Run)
//   (:Failure)-[:ANALYZED]->(:Analysis) (:Failure)-[:PROVEN_BY]->(:Experiment)
//   (:Run)-[:UNDER]->(:Contract)       (:Result)-[:RETURNED]->(:Shape)
// Nomor berurutan (run, result, experiment, rule) dibuat dari node :Counter, pengganti BIGSERIAL.

CREATE CONSTRAINT run_id IF NOT EXISTS FOR (n:Run) REQUIRE n.id IS UNIQUE;
CREATE CONSTRAINT result_id IF NOT EXISTS FOR (n:Result) REQUIRE n.id IS UNIQUE;
CREATE CONSTRAINT test_key IF NOT EXISTS FOR (n:Test) REQUIRE n.key IS UNIQUE;
CREATE CONSTRAINT failure_id IF NOT EXISTS FOR (n:Failure) REQUIRE n.id IS UNIQUE;
CREATE CONSTRAINT shape_id IF NOT EXISTS FOR (n:Shape) REQUIRE n.id IS UNIQUE;
CREATE CONSTRAINT source_file_id IF NOT EXISTS FOR (n:SourceFile) REQUIRE n.id IS UNIQUE;
CREATE CONSTRAINT contract_id IF NOT EXISTS FOR (n:Contract) REQUIRE n.id IS UNIQUE;
CREATE CONSTRAINT experiment_id IF NOT EXISTS FOR (n:Experiment) REQUIRE n.id IS UNIQUE;
CREATE CONSTRAINT rule_id IF NOT EXISTS FOR (n:Rule) REQUIRE n.id IS UNIQUE;
CREATE CONSTRAINT counter_name IF NOT EXISTS FOR (n:Counter) REQUIRE n.name IS UNIQUE;
CREATE CONSTRAINT schema_migration_version IF NOT EXISTS FOR (n:SchemaMigration) REQUIRE n.version IS UNIQUE;

CREATE INDEX failure_status IF NOT EXISTS FOR (n:Failure) ON (n.status);
CREATE INDEX test_case_id IF NOT EXISTS FOR (n:Test) ON (n.caseId);
CREATE INDEX run_created_at IF NOT EXISTS FOR (n:Run) ON (n.createdAt);
