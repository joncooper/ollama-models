package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestParseSearchResults(t *testing.T) {
	html := `
<ul>
  <li x-test-model class="flex items-baseline border-b border-neutral-200 py-6">
    <a href="/library/qwen3.5" class="group w-full">
      <div class="flex flex-col mb-1" title="qwen3.5">
        <h2><span x-test-search-response-title>qwen3.5</span></h2>
        <p class="max-w-lg break-words text-neutral-800 text-md">Official Qwen 3.5 family.</p>
      </div>
      <p>
        <span x-test-pull-count>1M</span>
        <span x-test-tag-count>30</span>
        <span x-test-updated>6 days ago</span>
      </p>
    </a>
  </li>
  <li x-test-model class="flex items-baseline border-b border-neutral-200 py-6">
    <a href="/library/qwen2.5" class="group w-full">
      <div class="flex flex-col mb-1" title="qwen2.5">
        <h2><span x-test-search-response-title>qwen2.5</span></h2>
        <p class="max-w-lg break-words text-neutral-800 text-md">Older Qwen family.</p>
      </div>
      <p>
        <span x-test-pull-count>500K</span>
        <span x-test-tag-count>12</span>
        <span x-test-updated>2 months ago</span>
      </p>
    </a>
  </li>
</ul>`

	results := parseSearchResults(html)
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	if results[0].Model != "qwen3.5" {
		t.Fatalf("expected first model qwen3.5, got %q", results[0].Model)
	}
	if results[0].Summary != "Official Qwen 3.5 family." {
		t.Fatalf("unexpected summary: %q", results[0].Summary)
	}
	if results[0].Downloads != "1M" || results[0].Tags != "30" || results[0].Updated != "6 days ago" {
		t.Fatalf("unexpected first result: %#v", results[0])
	}
}

func TestMatchesQuery(t *testing.T) {
	tests := []struct {
		model string
		query string
		want  bool
	}{
		{model: "qwen3.5", query: "qwen3.5", want: true},
		{model: "free01/Qwen3.5-9B", query: "qwen3.5", want: true},
		{model: "kiwi_kiwi/qwen3.5_a", query: "qwen3.5", want: true},
		{model: "qwen2.5", query: "qwen3.5", want: false},
		{model: "jaahas/crow", query: "qwen3.5", want: false},
	}

	for _, tc := range tests {
		got := matchesQuery(tc.model, tc.query)
		if got != tc.want {
			t.Fatalf("matchesQuery(%q, %q) = %v, want %v", tc.model, tc.query, got, tc.want)
		}
	}
}

func TestFindNextSearchPage(t *testing.T) {
	body := `<li hx-get="/search?page=2&q=qwen3.5"></li><li hx-get="/search?page=3&q=qwen3.5"></li>`
	if got := findNextSearchPage(body, 1); got != 2 {
		t.Fatalf("expected next page 2, got %d", got)
	}
	if got := findNextSearchPage(body, 3); got != 0 {
		t.Fatalf("expected no next page, got %d", got)
	}
}

func TestRootHelp(t *testing.T) {
	cmd := newRootCmd()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}

	out := stdout.String()
	if !strings.Contains(out, "Search Ollama library models over HTTP") {
		t.Fatalf("expected root help description, got %q", out)
	}
	if !strings.Contains(out, "search") || !strings.Contains(out, "tags") || !strings.Contains(out, "info") {
		t.Fatalf("expected subcommands in help output, got %q", out)
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output, got %q", stderr.String())
	}
}

func TestSearchHelp(t *testing.T) {
	cmd := newRootCmd()
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{"search", "--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}

	out := stdout.String()
	if !strings.Contains(out, "List model families") {
		t.Fatalf("expected search help text, got %q", out)
	}
	if !strings.Contains(out, "ollama-models search qwen3.5") {
		t.Fatalf("expected search example, got %q", out)
	}
}
