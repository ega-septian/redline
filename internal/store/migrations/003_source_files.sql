-- Isi kode test untuk test yang GAGAL (file spec + file lokal yang di-import), supaya AI bisa membaca
-- kodenya, bukan hanya potongan beberapa baris di sekitar error. Tanpa kode, AI sering tidak bisa
-- membedakan salah test dari salah API (misalnya payload test yang kurang field).
-- Disimpan sekali per isi file, sudah disamarkan (token, password).
CREATE TABLE source_files (
    id      TEXT PRIMARY KEY,                               -- md5(path + isi)
    path    TEXT NOT NULL,                                  -- relatif terhadap project
    content TEXT NOT NULL
);

-- File yang dipakai test pada kemunculan ini (urutan: file spec dulu). Kosong untuk test yang lulus.
ALTER TABLE test_results ADD COLUMN source_ids TEXT[] NOT NULL DEFAULT '{}';
