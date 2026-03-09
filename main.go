package main

import (
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"
	"unicode"

	"github.com/spf13/cobra"
)

const baseURL = "https://ollama.com"

var httpClient = &http.Client{
	Timeout: 20 * time.Second,
}

var (
	titleRE           = regexp.MustCompile(`(?s)<title>(.*?)</title>`)
	searchCardRE      = regexp.MustCompile(`(?s)<li x-test-model\b.*?</li>`)
	searchTitleRE     = regexp.MustCompile(`(?s)<span x-test-search-response-title>(.*?)</span>`)
	searchDescRE      = regexp.MustCompile(`(?s)<p class="max-w-lg[^"]*">(.*?)</p>`)
	pullCountRE       = regexp.MustCompile(`(?s)<span x-test-pull-count>(.*?)</span>`)
	tagCountRE        = regexp.MustCompile(`(?s)<span x-test-tag-count>(.*?)</span>`)
	updatedRE         = regexp.MustCompile(`(?s)<span x-test-updated>(.*?)</span>`)
	updatedAgeValueRE = regexp.MustCompile(`^(\d+)\s+(hour|day|week|month|year)s?\s+ago$`)
	nextSearchPageRE  = regexp.MustCompile(`hx-get="/search\?page=(\d+)[^"]*"`)
	modelNameRE       = regexp.MustCompile(`(?s)<a x-test-model-name[^>]*>(.*?)</a>`)
	summaryRE         = regexp.MustCompile(`(?s)<span id="summary-content">\s*(.*?)\s*</span>`)
	tagHrefRE         = regexp.MustCompile(`href="/library/([^"/?#]+:[^"/?#]+)"`)
	detailLabelLinkRE = regexp.MustCompile(`(?s)<a href="/library/[^"]+/blobs/[^"]+">\s*(.*?)\s*</a>`)
	detailValueRE     = regexp.MustCompile(`(?s)<div class="truncate font-mono[^"]*">\s*(.*?)\s*</div>\s*<div class="hidden text-right[^"]*">\s*(.*?)\s*</div>`)
	readmeRE          = regexp.MustCompile(`(?s)<div\s+id="display"[^>]*>\s*(.*?)\s*</div>\s*</div>\s*<div id="editorContainer"`)
	scriptRE          = regexp.MustCompile(`(?is)<script\b.*?</script>`)
	styleRE           = regexp.MustCompile(`(?is)<style\b.*?</style>`)
	liOpenRE          = regexp.MustCompile(`(?is)<li\b[^>]*>`)
	tagRE             = regexp.MustCompile(`(?s)<[^>]+>`)
)

type searchResult struct {
	Model     string
	Summary   string
	Downloads string
	Tags      string
	Updated   string
}

const (
	searchSortModel     = "model"
	searchSortTags      = "tags"
	searchSortDownloads = "downloads"
	searchSortUpdated   = "updated"
)

type metadataRow struct {
	Name  string
	Value string
	Size  string
}

type modelInfo struct {
	Reference     string
	Family        string
	Summary       string
	Downloads     string
	Updated       string
	Metadata      []metadataRow
	ReadmeSnippet string
}

func main() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func newRootCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "ollama-models",
		Short:         "Search Ollama library models over HTTP",
		SilenceUsage:  true,
		SilenceErrors: true,
		Long: strings.TrimSpace(`
Search Ollama library models over HTTP.

The CLI only queries official Ollama library pages:
  /search?q=...
  /library/<model>/tags
  /library/<model:tag>
`),
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}

	cmd.AddCommand(newSearchCmd())
	cmd.AddCommand(newTagsCmd())
	cmd.AddCommand(newInfoCmd())
	return cmd
}

func newSearchCmd() *cobra.Command {
	var sortBy string

	cmd := &cobra.Command{
		Use:   "search <query>",
		Short: "List model families from official Ollama search results",
		Long:  "List model families whose names match the query from official Ollama search results.",
		Args:  cobra.MinimumNArgs(1),
		Example: strings.TrimSpace(`
  ollama-models search qwen3.5
  ollama-models search llama 3
  ollama-models search qwen3.5 --sort downloads
`),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !isValidSearchSort(sortBy) {
				return fmt.Errorf("invalid sort %q (expected one of: %s, %s, %s, %s)", sortBy, searchSortModel, searchSortTags, searchSortDownloads, searchSortUpdated)
			}
			return runSearch(strings.TrimSpace(strings.Join(args, " ")), sortBy)
		},
	}

	cmd.Flags().StringVar(&sortBy, "sort", "", "Sort results by one of: model, tags, downloads, updated")
	return cmd
}

func newTagsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "tags <model>",
		Short: "List available tags for a model",
		Long:  "List available tags for a model from /library/<model>/tags.",
		Args:  cobra.ExactArgs(1),
		Example: strings.TrimSpace(`
  ollama-models tags qwen3.5
  ollama-models tags llama3.3
`),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runTags(strings.TrimSpace(args[0]))
		},
	}
}

func newInfoCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "info <model:tag>",
		Short: "Show summary, downloads, updated time, metadata, and README snippet",
		Long:  "Show summary, downloads, updated time, metadata, and a README snippet for a model tag.",
		Args: cobra.ExactArgs(1),
		Example: strings.TrimSpace(`
  ollama-models info qwen3.5:latest
  ollama-models info llama3:8b-text-q3_K_L
`),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref := strings.TrimSpace(args[0])
			if !strings.Contains(ref, ":") {
				return fmt.Errorf("info requires a model:tag reference")
			}
			return runInfo(ref)
		},
	}
}

func runSearch(query, sortBy string) error {
	results, err := searchModels(query)
	if err != nil {
		return err
	}
	if len(results) == 0 {
		return fmt.Errorf("no matches found for %q", query)
	}

	sortSearchResults(results, sortBy)

	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "MODEL\tTAGS\tDOWNLOADS\tUPDATED\tSUMMARY")
	for _, result := range results {
		fmt.Fprintf(
			tw,
			"%s\t%s\t%s\t%s\t%s\n",
			result.Model,
			result.Tags,
			result.Downloads,
			result.Updated,
			result.Summary,
		)
	}
	return tw.Flush()
}

func runTags(model string) error {
	tags, err := fetchTags(model)
	if err != nil {
		return err
	}
	if len(tags) == 0 {
		return fmt.Errorf("no tags found for %q", model)
	}
	for _, tag := range tags {
		fmt.Println(tag)
	}
	return nil
}

func runInfo(reference string) error {
	info, err := fetchInfo(reference)
	if err != nil {
		return err
	}

	fmt.Printf("Model: %s\n", info.Reference)
	if info.Family != "" {
		fmt.Printf("Family: %s\n", info.Family)
	}
	fmt.Printf("Summary: %s\n", info.Summary)
	fmt.Printf("Downloads: %s\n", info.Downloads)
	fmt.Printf("Updated: %s\n", info.Updated)

	if len(info.Metadata) > 0 {
		fmt.Printf("\nMetadata:\n")
		for _, row := range info.Metadata {
			if row.Size != "" {
				fmt.Printf("- %s [%s]: %s\n", row.Name, row.Size, row.Value)
			} else {
				fmt.Printf("- %s: %s\n", row.Name, row.Value)
			}
		}
	}

	if info.ReadmeSnippet != "" {
		fmt.Printf("\nREADME snippet:\n%s\n", info.ReadmeSnippet)
	}

	return nil
}

func searchModels(query string) ([]searchResult, error) {
	seen := make(map[string]bool)
	results := make([]searchResult, 0)
	page := 1

	for {
		path := "/search?q=" + url.QueryEscape(query)
		if page > 1 {
			path = fmt.Sprintf("/search?page=%d&q=%s", page, url.QueryEscape(query))
		}

		body, err := fetch(path)
		if err != nil {
			return nil, err
		}

		for _, result := range parseSearchResults(body) {
			if result.Model == "" || seen[result.Model] || !matchesQuery(result.Model, query) {
				continue
			}
			seen[result.Model] = true
			results = append(results, result)
		}

		nextPage := findNextSearchPage(body, page)
		if nextPage == 0 || nextPage <= page {
			break
		}
		page = nextPage
	}

	return results, nil
}

func parseSearchResults(body string) []searchResult {
	cards := searchCardRE.FindAllString(body, -1)
	results := make([]searchResult, 0, len(cards))

	for _, card := range cards {
		result := searchResult{
			Model:     firstText(searchTitleRE, card),
			Summary:   firstText(searchDescRE, card),
			Downloads: firstText(pullCountRE, card),
			Tags:      firstText(tagCountRE, card),
			Updated:   firstText(updatedRE, card),
		}
		if result.Model == "" {
			continue
		}
		results = append(results, result)
	}

	return results
}

func findNextSearchPage(body string, currentPage int) int {
	next := 0
	for _, match := range nextSearchPageRE.FindAllStringSubmatch(body, -1) {
		page, err := strconv.Atoi(match[1])
		if err != nil {
			continue
		}
		if page <= currentPage {
			continue
		}
		if next == 0 || page < next {
			next = page
		}
	}
	return next
}

