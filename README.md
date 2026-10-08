# Redline

Redline membaca hasil test Playwright, mengelompokkan kegagalan yang sama, dan melacak
statusnya dari run ke run. Tujuan akhirnya menjawab: **"kenapa test ini gagal?"**

Isinya:

- Menerima `results.json` dari reporter JSON Playwright.
- Meredaksi token, password, JWT, dan angka 16 digit (NIK/kartu) **sebelum** disimpan.
- Mengelompokkan kegagalan: test yang sama dengan error yang sama masuk satu **kelompok
  kegagalan**. ID kelompok adalah hash (kode pendek yang selalu sama untuk teks yang sama) dari
  nama test + pesan error yang dinormalisasi (UUID, ULID, waktu, email, dan id panjang diabaikan;
  kode status seperti 404 vs 500 tetap dibedakan).
- Melacak status tiap kelompok kegagalan:

```
gagal pertama kali  → open
lulus bersih        → resolved
gagal lagi (sama)   → regressed
```

Test flaky (gagal lalu lulus saat retry) dicatat terpisah dan tidak membuka kelompok.

- Menggabungkan kegagalan banyak test yang penyebabnya sama menjadi satu **insiden**, supaya
  server mati atau satu endpoint rusak tidak muncul sebagai ratusan baris terpisah:

| Jenis        | Kapan                                         | Contoh label                                   |
|--------------|-----------------------------------------------|------------------------------------------------|
| `connection` | `ECONNREFUSED`, `ENOTFOUND`, dan sejenisnya   | `Tidak bisa terhubung: ECONNREFUSED localhost:8091` |
| `http`       | ada panggilan API yang membalas 5xx (butuh fixture Redline) | `POST /users/login → 500`        |
| `error`      | selain itu: pesan error ternormalisasi sama   | baris pertama pesan error                      |

  Respons `POST /api/runs` berisi `incidents` (terbesar dulu) beserta `representative`: satu
  kelompok yang cukup dianalisis untuk seluruh insiden. Reporter Redline memakainya sehingga
  analisis dan biaya AI dihitung per insiden, bukan per test. 4xx sengaja tidak dipakai karena
  bisa jadi memang diharapkan oleh test negatif.

