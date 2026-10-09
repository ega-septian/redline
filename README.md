# Redline

Saat test Playwright gagal, pertanyaan pertama biasanya sama: **ini bug di aplikasi, bug di
test, atau cuma server yang lagi bermasalah?** Redline membantu menjawabnya.

Setiap kali test selesai, hasilnya dikirim ke Redline. Redline lalu:

- **Mengelompokkan kegagalan yang sama**, jadi test yang gagal dengan error yang sama di sepuluh
  run tetap terlihat sebagai satu masalah, bukan sepuluh.
- **Mengingat riwayatnya**: apakah kegagalan ini baru, masih terjadi, sudah beres, atau muncul
  lagi setelah sempat beres.
- **Menggabungkan kegagalan yang penyebabnya sama.** Kalau server mati dan 300 test ikut gagal,
  yang muncul adalah satu penyebab, bukan 300 baris.
- **Menebak penyebabnya**: bug backend, bug di test, masalah environment, atau test flaky.
  Aturan sederhana dicoba dulu; AI (Claude) baru dipakai kalau aturan tidak cukup.

Token, password, JWT, dan angka 16 digit (NIK atau nomor kartu) disamarkan **sebelum** apa pun
disimpan.

## Mulai cepat

Butuh Go 1.24+ dan Docker.

```bash
cp .env.example .env
docker compose up -d        # Postgres + pgvector di port 5433, supaya tidak bentrok dengan Postgres lain
go run ./cmd/server         # API di http://localhost:8787; tabel dibuat otomatis
```

Dua key opsional di `.env`:

