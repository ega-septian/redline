// Package report membaca laporan JSON dari Playwright (reporter "json")
// dan meratakannya menjadi satu baris per test.
package report

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"path"
	"strconv"
	"strings"
	"time"
)

// Status akhir satu test, dari sudut pandang Redline.
const (
	StatusPassed  = "passed"  // lulus di percobaan pertama
	StatusFailed  = "failed"  // gagal di semua percobaan
	StatusFlaky   = "flaky"   // gagal dulu, lalu lulus saat retry
	StatusSkipped = "skipped" // dilewati (test.skip / fixme)
)

// Report adalah bagian dari results.json yang kita pakai. Field lain diabaikan.
type Report struct {
	Config struct {
		Version    string `json:"version"`
		RootDir    string `json:"rootDir"`
		ConfigFile string `json:"configFile"`
	} `json:"config"`
	Suites []Suite      `json:"suites"`
	Errors []ErrorEntry `json:"errors"` // error di luar test, misalnya globalSetup gagal
	Stats  struct {
		StartTime time.Time `json:"startTime"`
		Duration  float64   `json:"duration"`
	} `json:"stats"`

	// TestHashes: "file:line" -> sidik jari kode test, dikirim reporter Redline di luar results.json.
	TestHashes map[string]string `json:"-"`
	// TestFiles: "file:line" -> file lokal yang dipakai test (spec + import langsung), relatif terhadap project.
	TestFiles map[string][]string `json:"-"`
}

type Suite struct {
	Title  string  `json:"title"`
	File   string  `json:"file"`
	Line   int     `json:"line"`
	Specs  []Spec  `json:"specs"`
	Suites []Suite `json:"suites"`
}

type Spec struct {
	Title string `json:"title"`
	File  string `json:"file"`
	Line  int    `json:"line"`
	Tests []Test `json:"tests"`
}

type Test struct {
	ProjectName    string   `json:"projectName"`
	ExpectedStatus string   `json:"expectedStatus"`
	Status         string   `json:"status"` // expected, unexpected, flaky, skipped
	Results        []Result `json:"results"`
}

type Result struct {
	Status      string       `json:"status"` // passed, failed, timedOut, skipped, interrupted
	Duration    float64      `json:"duration"`
	Retry       int          `json:"retry"`
	StartTime   time.Time    `json:"startTime"`
	Error       *ErrorEntry  `json:"error"`
	Errors      []ErrorEntry `json:"errors"`
	Attachments []Attachment `json:"attachments"`
}

type ErrorEntry struct {
	Message  string    `json:"message"`
	Snippet  string    `json:"snippet"`
	Location *Location `json:"location"`
}

type Location struct {
	File   string `json:"file"`
	Line   int    `json:"line"`
	Column int    `json:"column"`
}

type Attachment struct {
	Name        string `json:"name"`
	ContentType string `json:"contentType"`
	Path        string `json:"path"`
	Body        string `json:"body"` // base64, untuk attachment yang dibuat dari isi (bukan file)
}

// HTTPCall adalah bentuk satu response API yang dicatat fixture Redline (tanpa isi data).
type HTTPCall struct {
	Method string          `json:"method"`
	Path   string          `json:"path"`
	Status int             `json:"status"`
	Shape  json.RawMessage `json:"shape"`
}

// attachmentHTTP adalah nama attachment dari fixture Redline (coba2/fixtures/redline.ts).
const attachmentHTTP = "redline-http"

// Outcome adalah hasil akhir satu test di satu project.
type Outcome struct {
	Project string
	File    string
	Title   string // judul lengkap: "describe › nama test"
	Line    int

	Status     string
	Retries    int // jumlah retry yang terjadi
	DurationMs int // total semua percobaan

	// Diisi dari percobaan yang gagal (untuk failed: percobaan terakhir,
	// untuk flaky: percobaan gagal pertama). Belum diredaksi.
	ErrorMessage  string
	ErrorSnippet  string
	ErrorLocation string // file:line:column
	TracePath     string
	Screenshots   []string

	// SourceHash: sidik jari kode test (kosong kalau reporter tidak mengirimnya).
	SourceHash string
	// CodeFiles: file lokal yang dipakai test (kosong kalau reporter tidak mengirimnya).
	CodeFiles []string
	// Calls: bentuk response API dari percobaan yang dilaporkan (kosong tanpa fixture Redline).
	Calls []HTTPCall
}

// Key mengidentifikasi test yang sama di run yang berbeda.
func (o Outcome) Key() string {
	return o.Project + " › " + o.File + " › " + o.Title
}

// Upload adalah body yang dikirim reporter Redline: results.json ditambah sidik jari kode test.
type upload struct {
	Playwright json.RawMessage     `json:"playwright"`
	TestHashes map[string]string   `json:"test_hashes"`
	TestFiles  map[string][]string `json:"test_files"`
}

// ParseUpload menerima dua format: bungkus dari reporter Redline
// ({"playwright": ..., "test_hashes": ...}) atau results.json mentah (misalnya dari curl).
func ParseUpload(data []byte) (*Report, error) {
	var u upload
	if err := json.Unmarshal(data, &u); err == nil && len(u.Playwright) > 0 {
		rep, err := Parse(bytes.NewReader(u.Playwright))
		if err != nil {
			return nil, err
		}
		rep.TestHashes = u.TestHashes
		rep.TestFiles = u.TestFiles
		return rep, nil
	}
	return Parse(bytes.NewReader(data))
}

