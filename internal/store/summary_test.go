package store

import "testing"

func TestSummarize(t *testing.T) {
	cases := map[string]string{
		"Error: expect(received).toBe(expected) // Object.is equality\n\nExpected: 201\nReceived: 500":                                                                                                         "Error: expect(received).toBe(expected) // Object.is equality | Expected: 201 | Received: 500",
		"Error: expect(received).toEqual(expected) // deep equality\n\n- Expected  - 1\n+ Received  + 1\n\n  Object {\n    \"id\": Any<String>,\n-   \"status\": \"PAID\",\n+   \"status\": \"PENDING\",\n  }": `Error: expect(received).toEqual(expected) // deep equality | - "status": "PAID", | + "status": "PENDING",`,
		"Error: timeout": "Error: timeout",
	}
	for in, want := range cases {
		if got := summarize(in); got != want {
			t.Errorf("summarize:\n got  %q\n want %q", got, want)
		}
	}
}
