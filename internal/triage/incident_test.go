package triage

import (
	"testing"

	"redline/internal/report"
)

func TestIncidentKey(t *testing.T) {
	login500 := []report.HTTPCall{{Method: "POST", Path: "/users/login", Status: 500}}

	cases := []struct {
		name      string
		err       string
		calls     []report.HTTPCall
		wantKind  string
		wantLabel string
	}{
		{
			name:      "server mati",
			err:       "Error: apiRequestContext.get: connect ECONNREFUSED 127.0.0.1:8091",
			wantKind:  IncidentConnection,
			wantLabel: "Tidak bisa terhubung: ECONNREFUSED localhost:8091",
		},
		{
			name:      "server mati, localhost lewat IPv6",
			err:       "AggregateError: apiRequestContext.call: connect ECONNREFUSED ::1:9\n- → GET http://localhost:9/products",
			wantKind:  IncidentConnection,
			wantLabel: "Tidak bisa terhubung: ECONNREFUSED localhost:9",
		},
		{
			name:      "host tidak ditemukan",
			err:       "Error: getaddrinfo ENOTFOUND api.toolshop.test",
			wantKind:  IncidentConnection,
			wantLabel: "Tidak bisa terhubung: ENOTFOUND api.toolshop.test",
		},
		{
			name:      "endpoint 5xx",
			err:       "expect(received).toBe(expected)\nExpected: 200\nReceived: 500",
			calls:     []report.HTTPCall{{Method: "GET", Path: "/brands", Status: 200}, login500[0]},
			wantKind:  IncidentHTTP,
			wantLabel: "POST /users/login → 500",
		},
		{
			name:      "4xx tidak dianggap insiden HTTP",
			err:       "expect(received).toBe(expected)\nExpected: 201\nReceived: 422",
			calls:     []report.HTTPCall{{Method: "POST", Path: "/users/register", Status: 422}},
			wantKind:  IncidentError,
			wantLabel: "expect(received).toBe(expected)",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			key, kind, label := IncidentKey(Normalize(c.err), c.calls)
			if key == "" || kind != c.wantKind || label != c.wantLabel {
				t.Errorf("dapat (%q, %q, %q), mau kind %q label %q", key, kind, label, c.wantKind, c.wantLabel)
			}
		})
	}
}

func TestIncidentKey_SameCauseSameKey(t *testing.T) {
	login500 := []report.HTTPCall{{Method: "POST", Path: "/users/login", Status: 500}}

	// Assertion berbeda di tiap test, tapi penyebabnya endpoint yang sama -> satu insiden.
	a, _, _ := IncidentKey(Normalize("Expected: 200\nReceived: 500"), login500)
	b, _, _ := IncidentKey(Normalize("Expected: 201\nReceived: 500"), login500)
	if a != b {
		t.Error("5xx di endpoint yang sama harus satu insiden")
	}

	// localhost lewat IPv4 atau IPv6 tetap satu insiden.
	v4, _, _ := IncidentKey(Normalize("connect ECONNREFUSED 127.0.0.1:8091"), nil)
	v6, _, _ := IncidentKey(Normalize("connect ECONNREFUSED ::1:8091"), nil)
	if v4 != v6 {
		t.Error("127.0.0.1 dan ::1 harus satu insiden")
	}

	// Pesan sama tapi target berbeda -> insiden berbeda.
	c, _, _ := IncidentKey(Normalize("connect ECONNREFUSED 127.0.0.1:8091"), nil)
	d, _, _ := IncidentKey(Normalize("connect ECONNREFUSED 127.0.0.1:8787"), nil)
	if c == d {
		t.Error("ECONNREFUSED ke host berbeda harus insiden berbeda")
	}

	// Error biasa yang berbeda tidak boleh digabung.
	e, _, _ := IncidentKey(Normalize("Expected: 201\nReceived: 422"), nil)
	f, _, _ := IncidentKey(Normalize("Expected: 200\nReceived: 404"), nil)
	if e == f {
		t.Error("error berbeda tidak boleh digabung")
	}
}
