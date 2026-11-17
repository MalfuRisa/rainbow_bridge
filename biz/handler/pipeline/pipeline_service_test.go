package pipeline

import "testing"

func TestParsePipelineSortOrderPresence(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		present bool
		value   int
		invalid bool
	}{
		{"omitted", `{"pipeline":{"pipeline_key":"default","pipeline_name":"x"}}`, false, 0, false},
		{"zero", `{"pipeline":{"pipeline_key":"default","sort_order":0}}`, true, 0, false},
		{"positive", `{"pipeline":{"pipeline_key":"default","sort_order":20}}`, true, 20, false},
		{"negative", `{"pipeline":{"pipeline_key":"default","sort_order":-3}}`, true, -3, false},
		{"null", `{"pipeline":{"sort_order":null}}`, false, 0, true},
		{"string", `{"pipeline":{"sort_order":"20"}}`, false, 0, true},
		{"decimal", `{"pipeline":{"sort_order":2.5}}`, false, 0, true},
		{"overflow", `{"pipeline":{"sort_order":2147483648}}`, false, 0, true},
		{"invalid body", `{`, false, 0, true},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			value, err := parsePipelineSortOrder([]byte(item.body))
			if item.invalid {
				if err == nil {
					t.Fatalf("expected invalid sort_order, got %v", value)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if item.present != (value != nil) {
				t.Fatalf("present=%v want %v", value != nil, item.present)
			}
			if value != nil && *value != item.value {
				t.Fatalf("value=%d want %d", *value, item.value)
			}
		})
	}
}