func fetchTags(model string) ([]string, error) {
	body, err := fetch("/library/" + model + "/tags")
	if err != nil {
		return nil, err
	}

	prefix := model + ":"
	seen := make(map[string]bool)
	tags := make([]string, 0)

	for _, match := range tagHrefRE.FindAllStringSubmatch(body, -1) {
		full := html.UnescapeString(match[1])
		tag, ok := strings.CutPrefix(full, prefix)
		if !ok || tag == "" || seen[tag] {
			continue
		}
		seen[tag] = true
		tags = append(tags, tag)
	}

	return tags, nil
}

func fetchInfo(reference string) (modelInfo, error) {
	body, err := fetch("/library/" + reference)
	if err != nil {
		return modelInfo{}, err
	}

	family := firstText(modelNameRE, body)
	if family == "" {
		family, _, _ = strings.Cut(reference, ":")
	}

	info := modelInfo{
		Reference: reference,
		Family:    family,
		Summary:   firstText(summaryRE, body),
		Downloads: firstText(pullCountRE, body),
		Updated:   firstText(updatedRE, body),
		Metadata:  parseMetadata(body),
	}

	if info.Summary == "" {
		info.Summary = firstText(titleRE, body)
	}

	info.ReadmeSnippet = snippet(cleanText(firstRaw(readmeRE, body)), 700)
	return info, nil
}

func parseMetadata(body string) []metadataRow {
	section := between(body, `<section id="file-explorer"`, `<div class="flex flex-1 flex-col py-8" id="readme">`)
	if section == "" {
		return nil
	}

	parts := strings.Split(section, `<div class="group block grid-cols-12 gap-2 px-4 py-3 sm:grid sm:grid-cols-12">`)
	rows := make([]metadataRow, 0, len(parts))

	for _, part := range parts[1:] {
		label := firstText(detailLabelLinkRE, part)
		if label == "" {
			continue
		}

		var value, size string
		if match := detailValueRE.FindStringSubmatch(part); len(match) == 3 {
			value = cleanText(match[1])
			size = cleanText(match[2])
		}

		rows = append(rows, metadataRow{
			Name:  label,
			Value: value,
			Size:  size,
		})
	}

	return rows
}

func fetch(path string) (string, error) {
	target := path
	if !strings.HasPrefix(target, "http://") && !strings.HasPrefix(target, "https://") {
		target = baseURL + path
	}

	req, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "ollama-models/0.1")
	req.Header.Set("Accept", "text/html,application/xhtml+xml")

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return "", err
	}
	body := string(bodyBytes)

	if resp.StatusCode != http.StatusOK {
		title := firstText(titleRE, body)
		if title != "" {
			return "", fmt.Errorf("%s returned %s (%s)", target, resp.Status, title)
		}
		return "", fmt.Errorf("%s returned %s", target, resp.Status)
	}

	return body, nil
}

func firstRaw(re *regexp.Regexp, input string) string {
	match := re.FindStringSubmatch(input)
	if len(match) < 2 {
		return ""
	}
	return match[1]
}

func firstText(re *regexp.Regexp, input string) string {
	return cleanText(firstRaw(re, input))
}

func between(input, start, end string) string {
	startIdx := strings.Index(input, start)
	if startIdx < 0 {
		return ""
	}
	rest := input[startIdx:]
	endIdx := strings.Index(rest, end)
	if endIdx < 0 {
		return rest
	}
	return rest[:endIdx]
}

func cleanText(input string) string {
	if input == "" {
		return ""
	}

	s := scriptRE.ReplaceAllString(input, " ")
	s = styleRE.ReplaceAllString(s, " ")

	replacer := strings.NewReplacer(
		"<br>", "\n",
		"<br/>", "\n",
		"<br />", "\n",
		"</p>", "\n\n",
		"</div>", "\n",
		"</section>", "\n",
		"</article>", "\n",
		"</h1>", "\n\n",
		"</h2>", "\n\n",
		"</h3>", "\n\n",
		"</h4>", "\n\n",
		"</h5>", "\n\n",
		"</h6>", "\n\n",
		"</pre>", "\n\n",
		"</code>", "\n",
		"</li>", "\n",
		"</ul>", "\n",
		"</ol>", "\n",
		"</tr>", "\n",
		"</td>", "\t",
		"</th>", "\t",
	)
	s = replacer.Replace(s)
	s = liOpenRE.ReplaceAllString(s, "- ")
	s = tagRE.ReplaceAllString(s, "")
	s = html.UnescapeString(s)
	s = strings.ReplaceAll(s, "\u00a0", " ")

	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	lastBlank := false

	for _, line := range lines {
		line = strings.Join(strings.Fields(line), " ")
		if line == "" {
			if !lastBlank {
				out = append(out, "")
				lastBlank = true
			}
			continue
		}
		out = append(out, line)
		lastBlank = false
	}

	return strings.TrimSpace(strings.Join(out, "\n"))
}

