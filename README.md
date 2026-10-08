# Redline

Redline membaca hasil test Playwright, mengelompokkan kegagalan yang sama, dan melacak
statusnya dari run ke run. Tujuan akhirnya menjawab: **"kenapa test ini gagal?"**

Isinya:

- Menerima `results.json` dari reporter JSON Playwright.
- Meredaksi token, password, JWT, dan angka 16 digit (NIK/kartu) **sebelum** disimpan.
- Membuat fingerprint dari test + pesan error yang dinormalisasi (UUID, ULID, waktu, email,
  dan id panjang diabaikan; kode status seperti 404 vs 500 tetap dibedakan).
- Melacak status tiap kelompok kegagalan:

```
gagal pertama kali  → open
lulus bersih        → resolved
gagal lagi (sama)   → regressed
```

Test flaky (gagal lalu lulus saat retry) dicatat terpisah dan tidak membuka kelompok.

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
| `PUT /api/groups/{fingerprint}/label` | Label manual: `{"label":"test_bug","note":"..."}`; label kosong menghapus |
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

Tabel: `runs`, `test_results`, `failure_groups`, `analyses`. File besar (trace.zip, screenshot) tidak
masuk database; yang disimpan hanya path-nya di mesin yang menjalankan test.

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

1. Korelasi antar test: kalau semua test yang memanggil endpoint yang sama gagal bersamaan,
   condong ke backend/environment; kalau hanya satu, condong ke test.
2. Perintah CLI `redline ingest` untuk CI (langsung ke database, tanpa server).
3. Halaman web kecil untuk melihat kelompok, hasil analisis, dan memberi label.
