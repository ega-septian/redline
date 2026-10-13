// Graph import untuk KAG.
//   (:File)-[:IMPORTS]->(:File)                       graph import terbaru (diganti setiap run melaporkan file itu)
//   (:File)-[:VERSION]->(:FileVersion {path, hash})   satu node per isi file
//   (:Run)-[:HAD]->(:FileVersion)                     isi file saat run itu
//   (:Result)-[:DEPENDS_ON]->(:File)                  semua file yang dipakai test, termasuk import bertingkat
CREATE CONSTRAINT file_version_id IF NOT EXISTS FOR (n:FileVersion) REQUIRE n.id IS UNIQUE;
