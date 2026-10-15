// Package reportportal mengisi hasil analisis Redline ke ReportPortal: jenis defect dan komentar untuk test
// yang gagal dan masih "To Investigate". Redline tidak pernah menimpa label dari manusia atau dari
// Auto-Analysis ReportPortal.
package reportportal

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client memanggil REST API ReportPortal (v1) untuk satu project.
type Client struct {
	BaseURL string // misalnya http://localhost:8585
	Project string // misalnya superadmin_personal
	APIKey  string
	HTTP    *http.Client
}

// Launch adalah satu launch ReportPortal.
type Launch struct {
	ID     int64  `json:"id"`
	UUID   string `json:"uuid"`
	Number int    `json:"number"`
	Status string `json:"status"` // IN_PROGRESS selama test masih dilaporkan
}

// Finished: launch sudah ditutup oleh agen Playwright.
func (l Launch) Finished() bool { return l.Status != "" && l.Status != "IN_PROGRESS" }

// Item adalah satu test di sebuah launch.
type Item struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	TestCaseID string `json:"testCaseId"`
	Issue      *struct {
		IssueType    string `json:"issueType"`
		AutoAnalyzed bool   `json:"autoAnalyzed"`
	} `json:"issue"`
}

// IssueType: locator jenis defect saat ini, misalnya "ti001". Kosong kalau belum ada.
func (i Item) IssueType() string {
	if i.Issue == nil {
		return ""
	}
	return i.Issue.IssueType
}

// IssueUpdate mengubah jenis defect dan komentar satu item.
type IssueUpdate struct {
	ItemID    int64
	IssueType string // locator, misalnya "pb001"
	Comment   string
}

func (c *Client) do(ctx context.Context, method, path string, body any, out any) error {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method,
		strings.TrimRight(c.BaseURL, "/")+"/api/v1/"+url.PathEscape(c.Project)+path, r)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("ReportPortal %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("ReportPortal %s %s: status %d: %s", method, path, resp.StatusCode, strings.TrimSpace(string(data)))
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("ReportPortal %s %s: jawaban tidak valid: %w", method, path, err)
		}
	}
	return nil
}

// LaunchByAttribute mengembalikan launch terbaru yang punya atribut key:value, atau nil kalau belum ada.
func (c *Client) LaunchByAttribute(ctx context.Context, key, value string) (*Launch, error) {
	q := url.Values{
		"filter.has.compositeAttribute": {key + ":" + value},
		"page.sort":                     {"startTime,desc"},
		"page.size":                     {"1"},
	}
	var page struct {
		Content []Launch `json:"content"`
	}
	if err := c.do(ctx, http.MethodGet, "/launch?"+q.Encode(), nil, &page); err != nil {
		return nil, err
	}
	if len(page.Content) == 0 {
		return nil, nil
	}
	return &page.Content[0], nil
}

// FailedItems mengembalikan test yang gagal di sebuah launch. Langkah test (step) tidak ikut: hanya item yang
// punya statistik sendiri dan tidak punya anak.
func (c *Client) FailedItems(ctx context.Context, launchID int64) ([]Item, error) {
	out := []Item{}
	for page := 1; ; page++ {
		q := url.Values{
			"filter.eq.launchId":    {fmt.Sprint(launchID)},
			"filter.eq.status":      {"FAILED"},
			"filter.eq.hasStats":    {"true"},
			"filter.eq.hasChildren": {"false"},
			"page.size":             {"300"},
			"page.page":             {fmt.Sprint(page)},
		}
		var res struct {
			Content []Item `json:"content"`
			Page    struct {
				TotalPages int `json:"totalPages"`
			} `json:"page"`
		}
		if err := c.do(ctx, http.MethodGet, "/item?"+q.Encode(), nil, &res); err != nil {
			return nil, err
		}
		out = append(out, res.Content...)
		if page >= res.Page.TotalPages {
			return out, nil
		}
	}
}

// UpdateIssues mengubah jenis defect dan komentar beberapa item sekaligus.
func (c *Client) UpdateIssues(ctx context.Context, updates []IssueUpdate) error {
	if len(updates) == 0 {
		return nil
	}
	type issue struct {
		IssueType      string `json:"issueType"`
		Comment        string `json:"comment"`
		AutoAnalyzed   bool   `json:"autoAnalyzed"`
		IgnoreAnalyzer bool   `json:"ignoreAnalyzer"`
	}
	type entry struct {
		TestItemID int64 `json:"testItemId"`
		Issue      issue `json:"issue"`
	}
	body := struct {
		Issues []entry `json:"issues"`
	}{}
	for _, u := range updates {
		body.Issues = append(body.Issues, entry{TestItemID: u.ItemID, Issue: issue{IssueType: u.IssueType, Comment: u.Comment}})
	}
	return c.do(ctx, http.MethodPut, "/item", body, nil)
}