func snippet(text string, max int) string {
	if len(text) <= max {
		return text
	}

	cut := strings.LastIndex(text[:max], "\n")
	if cut < max/2 {
		cut = strings.LastIndex(text[:max], " ")
	}
	if cut < 0 {
		cut = max
	}

	return strings.TrimSpace(text[:cut]) + "..."
}

func matchesQuery(model, query string) bool {
	modelNorm := normalizeSearchText(model)
	queryNorm := normalizeSearchText(query)
	if modelNorm == "" || queryNorm == "" {
		return false
	}
	return strings.Contains(modelNorm, queryNorm)
}

func normalizeSearchText(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func isValidSearchSort(sortBy string) bool {
	switch sortBy {
	case "", searchSortModel, searchSortTags, searchSortDownloads, searchSortUpdated:
		return true
	default:
		return false
	}
}

func sortSearchResults(results []searchResult, sortBy string) {
	if sortBy == "" {
		return
	}

	now := time.Now()
	sort.SliceStable(results, func(i, j int) bool {
		left := results[i]
		right := results[j]

		switch sortBy {
		case searchSortModel:
			return compareStringsAsc(left.Model, right.Model)
		case searchSortTags:
			lt := parsePlainInt(left.Tags)
			rt := parsePlainInt(right.Tags)
			if lt == rt {
				return compareStringsAsc(left.Model, right.Model)
			}
			return lt < rt
		case searchSortDownloads:
			ld := parseMetricCount(left.Downloads)
			rd := parseMetricCount(right.Downloads)
			if ld == rd {
				return compareStringsAsc(left.Model, right.Model)
			}
			return ld > rd
		case searchSortUpdated:
			lu := parseUpdatedUnix(left.Updated, now)
			ru := parseUpdatedUnix(right.Updated, now)
			if lu == ru {
				return compareStringsAsc(left.Model, right.Model)
			}
			return lu > ru
		default:
			return false
		}
	})
}

func compareStringsAsc(left, right string) bool {
	ll := strings.ToLower(left)
	rl := strings.ToLower(right)
	if ll == rl {
		return left < right
	}
	return ll < rl
}

func parsePlainInt(s string) int64 {
	s = strings.TrimSpace(strings.ReplaceAll(s, ",", ""))
	if s == "" {
		return 0
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0
	}
	return n
}

func parseMetricCount(s string) float64 {
	s = strings.TrimSpace(strings.ReplaceAll(strings.ToUpper(s), ",", ""))
	if s == "" {
		return 0
	}

	multiplier := 1.0
	switch {
	case strings.HasSuffix(s, "K"):
		multiplier = 1_000
		s = strings.TrimSuffix(s, "K")
	case strings.HasSuffix(s, "M"):
		multiplier = 1_000_000
		s = strings.TrimSuffix(s, "M")
	case strings.HasSuffix(s, "B"):
		multiplier = 1_000_000_000
		s = strings.TrimSuffix(s, "B")
	}

	value, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return value * multiplier
}

func parseUpdatedUnix(s string, now time.Time) int64 {
	s = strings.ToLower(strings.TrimSpace(s))
	switch s {
	case "", "unknown":
		return 0
	case "just now", "today":
		return now.Unix()
	case "yesterday":
		return now.Add(-24 * time.Hour).Unix()
	}

	match := updatedAgeValueRE.FindStringSubmatch(s)
	if len(match) != 3 {
		return 0
	}

	amount, err := strconv.Atoi(match[1])
	if err != nil {
		return 0
	}

	var age time.Duration
	switch match[2] {
	case "hour":
		age = time.Duration(amount) * time.Hour
	case "day":
		age = time.Duration(amount) * 24 * time.Hour
	case "week":
		age = time.Duration(amount) * 7 * 24 * time.Hour
	case "month":
		age = time.Duration(amount) * 30 * 24 * time.Hour
	case "year":
		age = time.Duration(amount) * 365 * 24 * time.Hour
	default:
		return 0
	}

	return now.Add(-age).Unix()
}
