package main

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
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
	if results[0].Path != "/library/qwen3.5" {
		t.Fatalf("expected first path /library/qwen3.5, got %q", results[0].Path)
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

func TestPagePathForReference(t *testing.T) {
	tests := []struct {
		ref  string
		want string
	}{
		{ref: "qwen3.5", want: "/library/qwen3.5"},
		{ref: "qwen3.5:latest", want: "/library/qwen3.5:latest"},
		{ref: "stewartpark/qwen3.5", want: "/stewartpark/qwen3.5"},
		{ref: "stewartpark/qwen3.5:latest", want: "/stewartpark/qwen3.5:latest"},
	}

	for _, tc := range tests {
		if got := pagePathForReference(tc.ref); got != tc.want {
			t.Fatalf("pagePathForReference(%q) = %q, want %q", tc.ref, got, tc.want)
		}
	}
}

func TestTagsPathForReference(t *testing.T) {
	tests := []struct {
		ref  string
		want string
	}{
		{ref: "qwen3.5", want: "/library/qwen3.5/tags"},
		{ref: "qwen3.5:latest", want: "/library/qwen3.5/tags"},
		{ref: "stewartpark/qwen3.5", want: "/stewartpark/qwen3.5/tags"},
		{ref: "stewartpark/qwen3.5:latest", want: "/stewartpark/qwen3.5/tags"},
	}

	for _, tc := range tests {
		if got := tagsPathForReference(tc.ref); got != tc.want {
			t.Fatalf("tagsPathForReference(%q) = %q, want %q", tc.ref, got, tc.want)
		}
	}
}

func TestNormalizeReadmeSnippet(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{input: "No readme", want: ""},
		{input: " no README ", want: ""},
		{input: "Actual content", want: "Actual content"},
	}

	for _, tc := range tests {
		if got := normalizeReadmeSnippet(tc.input); got != tc.want {
			t.Fatalf("normalizeReadmeSnippet(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestFetchTagsFallsBackToModelPage(t *testing.T) {
	previousClient := httpClient
	defer func() { httpClient = previousClient }()

	httpClient = &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			switch req.URL.String() {
			case "https://ollama.com/stewartpark/qwen3.5/tags":
				return stringResponse(req, http.StatusNotFound, `<title>Ollama</title>`)
			case "https://ollama.com/stewartpark/qwen3.5":
				return stringResponse(req, http.StatusOK, `
<div class="group px-4 py-3">
  <a href="/stewartpark/qwen3.5:latest" class="group-hover:underline">stewartpark/qwen3.5:latest</a>
  <span class="ml-2 inline-flex items-center rounded-full px-2 py-px text-xs font-medium border border-blue-500 text-blue-600">latest</span>
  <p class="col-span-2 text-neutral-500 text-[13px]">16GB</p>
  <p class="col-span-2 text-neutral-500 text-[13px]">128K</p>
  <div class="col-span-2 text-neutral-500 text-[13px] ">Text</div>
  <div class="flex text-neutral-500 text-xs items-center">
    <span class="font-mono text-[11px]">abcdef123456</span>&nbsp;·&nbsp;7 hours ago
  </div>
</div>`)
			default:
				return nil, fmt.Errorf("unexpected URL: %s", req.URL.String())
			}
		}),
	}

	tags, err := fetchTags("stewartpark/qwen3.5")
	if err != nil {
		t.Fatalf("fetchTags returned error: %v", err)
	}
	if len(tags) != 1 {
		t.Fatalf("expected 1 tag, got %d", len(tags))
	}
	if tags[0].Tag != "latest" {
		t.Fatalf("expected latest tag, got %q", tags[0].Tag)
	}
	if !tags[0].Latest {
		t.Fatalf("expected latest badge to be true")
	}
	if tags[0].Digest != "abcdef123456" || tags[0].Updated != "7 hours ago" {
		t.Fatalf("unexpected tag metadata: %#v", tags[0])
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func stringResponse(req *http.Request, status int, body string) (*http.Response, error) {
	return &http.Response{
		StatusCode: status,
		Status:     fmt.Sprintf("%d %s", status, http.StatusText(status)),
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    req,
	}, nil
}