- `ANTHROPIC_API_KEY`: analisis AI dan eksperimen patch. Tanpa ini, Redline jalan dengan aturan saja.
- `VOYAGE_API_KEY`: pencarian kasus mirip dengan embedding ([Voyage AI](https://dashboard.voyageai.com),
  200 juta token pertama gratis). Tanpa ini, analisis jalan tanpa kasus mirip.

## Menghubungkan project Playwright

### Cara yang disarankan: reporter Redline

Salin tiga file ini dari project `coba2` ke project Playwright-mu:

| File | Gunanya |
|---|---|
| `reporters/redline.ts` | Mengirim hasil test ke Redline dan mencetak ringkasan |
| `reporters/source-hash.ts` | Mendeteksi apakah kode test berubah sejak terakhir lulus |
| `fixtures/redline.ts` | Mencatat bentuk response API (opsional, tapi membuat analisis jauh lebih akurat) |

Pasang reporter **setelah** reporter JSON di `playwright.config.ts`:

```ts
reporter: [
  ["html"],
  ["json", { outputFile: "test-results/results.json" }],
  ["./reporters/redline.ts"],
],
```

Untuk memakai fixture, ganti import di file test:

```ts
import { test, expect } from "../../../fixtures/redline";
```

Selesai. Setiap `npx playwright test`, ringkasan seperti ini muncul di terminal:

```
[redline] run #12 · 8 Okt 2026, 19.36 WIB: 40 lulus, 9 gagal, 0 flaky, 1 skip
  9 kegagalan dari 2 penyebab

  #  INSIDEN                             TEST  STATUS     KATEGORI     PENYEBAB
  1  POST /users/login → 500                8  BARU       BUG BACKEND  API membalas 500 padahal test…
  2  TC-USR-001 Register user               1  REGRESSED  BUG DI TEST  Bentuk response API sama…

  Laporan lengkap: test-results/redline-report.md
```

Laporan lengkapnya ditulis ke `test-results/redline-report.md`, dan di GitHub Actions juga
muncul di halaman ringkasan job. Kalau server Redline sedang mati, test tetap jalan seperti
biasa; hanya muncul peringatan.

Pengaturan reporter lewat environment variable:

| Env | Fungsi |
|---|---|
| `REDLINE_URL` | Alamat server (default `http://localhost:8787`) |
| `REDLINE_AI=1` | Minta analisis penyebab untuk setiap kegagalan |
| `REDLINE_AI_MAX` | Batas penyebab yang dianalisis per run (default 10) |
| `REDLINE_APP_VERSION` | Versi backend yang sedang dites, misalnya `sprint5-with-bugs` |
| `REDLINE_TRIGGERED_BY` | Nama yang menjalankan test (default: user laptop, atau `ci:<akun GitHub>`) |
| `REDLINE=0` | Matikan pengiriman |

Untuk memberi ID test case, pakai tag: `test("...", { tag: "@TC-USR-001" }, ...)`.

### Tanpa reporter: curl

```bash
curl -sS -X POST "http://localhost:8787/api/runs?source=local&commit=$(git rev-parse --short HEAD)" \
  --data-binary @test-results/results.json
```

Semua parameter opsional: `source` (`local` atau `ci`), `branch`, `commit`, `app_version`,
`triggered_by`, dan `ci_url`.

## Dipakai bersama tim

Pengaturan server di `.env`:

| Env | Default | Fungsi |
|---|---|---|
| `STATUS_FROM` | `ci` | Run dari mana yang boleh mengubah status: `ci`, `local`, atau `all` |
| `TIMEZONE` | `Asia/Jakarta` | Zona waktu di API, log, dan database, walaupun jam server UTC |
| `RETENTION_DAYS` | `30` | Detail hasil test yang lebih tua dari ini dibersihkan (`0` = mati) |

**Kenapa hanya CI yang mengubah status?** Bayangkan dua situasi ini kalau run dari laptop ikut
dihitung:

- Seseorang sedang menulis test baru yang belum selesai. Test-nya gagal, dan kegagalan itu
  langsung muncul di daftar tim.
- Seseorang menjalankan satu test yang kebetulan lulus di laptopnya. Kegagalan yang sedang
  diselidiki tim langsung dianggap beres, padahal di CI masih gagal.

Dengan `STATUS_FROM=ci`, run dari laptop tetap disimpan dan bisa dianalisis, tapi hanya sebagai
**pratinjau**. Kegagalan baru dari laptop disembunyikan dari daftar tim sampai muncul juga di CI.
Kalau kamu memakai Redline sendirian, pakai `STATUS_FROM=all`.

## Cara kerjanya

### Kelompok kegagalan

Test yang sama yang gagal dengan error yang sama masuk ke satu **kelompok kegagalan**. Supaya
error yang "sama" tetap dikenali walaupun detailnya berubah tiap run, bagian yang berubah-ubah
(UUID, ULID, waktu, email, id panjang) diabaikan. Kode status tetap dibedakan, jadi 404 dan 500
masuk kelompok yang berbeda.

Setiap kelompok punya status:

```
gagal pertama kali              → open
lulus lagi                      → resolved
gagal lagi dengan error sama    → regressed
```

Test flaky (gagal, lalu lulus saat retry) dicatat terpisah dan tidak membuka kelompok.

### Penyebab yang sama

Kegagalan dari banyak test digabung kalau penyebabnya sama:

| Jenis | Kapan | Contoh |
|---|---|---|
| `connection` | Server tidak bisa dihubungi (`ECONNREFUSED`, `ENOTFOUND`, …) | `Tidak bisa terhubung: ECONNREFUSED localhost:8091` |
| `http` | Ada panggilan API yang membalas 5xx (butuh fixture) | `POST /users/login → 500` |
| `error` | Selain itu, kalau pesan error-nya sama persis | baris pertama pesan error |

Analisis cukup dilakukan sekali per penyebab, jadi biaya AI dihitung per penyebab, bukan per
test. Respons 4xx sengaja tidak dipakai untuk menggabungkan, karena bisa jadi memang yang
diharapkan oleh test negatif.

### Menentukan penyebab

Redline mencoba dari cara yang paling murah dan paling bisa dipercaya:

1. **Label dari manusia.** Kalau seseorang sudah memberi label, itu yang dipakai.
2. **Bukti eksperimen.** Test lulus setelah patch → terbukti `test_bug`. Test lulus saat diulang
   tanpa perubahan → `flaky`. Lihat [Eksperimen](#eksperimen-membuktikan-bukan-menebak).
3. **Aturan tetap**, tanpa AI dan tanpa biaya:
   - Server tidak bisa dihubungi → `environment`
   - Test mengharapkan 2xx tapi dapat 5xx → `backend_bug`
   - Yang diperiksa ternyata Promise (`received Promise`) → `test_bug`, kemungkinan lupa `await`
   - Aturan yang **dipelajari** dan sudah disetujui. Lihat [Aturan yang dipelajari](#aturan-yang-dipelajari)
   - Dibandingkan dengan run terakhir yang lulus:

     | Kode test | Bentuk response API | Kesimpulan |
     |---|---|---|
     | sama | berubah (field hilang, tipe atau status berubah) | `backend_bug` (mengalahkan aturan yang dipelajari) |
     | berubah | sama, atau hanya ada field baru | `test_bug` |
     | lainnya | | diserahkan ke AI |

4. **Hasil AI sebelumnya.** Kelompok yang sudah pernah dianalisis AI tidak dianalisis ulang,
   kecuali kegagalannya muncul lagi setelah sempat beres.
5. **AI (Claude).** Hanya kalau langkah di atas tidak menjawab. AI menerima fakta yang sudah
   disamarkan: pesan error, potongan kode test, riwayat status, dan bentuk response (nama field
   dan tipenya, tanpa isi data).

Supaya AI tidak mengarang, setiap bukti yang disebutkannya harus berupa kutipan persis dari
fakta yang dikirim. Bukti yang tidak ditemukan dibuang, dan kalau tidak ada yang tersisa,
tingkat keyakinannya diturunkan ke `low`.

Satu analisis AI sekitar 1.000 token input dan 150 token output, atau kira-kira **$0,0002**
dengan harga Claude Haiku 5.5. Biaya setiap analisis dicatat dan ditampilkan oleh reporter.

### Eksperimen: membuktikan, bukan menebak

AI yang hanya membaca pesan error tetap menebak. Eksperimen menguji tebakan itu dengan
menjalankan test lagi:

```bash
npm run redline:verify     # kegagalan run terakhir (dari test-results/redline-last-run.json)
```

1. **Rerun.** Test dijalankan ulang 2x tanpa perubahan. Kalau lulus, kegagalannya tidak
   konsisten (`flaky`) dan eksperimen berhenti di sini.
2. **Patch.** AI menerima fakta + kode test (spec dan file lokal yang di-import, sudah
   disamarkan), lalu mengusulkan hipotesis dan perubahan sekecil mungkin. Test dijalankan
   dengan patch itu. Lulus berarti hipotesisnya **terbukti**; gagal berarti AI mencoba lagi
   dengan error barunya (default 2 kali).

Semua berjalan di **salinan project** di folder sementara, jadi kode kamu tidak berubah.
Patch yang terbukti disimpan di `test-results/redline/<id>.patch` untuk di-review dan
diterapkan dengan `git apply`.

AI bisa "curang" supaya test lulus, jadi patch ditolak otomatis kalau mengurangi jumlah
`expect`, matcher, atau `.parse`, atau kalau menambah `skip`/`only`/`fixme`, `try/catch`,
atau retry/timeout. Kalau menurut AI penyebabnya di backend atau environment, AI tidak
membuat patch: itu tidak bisa dibuktikan dengan mengubah test.

Eksperimen dijalankan di mesin yang punya kode test. Server hanya membuat hipotesis
(`POST /api/groups/{id}/fix`) dan menyimpan hasilnya (`POST /api/groups/{id}/experiments`),
dan tidak pernah menjalankan kode.

### Aturan yang dipelajari

Setiap kasus yang sudah terbukti (label manual, atau patch yang lulus) bisa dijadikan aturan,
supaya kasus serupa berikutnya diputuskan tanpa AI:

```bash
npm run redline:learn            # AI mengusulkan pola regex dari kasus terbukti
npm run redline -- rules         # daftar aturan
npm run redline -- approve 3     # aktifkan (atau: reject 3)
```

Sebelum disimpan, setiap usulan diuji ke data lama:

- harus cocok dengan minimal satu kasus terbukti berkategori sama
- tidak boleh cocok dengan kasus terbukti berkategori lain
- tidak boleh cocok dengan lebih dari separuh semua kelompok (terlalu umum)
- tidak boleh sama dengan aturan yang pernah ditolak. "Sama" berarti mencocokkan kelompok yang
  persis sama, walaupun teks regex-nya berbeda

Aturan baru berstatus `proposed` dan baru dipakai setelah disetujui manusia. Regex memakai
sintaks Go (RE2), yang waktu prosesnya selalu linear, jadi pola dari AI tidak bisa membuat
server macet.

Hasilnya, semakin lama Redline dipakai, semakin banyak kegagalan yang diputuskan oleh aturan
dan semakin sedikit biaya AI.

### Kontrak API: siapa yang menyimpang

Untuk test yang baru ditulis, kegagalan "test mengharapkan X, API memberi Y" tidak bisa diputuskan dari
kode saja: tidak ada acuan siapa yang benar. Kontrak API (OpenAPI) menjadi acuannya:

```
OPENAPI_SPECS=toolshop=http://localhost:8091/docs     # project Playwright = URL OpenAPI 3 (JSON)
```

- **Dipotret setiap run.** Kontrak berubah antar versi aplikasi (misalnya sprint1 → sprint2), jadi
  analisis memakai kontrak **saat test gagal**, bukan kontrak terbaru. Disimpan sekali per isi.
- **Cek kesesuaian otomatis, tanpa AI.** Bentuk response yang tercatat dicocokkan dengan schema
  kontrak: status yang tidak terdokumentasi, field yang hilang, atau tipe yang berbeda.
- **Dimensi ketiga matriks perubahan**, untuk membedakan perubahan yang disengaja dari regresi:

  | Kode test | Response API | Kontrak | Kesimpulan |
  |---|---|---|---|
  | sama | berubah | sama | `backend_bug`: regresi |
  | sama | berubah | berubah, response mengikutinya | `test_bug`: test usang, perubahan disengaja |
  | sama | berubah | berubah, response tidak mengikutinya | `backend_bug` |
  | (test baru) | melanggar kontrak | – | `backend_bug` |

Tanpa kontrak, Redline tetap jalan: riwayat (matriks perubahan) menangani regresi, dan sisanya
diputuskan AI dengan keyakinan lebih rendah.

### Ingatan: peta kode dan kasus mirip

Setiap analisis AI mendapat dua jenis ingatan:

- **Peta kode.** Reporter mengirim file lokal yang dipakai setiap test (file spec dan file yang
  di-import langsung), dan fixture mencatat endpoint yang dipanggil. Redline lalu menghitung test
  lain di run yang sama yang memakai endpoint atau file yang sama:

  ```
  - endpoint GET /brands: 3 test lain lulus, 0 gagal         → masalah condong ke test ini
  - file tests/.../brand.schema.ts: 0 test lain lulus, 4 gagal → condong ke schema bersama atau backend
  ```

- **Kasus mirip.** Sampai 3 kasus terbukti (label manual atau patch yang lulus) yang pesan
  error-nya paling mirip **maknanya**. Pesan error diubah menjadi embedding oleh Voyage AI
  (`voyage-4-lite`), disimpan di Postgres dengan pgvector, lalu dibandingkan dengan cosine
  similarity. Kasus ini dikirim sebagai **referensi, bukan bukti**, dan tidak boleh dikutip
  sebagai evidence, karena error yang mirip bisa saja penyebabnya berbeda.

  Contoh nyata: `Matcher error: received value must have a length property… Received has value:
  undefined` dan `Cannot read properties of undefined (reading 'length')` kata-katanya berbeda,
  tapi kemiripannya 0.81, dan keduanya memang salah bentuk response di test.

  Beberapa hal yang sengaja dibuat begini:

  - **Dibuat sekali per kelompok, sekaligus satu request per run.** Setelah ingest, semua kelompok
    yang belum punya embedding dikirim ke Voyage dalam satu batch. Sekitar 30–60 token per error.
  - **Batasnya 0.70.** Embedding membuat teks yang tidak berhubungan pun mendapat skor 0.5–0.7,
    jadi batas yang rendah akan memasukkan referensi yang menyesatkan.
  - **Model disimpan bersama embedding.** Kalau `VOYAGE_MODEL` diganti, hanya embedding dari model
    yang sama yang dibandingkan, dan semuanya dibuat ulang otomatis.
  - **Teks yang hampir sama belum tentu penyebabnya sama.** `Received: 500` dan `Received: 422`
    mendapat skor 0.93, padahal yang satu bug server dan yang lain validasi. Karena itu kasus mirip
    tidak pernah memutuskan sendiri; aturan dan eksperimen tetap yang menentukan.

### Benchmark: bug yang sengaja ditanam

Rapor butuh waktu untuk terisi, jadi akurasi juga diukur dengan benchmark di project Playwright
(`npm run redline:bench`, lihat coba2): bug dengan jawaban yang sudah diketahui ditanam ke salinan
project, lalu Redline menganalisisnya di server dan schema database terpisah.

Hasil pada 24 bug (12 di test, 9 di backend lewat proxy yang merusak response, 3 environment) dan
6 kasus upgrade versi, dengan Claude Haiku 5.5:

| Skenario | Awal | Sekarang | Salah tebak |
|---|---|---|---|
| Test baru (cold start) | 46% | **92%** | 0 (2 "belum jelas") |
| Test yang pernah lulus | 88% | **96%** | 0 (1 "belum jelas") |
| Upgrade versi (test usang vs regresi) | – | **6/6** | 0 |

Yang paling menaikkan angka: kode test dikirim ke AI, kalibrasi agar tidak terlalu sering menjawab
"belum jelas", kontrak API, dan AI menulis alasannya dulu sebelum memilih kategori. Satu run per
skenario; hasil AI bisa sedikit berbeda antar run.

### Rapor: seberapa sering tebakan benar

Setiap tebakan aturan dan AI dicatat. Setelah ada bukti (eksperimen yang lulus atau label
manual), tebakan **sebelum** bukti itu dinilai benar atau salah:

```bash
npm run redline -- score
```

```
  aturan  4/4 benar (100%)
  AI      5/7 benar (71%), 1 tidak menebak
```

`npm run redline:verify` selalu mencatat tebakan dulu sebelum eksperimen, lalu menampilkan
hasilnya, misalnya `AI: BUG BACKEND (high) → ❌ salah (terbukti BUG DI TEST)`. Reporter juga
menampilkan satu baris rapor setiap kali ada kegagalan.

Rapor ini yang dipakai untuk menilai apakah perubahan prompt atau ingatan benar-benar membuat
analisis lebih tepat, bukan sekadar terasa lebih pintar.

### Dari mana Redline tahu sesuatu berubah

Tanpa membaca git:

- **Kode test.** Reporter menghitung sidik jari dari blok `test(...)` beserta file yang
  di-import langsung (schema, helper, data). Spasi dan komentar diabaikan, dan perubahan yang
  belum di-commit tetap terdeteksi.
- **Response API.** Fixture mencatat method, path, status, serta nama field dan tipenya.
  Isi data dan header **tidak** dicatat.

Karena butuh pembanding, run pertama setelah memasang fixture belum bisa dibandingkan.

## API

| Endpoint | Fungsi |
|---|---|
| `POST /api/runs` | Kirim `results.json` |
| `GET /api/runs?limit=20` | Daftar run terakhir |
| `GET /api/groups` | Kegagalan yang perlu dikerjakan (`open` dan `regressed`) |
| `GET /api/groups?status=all` | Semua kegagalan; bisa juga `resolved` atau `open,regressed` |
| `GET /api/groups/{id}` | Detail kegagalan, 20 kemunculan terakhir, dan hasil analisis |
| `POST /api/groups/{id}/analyze` | Analisis penyebab; `?force=1` untuk mengulang |
| `PUT /api/groups/{id}/label` | Beri label: `{"label":"test_bug","note":"...","by":"nama"}` |
| `POST /api/groups/{id}/fix` | AI membuat hipotesis + patch: `{"files":[{"path","content"}],"attempts":[...]}` |
| `POST /api/groups/{id}/experiments` | Simpan hasil eksperimen (`kind` rerun/patch, `outcome`, `runs`, `passes`, `patch`) |
| `GET /api/rules?status=proposed` | Daftar aturan yang dipelajari (`proposed`, `active`, `rejected`, kosong = semua) |
| `POST /api/rules/propose` | AI mengusulkan aturan dari kasus terbukti |
| `PUT /api/rules/{id}` | Setujui atau tolak: `{"status":"active","by":"nama"}` |
| `GET /api/scoreboard?limit=20` | Rapor akurasi tebakan aturan dan AI dibanding bukti |
| `GET /healthz` | Cek server hidup |

Label harus salah satu dari `backend_bug`, `test_bug`, `environment`, `flaky`, atau `unknown`,
dan `by` wajib diisi. Label kosong menghapus label.

Beberapa nama field di respons API masih memakai nama lama (`fingerprint`, `incident`,
`sample_error`, `human_label`) supaya reporter yang sudah terpasang tetap jalan.

## Data

Skema lengkap, dengan penjelasan setiap kolom, ada di
[`internal/store/migrations/`](internal/store/migrations/).

| Tabel | Isi |
|---|---|
| `runs` | Setiap kali test dijalankan: kapan, oleh siapa, dari mana, dan link CI-nya |
| `failure_groups` | Kelompok kegagalan beserta status, penyebab, dan label manual |
| `test_results` | Hasil setiap test di setiap run (tabel terbesar) |
| `response_shapes` | Bentuk response API yang unik, disimpan sekali |
| `analyses` | Hasil analisis penyebab |
| `experiments` | Hasil eksperimen (rerun dan patch), termasuk hipotesis dan patch-nya |
| `learned_rules` | Aturan yang dipelajari: pola, hasil uji ke data lama, dan siapa yang menyetujui |
| `predictions` | Riwayat tebakan aturan dan AI, untuk rapor |
| `source_files` | Kode test untuk test yang gagal (spec + import lokal), sudah disamarkan |
| `contracts`, `run_contracts` | Potret kontrak API per versi, dan kontrak yang berlaku di setiap run |

File besar seperti trace dan screenshot **tidak** masuk database. Yang disimpan hanya path-nya,
relatif terhadap project (misalnya `test-results/brand-toolshop/trace.zip`), supaya sama untuk
semua anggota tim dan tidak membocorkan struktur folder laptop. Untuk run CI, file-nya bisa
diunduh dari artifact di halaman `ci_url`.

Supaya database tidak terus membengkak:

- **Bentuk response disimpan sekali.** 500 test yang memanggil endpoint yang sama di 1.000 run
  tetap tersimpan sebagai satu bentuk.
- **Detail lama dibersihkan setiap hari.** Hasil test yang lebih tua dari `RETENTION_DAYS`
  dihapus, kecuali yang masih dibutuhkan untuk analisis: hasil lulus terakhir setiap test, serta
  kemunculan pertama dan terakhir setiap kelompok kegagalan.

## Pengembangan

```
cmd/server/          entrypoint HTTP server
internal/report/     membaca laporan JSON Playwright
internal/triage/     penyamaran data, pengelompokan kegagalan dan penyebab
internal/analysis/   label manual, cache, aturan, lalu AI
internal/llm/        client Claude API
internal/store/      Postgres: skema, penyimpanan, query, retensi
internal/api/        HTTP handler
```

Menjalankan test:

```bash
go test ./...   # tanpa database: test yang butuh Postgres dilewati

REDLINE_TEST_DATABASE_URL="postgres://redline:redline@localhost:5433/redline_test?sslmode=disable" go test ./...
```

Database `redline_test` dibuat otomatis oleh `docker compose`. Setiap package test memakai
schema sendiri di dalamnya yang **dihapus dan dibuat ulang** setiap kali test jalan, jadi jangan
arahkan ke database `redline`. AI tidak benar-benar dipanggil saat test.

Kalau volume Docker sudah ada sebelum database test ditambahkan, buat manual:

```bash
docker exec redline-db psql -U redline -c "CREATE DATABASE redline_test"
```

`internal/report/testdata/` berisi laporan dari run Playwright sungguhan terhadap API tiruan:
`run1-bug.json` (ada field yang hilang, error 500, status yang salah, satu test flaky, dan satu
yang di-skip) dan `run2-fixed.json` (semuanya sudah diperbaiki).

## Rencana berikutnya

- Perintah `redline ingest` untuk CI, langsung ke database tanpa server
- Halaman web kecil untuk melihat kegagalan, hasil analisis, dan memberi label