// Parse membaca results.json.
func Parse(r io.Reader) (*Report, error) {
	var rep Report
	dec := json.NewDecoder(r)
	if err := dec.Decode(&rep); err != nil {
		return nil, fmt.Errorf("results.json tidak valid: %w", err)
	}
	if rep.Suites == nil && rep.Stats.StartTime.IsZero() {
		return nil, fmt.Errorf("ini bukan laporan JSON Playwright (tidak ada suites/stats)")
	}
	return &rep, nil
}

// Outcomes meratakan pohon suite menjadi daftar test.
func (r *Report) Outcomes() []Outcome {
	var out []Outcome
	for _, s := range r.Suites {
		out = walk(out, s, nil, true)
	}
	root := path.Dir(slash(r.Config.ConfigFile))
	for i := range out {
		loc := out[i].File + ":" + strconv.Itoa(out[i].Line)
		out[i].SourceHash = r.TestHashes[loc]
		out[i].CodeFiles = r.TestFiles[loc]
		out[i].TracePath = ProjectPath(root, out[i].TracePath)
		for j, p := range out[i].Screenshots {
			out[i].Screenshots[j] = ProjectPath(root, p)
		}
	}
	return out
}

// ProjectPath mengubah path di mesin yang menjalankan test menjadi path relatif terhadap project
// (folder playwright.config.ts), supaya tidak membocorkan struktur folder pribadi dan sama
// untuk semua anggota tim. Tanpa root yang cocok, dipotong mulai dari "test-results/", atau
// tinggal nama filenya.
func ProjectPath(root, p string) string {
	if p == "" {
		return ""
	}
	p = slash(p)
	if root != "" && root != "." && strings.HasPrefix(p, root+"/") {
		return strings.TrimPrefix(p, root+"/")
	}
	if i := strings.Index(p, "test-results/"); i >= 0 {
		return p[i:]
	}
	return path.Base(p)
}

// slash memakai "/" untuk semua path, termasuk path Windows dari runner CI lain.
func slash(p string) string { return strings.ReplaceAll(p, `\`, "/") }

func walk(out []Outcome, s Suite, titles []string, root bool) []Outcome {
	// Suite paling atas adalah file; judulnya sama dengan nama file, jadi tidak ikut judul test.
	if !root || s.Title != s.File {
		titles = append(append([]string(nil), titles...), s.Title)
	}
	for _, spec := range s.Specs {
		title := strings.Join(append(append([]string(nil), titles...), spec.Title), " › ")
		for _, t := range spec.Tests {
			out = append(out, toOutcome(spec, t, title))
		}
	}
	for _, child := range s.Suites {
		out = walk(out, child, titles, false)
	}
	return out
}

func toOutcome(spec Spec, t Test, title string) Outcome {
	o := Outcome{
		Project: t.ProjectName,
		File:    spec.File,
		Title:   title,
		Line:    spec.Line,
	}
	switch t.Status {
	case "expected":
		o.Status = StatusPassed
		if t.ExpectedStatus == "skipped" {
			o.Status = StatusSkipped
		}
	case "unexpected":
		o.Status = StatusFailed
	case "flaky":
		o.Status = StatusFlaky
	default:
		o.Status = StatusSkipped
	}
	if len(t.Results) > 0 {
		o.Retries = len(t.Results) - 1
	}
	for _, res := range t.Results {
		o.DurationMs += int(res.Duration)
	}

	var failed *Result
	for i := range t.Results {
		res := &t.Results[i]
		if res.Error == nil && len(res.Errors) == 0 {
			continue
		}
		failed = res
		if o.Status == StatusFlaky {
			break // flaky: ambil kegagalan pertama
		}
	}
	if failed != nil && o.Status != StatusPassed {
		fillError(&o, failed)
	}
	// Bentuk response: dari percobaan yang gagal (failed) atau percobaan terakhir (passed).
	// Flaky dilewati: dua percobaannya bisa berbeda, jadi tidak cocok jadi pembanding.
	switch {
	case o.Status == StatusFailed && failed != nil:
		o.Calls = httpCalls(failed)
	case o.Status == StatusPassed && len(t.Results) > 0:
		o.Calls = httpCalls(&t.Results[len(t.Results)-1])
	}
	return o
}

func httpCalls(res *Result) []HTTPCall {
	for _, a := range res.Attachments {
		if a.Name != attachmentHTTP || a.Body == "" {
			continue
		}
		raw, err := base64.StdEncoding.DecodeString(a.Body)
		if err != nil {
			return nil
		}
		var calls []HTTPCall
		if json.Unmarshal(raw, &calls) != nil {
			return nil
		}
		return calls
	}
	return nil
}

func fillError(o *Outcome, res *Result) {
	e := res.Error
	if e == nil {
		e = &res.Errors[0]
	}
	o.ErrorMessage = e.Message
	if e.Message == "" {
		var msgs []string
		for _, x := range res.Errors {
			msgs = append(msgs, x.Message)
		}
		o.ErrorMessage = strings.Join(msgs, "\n\n")
	}
	o.ErrorSnippet = e.Snippet
	if e.Location != nil {
		o.ErrorLocation = fmt.Sprintf("%s:%d:%d", e.Location.File, e.Location.Line, e.Location.Column)
	}
	for _, a := range res.Attachments {
		switch {
		case a.Name == "trace" && a.Path != "":
			o.TracePath = a.Path
		case a.Name == "screenshot" && a.Path != "":
			o.Screenshots = append(o.Screenshots, a.Path)
		}
	}
}
