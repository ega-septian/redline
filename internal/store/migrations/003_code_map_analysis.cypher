// Peta kode sebagai graph, dan hasil analisis.
//   (:Result)-[:CALLED]->(:Endpoint {key: "GET /brands"})
//   (:Result)-[:USED]->(:File {path: "tests/api/toolshop/schemas/brand.schema.ts"})
//   (:Failure)-[:ANALYZED]->(:Analysis {id: "<groupId>|<promptVersion>"})
CREATE CONSTRAINT endpoint_key IF NOT EXISTS FOR (n:Endpoint) REQUIRE n.key IS UNIQUE;
CREATE CONSTRAINT file_path IF NOT EXISTS FOR (n:File) REQUIRE n.path IS UNIQUE;
CREATE CONSTRAINT analysis_id IF NOT EXISTS FOR (n:Analysis) REQUIRE n.id IS UNIQUE;
