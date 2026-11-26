package main

import "testing"

func TestPaasQuotaEnforce(t *testing.T) {
	cases := []struct {
		plan    string
		request paasPlanQuota
		reject  bool
	}{
		{"starter", paasPlanQuota{256, 500, 2}, false},
		{"starter", paasPlanQuota{257, 500, 2}, true},
		{"pro", paasPlanQuota{512, 1000, 10}, false},
		{"pro", paasPlanQuota{512, 1001, 10}, true},
		{"business", paasPlanQuota{1024, 2000, 25}, false},
		{"business", paasPlanQuota{1024, 2000, 26}, true},
		{"enterprise", paasPlanQuota{1, 1, 1}, true},
		{"pro", paasPlanQuota{0, 100, 1}, true},
	}
	for _, c := range cases {
		err := paasQuotaEnforce(c.plan, c.request)
		if (err != nil) != c.reject {
			t.Fatalf("%s %+v: err=%v reject=%v", c.plan, c.request, err, c.reject)
		}
	}
}
