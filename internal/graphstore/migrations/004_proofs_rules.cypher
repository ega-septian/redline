// Eksperimen, tebakan untuk rapor, dan aturan yang dipelajari.
//   (:Failure)-[:TESTED_BY]->(:Experiment)    (:Failure)-[:PREDICTED]->(:Prediction)    (:Rule)
// Keunikan aturan (pola + kategori) disimpan sebagai satu properti uniqueKey, karena constraint
// gabungan beberapa properti tidak tersedia di semua edisi Neo4j.
CREATE CONSTRAINT prediction_id IF NOT EXISTS FOR (n:Prediction) REQUIRE n.id IS UNIQUE;
CREATE CONSTRAINT rule_unique_key IF NOT EXISTS FOR (n:Rule) REQUIRE n.uniqueKey IS UNIQUE;
CREATE INDEX experiment_group_id IF NOT EXISTS FOR (n:Experiment) ON (n.groupId);
CREATE INDEX prediction_group_id IF NOT EXISTS FOR (n:Prediction) ON (n.groupId);