- Menganalisis penyebab tiap kelompok: `backend_bug`, `test_bug`, `environment`, `flaky`,
  atau `unknown` (lihat [Analisis penyebab](#analisis-penyebab)).

## Menjalankan

Butuh Go 1.24+ dan Docker.

```bash
cp .env.example .env
docker compose up -d          # Postgres di port 5433 (Carikan tetap di 5432)
go mod tidy                   # sekali saja, mengunduh pgx dan membuat go.sum
go run ./cmd/server           # API di http://localhost:8787, migrasi tabel otomatis
```

### Dipakai bersama tim

| Env              | Default        | Fungsi                                                                 |
|------------------|----------------|------------------------------------------------------------------------|
| `STATUS_FROM`    | `ci`           | Source run yang boleh mengubah status kelompok: `ci`, `local`, atau `all` |
| `TIMEZONE`       | `Asia/Jakarta` | Zona waktu semua waktu di respons API dan log, walaupun jam server UTC |
| `RETENTION_DAYS` | `30`           | Lihat [Menjaga ukuran database](#menjaga-ukuran-database)               |

Dengan `STATUS_FROM=ci`, run dari laptop tetap disimpan, dikelompokkan, dan bisa dianalisis,
tapi **tidak mengubah status bersama**: test yang kebetulan lulus di laptop tidak menutup
kelompok, dan kegagalan baru dari laptop ditandai `local_only` sehingga tidak muncul di
`GET /api/groups`. Begitu kegagalan yang sama terlihat di CI, kelompok itu menjadi kelompok
bersama dan dihitung baru sejak run CI tersebut. Respons ingest berisi `shared_status: false`
untuk run yang hanya pratinjau. Kalau Redline dipakai sendiri, pakai `STATUS_FROM=all`.

## Mengirim hasil test dari project Playwright

### Otomatis (disarankan): reporter Redline

Salin `reporters/redline.ts` (ada di project `coba2`) ke project Playwright, lalu pasang
**setelah** reporter JSON di `playwright.config.ts`:

```ts
reporter: [
  ["html"],
  ["json", { outputFile: "test-results/results.json" }],
  ["./reporters/redline.ts"],
],
```

Setiap `npx playwright test` selesai, reporter mengirim `results.json` ke Redline dan mencetak
ringkasan (BARU, REGRESSED, masih gagal, sudah beres). Commit dan branch diambil otomatis
dari git; `source` otomatis `ci` kalau env `CI` ada.

| Env                   | Fungsi                                          |
|-----------------------|-------------------------------------------------|
| `REDLINE_URL`         | alamat server (default `http://localhost:8787`) |
| `REDLINE_APP_VERSION` | versi backend yang dites                        |
| `REDLINE_AI=1`        | sekalian minta analisis penyebab                |
| `REDLINE_AI_MAX`      | batas kelompok yang dianalisis per run (10)     |
| `REDLINE=0`           | matikan pengiriman                              |

Kalau server Redline mati, test tetap jalan; hanya muncul peringatan. Run yang dihentikan
(Ctrl+C) dan `--list` tidak dikirim.

### Manual: curl

```bash
curl -sS -X POST "http://localhost:8787/api/runs?source=local&commit=$(git rev-parse --short HEAD)&app_version=sprint5-with-bugs" \
  --data-binary @test-results/results.json
```

Semua query string opsional:

| Parameter     | Isi                                                    |
|---------------|--------------------------------------------------------|
| `source`      | `local` (default) atau `ci`                            |
| `branch`      | nama branch                                            |
| `commit`      | commit kode test                                       |
| `app_version` | versi backend yang dites, misalnya `sprint5-with-bugs` |

Contoh balasan:

```json
{
  "run_id": 1, "total": 5, "passed": 0, "failed": 3, "flaky": 1, "skipped": 1,
  "new": [
    {
      "fingerprint": "b601d9e80a28d4e3",
      "test": "toolshop › api.spec.ts › register user",
      "error": "Error: expect(received).toBe(expected) // Object.is equality | Expected: 201 | Received: 500",
      "occurrences": 1
    }
  ],
  "recurring": [], "regressed": [], "resolved": [],
  "flaky_tests": ["toolshop › api.spec.ts › kadang lambat (flaky)"]
}
```

## API

| Endpoint                          | Fungsi                                                      |
|-----------------------------------|-------------------------------------------------------------|
| `POST /api/runs`                  | Upload `results.json`                                       |
| `GET /api/runs?limit=20`          | Daftar run terakhir                                         |
| `GET /api/groups`                 | Kelompok yang perlu dikerjakan (`open` + `regressed`)       |
| `GET /api/groups?status=all`      | Semua kelompok; bisa juga `status=resolved` atau `open,regressed` |
| `GET /api/groups/{fingerprint}`   | Detail kelompok + 20 kemunculan terakhir + hasil analisis |
| `POST /api/groups/{fingerprint}/analyze` | Analisis penyebab (pakai cache; `?force=1` untuk ulang) |
| `PUT /api/groups/{fingerprint}/label` | Label manual: `{"label":"test_bug","note":"...","by":"nama"}`; `by` wajib; label kosong menghapus |
| `GET /healthz`                    | Cek server hidup                                            |

## Analisis penyebab

Urutan keputusan, dari yang paling murah dan paling bisa dipercaya:

1. **Label manual.** Kalau manusia sudah memberi label, itu yang dipakai. Label adalah kebenaran
   akhir dan nantinya jadi data untuk mengukur akurasi AI.
2. **Cache.** Kelompok yang sudah pernah dianalisis dengan versi prompt yang sama tidak dianalisis
   ulang (tanpa biaya). Regressed selalu dianalisis ulang oleh reporter.
3. **Aturan deterministik.** Tanpa AI, tanpa biaya:
   - `ECONNREFUSED`, `ENOTFOUND`, dan sejenisnya → `environment`.
   - Status 2xx diharapkan tapi dapat 5xx → `backend_bug`.
   - **Matriks perubahan** dibanding run terakhir yang lulus:

     | Kode test | Bentuk response | Kesimpulan |
     |---|---|---|
     | sama | berubah (field hilang, tipe/status berubah) | `backend_bug` (high) |
     | berubah | sama / hanya field baru | `test_bug` (medium) |
     | sama | sama | tidak diputuskan aturan → AI (biasanya nilai yang berubah) |
     | berubah | berubah | tidak diputuskan aturan → AI |

4. **AI (Claude Haiku).** Hanya kalau tiga langkah di atas tidak menjawab dan `ANTHROPIC_API_KEY`
   diisi. AI menerima fakta dari database (pesan error, potongan kode test, riwayat status,
   bentuk response, dan matriks perubahan), lalu menjawab lewat tool dengan format JSON yang dipaksa.

### Dari mana sinyal perubahan berasal (tanpa git)

- **Sidik jari kode test**: reporter (`reporters/source-hash.ts`) menghitung hash dari blok
  `test(...)` itu sendiri ditambah file lokal yang di-import langsung oleh file spec (schema,
  helper, data, fixture). Spasi dan komentar diabaikan. Perubahan yang belum di-commit tetap
  terdeteksi, dan mengubah test lain di file yang sama tidak memengaruhi hash test ini.
- **Bentuk response**: fixture `fixtures/redline.ts` membungkus `request` dan mencatat
  method, path (id dinormalisasi jadi `:id`), status, serta nama field dan tipenya. **Isi data
  tidak dicatat** dan header tidak disimpan. Test hanya perlu mengganti import:

  ```ts
  import { test, expect } from "../../../fixtures/redline";
  ```

`commit` dan `app_version` tetap dicatat sebagai info tambahan, tapi tidak lagi jadi dasar keputusan.

Pengaman terhadap jawaban karangan: setiap "bukti" dari AI harus kutipan persis dari fakta yang
dikirim. Bukti yang tidak ditemukan dibuang; kalau tidak tersisa satu pun, confidence diturunkan
ke `low`.

Yang dikirim ke Anthropic hanya data yang sudah diredaksi saat ingest (token, password, JWT,
angka 16 digit) dan bentuk response tanpa isi. Matriks perubahan butuh run lulus sebelumnya
sebagai pembanding, jadi run pertama setelah memasang fixture belum punya pembanding.

Biaya: satu analisis sekitar 1.000 token input dan 150 token output. Dengan harga Haiku 5.5
($0,10 input / $0,50 output per 1 juta token) itu sekitar $0,0002. Biaya tiap analisis
disimpan di tabel `analyses` dan dicetak oleh reporter.

## Struktur

```
cmd/server/            entrypoint HTTP server
internal/report/       parser laporan JSON Playwright -> satu Outcome per test
internal/triage/       redaksi, normalisasi error, fingerprint
internal/analysis/     label manual, cache, aturan, lalu AI
internal/llm/          client minimal Claude Messages API
internal/store/        Postgres: migrasi, ingest (open/resolved/regressed), query
internal/api/          HTTP handler
```

Skema lengkap dengan penjelasan tiap kolom ada di `internal/store/migrations/001_schema.sql`.

| Tabel             | Isi                                                                        |
|-------------------|----------------------------------------------------------------------------|
| `runs`            | Satu kali eksekusi test: kapan, siapa (`triggered_by`), link CI (`ci_url`) |
| `failure_groups`  | Kelompok kegagalan: test, status, penyebab (`cause`), label manual         |
| `test_results`    | Hasil tiap test di tiap run; tabel terbesar, dibersihkan oleh retensi      |
| `response_shapes` | Bentuk response API yang unik (nama field + tipe, tanpa isi data)          |
| `analyses`        | Hasil analisis penyebab per kelompok                                       |

File besar (trace.zip, screenshot) tidak masuk database. Yang disimpan hanya path-nya, **relatif
terhadap project** (misalnya `test-results/brand-toolshop/trace.zip`), supaya tidak membocorkan
struktur folder laptop. Untuk run CI, buka `runs.ci_url` untuk mengunduh artifact berisi file itu.

Nama field di respons API (`fingerprint`, `incident`, `sample_error`, `human_label`) sengaja
belum diubah supaya reporter yang sudah ada tetap jalan; di database namanya `id`/`group_id`,
`cause_id`, `last_error`, dan `manual_category`.

### Menjaga ukuran database

- **Bentuk response disimpan sekali.** `test_results.response_shape_id` merujuk ke `response_shapes`,
  jadi 500 test yang memanggil endpoint dengan bentuk sama di 1.000 run tetap satu baris bentuk.
- **Retensi.** Setiap start lalu setiap 24 jam, detail `test_results` dari run yang lebih tua dari
  `RETENTION_DAYS` (default 30, `0` = mati) dihapus, **kecuali** hasil lulus terakhir tiap test
  (pembanding analisis) serta kemunculan pertama dan terakhir tiap kelompok kegagalan. Bentuk
  response yang tidak dirujuk lagi ikut dihapus. `runs` dan `failure_groups` tidak disentuh.

## Test

```bash
go test ./...                     # unit test; test database otomatis dilewati
REDLINE_TEST_DATABASE_URL="postgres://redline:redline@localhost:5433/redline_test?sslmode=disable" go test ./...
```

Database `redline_test` dibuat otomatis oleh `docker compose` saat volume pertama kali dibuat.
Tiap package memakai schema sendiri di database itu (`store_test`, `analysis_test`) yang
**dihapus dan dibuat ulang** setiap test jalan, jadi jangan arahkan ke database `redline`.
AI tidak dipanggil saat test; Claude ditiru dengan server palsu.
Kalau volume sudah ada sebelum file `scripts/create-test-db.sql` ditambahkan, buat manual:

```bash
docker exec redline-db psql -U redline -c "CREATE DATABASE redline_test"
```

### Fixture test

`internal/report/testdata/` berisi laporan dari run Playwright sungguhan terhadap API tiruan:
`run1-bug.json` (field `slug` hilang, register 500, status order salah, satu test flaky,
satu skip) dan `run2-fixed.json` (semua sudah diperbaiki).

## Rencana berikutnya

1. Perintah CLI `redline ingest` untuk CI (langsung ke database, tanpa server).
2. Halaman web kecil untuk melihat kelompok, hasil analisis, dan memberi label.
