package main

import (
	"errors"
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
	titleRE                = regexp.MustCompile(`(?s)<title>(.*?)</title>`)
	searchCardRE           = regexp.MustCompile(`(?s)<li x-test-model\b.*?</li>`)
	searchTitleRE          = regexp.MustCompile(`(?s)<span x-test-search-response-title>(.*?)</span>`)
	searchDescRE           = regexp.MustCompile(`(?s)<p class="max-w-lg[^"]*">(.*?)</p>`)
	searchHrefRE           = regexp.MustCompile(`(?s)<a href="([^"]+)" class="group w-full">`)
	pullCountRE            = regexp.MustCompile(`(?s)<span x-test-pull-count>(.*?)</span>`)
	tagCountRE             = regexp.MustCompile(`(?s)<span x-test-tag-count>(.*?)</span>`)
	updatedRE              = regexp.MustCompile(`(?s)<span x-test-updated>(.*?)</span>`)
	updatedAgeValueRE      = regexp.MustCompile(`^(\d+)\s+(hour|day|week|month|year)s?\s+ago$`)
	nextSearchPageRE       = regexp.MustCompile(`hx-get="/search\?page=(\d+)[^"]*"`)
	modelNameRE            = regexp.MustCompile(`(?s)<a x-test-model-name[^>]*>(.*?)</a>`)
	summaryRE              = regexp.MustCompile(`(?s)<span id="summary-content">\s*(.*?)\s*</span>`)
	detailLabelLinkRE      = regexp.MustCompile(`(?s)<a href="/library/[^"]+/blobs/[^"]+">\s*(.*?)\s*</a>`)
	detailValueRE          = regexp.MustCompile(`(?s)<div class="truncate font-mono[^"]*">\s*(.*?)\s*</div>\s*<div class="hidden text-right[^"]*">\s*(.*?)\s*</div>`)
	readmeRE               = regexp.MustCompile(`(?s)<div\s+id="display"[^>]*>\s*(.*?)\s*</div>\s*</div>\s*<div id="editorContainer"`)
	tagTagsPageRowSplitRE  = regexp.MustCompile(`<div class="group px-4 py-3">`)
	tagModelPageRowSplitRE = regexp.MustCompile(`(?s)<div class="hidden group px-4 py-3[^"]*">`)
	tagCommandValueRE      = regexp.MustCompile(`(?s)<input class="command hidden" value="([^"]+:[^"]+)"`)
	tagHrefRE              = regexp.MustCompile(`href="/([^"?#]+:[^"?#]+)"`)
	tagSizeContextRE       = regexp.MustCompile(`(?s)<p class="col-span-2 text-neutral-500 text-\[13px\]">([^<]+)</p>`)
	tagInputRE             = regexp.MustCompile(`(?s)<div class="col-span-2 text-neutral-500 text-\[13px\]\s*">\s*(.*?)\s*</div>`)
	tagDigestUpdatedRE     = regexp.MustCompile(`(?s)<span class="font-mono text-\[11px\]">([^<]+)</span>&nbsp;·&nbsp;([^<]+)`)
	tagLatestBadgeRE       = regexp.MustCompile(`(?s)rounded-full[^>]*>\s*latest\s*</span>`)
	scriptRE               = regexp.MustCompile(`(?is)<script\b.*?</script>`)
	styleRE                = regexp.MustCompile(`(?is)<style\b.*?</style>`)
	liOpenRE               = regexp.MustCompile(`(?is)<li\b[^>]*>`)
	tagRE                  = regexp.MustCompile(`(?s)<[^>]+>`)
)

type searchResult struct {
	Model     string
	Path      string
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

type tagInfo struct {
	Tag     string
	Latest  bool
	Size    string
	Context string
	Input   string
	Digest  string
	Updated string
	Notes   string
}

type modelInfo struct {
	Reference     string
	Family        string
	PagePath      string
	Summary       string
	Downloads     string
	Updated       string
	Metadata      []metadataRow
	ReadmeSnippet string
}

type httpStatusError struct {
	URL        string
	StatusCode int
	Status     string
	Title      string
}

func (e *httpStatusError) Error() string {
	if e.Title != "" {
		return fmt.Sprintf("%s returned %s (%s)", e.URL, e.Status, e.Title)
	}
	return fmt.Sprintf("%s returned %s", e.URL, e.Status)
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
	sortBy := searchSortDownloads

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

	cmd.Flags().StringVar(&sortBy, "sort", searchSortDownloads, "Sort results by one of: model, tags, downloads, updated")
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
		Use:   "info <model|model:tag>",
		Short: "Show model or tag details, plus available tags",
		Long:  "Show summary, downloads, updated time, metadata, a README snippet, and available tags for a model or specific tag.",
		Args:  cobra.ExactArgs(1),
		Example: strings.TrimSpace(`
  ollama-models info qwen3.5
  ollama-models info qwen3.5:latest
  ollama-models info llama3:8b-text-q3_K_L
`),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInfo(strings.TrimSpace(args[0]))
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

	fmt.Println("Per-tag download counts are not exposed on Ollama's tags page.")
	return printTagsTable(os.Stdout, tags)
}

func printTagsTable(w io.Writer, tags []tagInfo) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "TAG\tLATEST\tSIZE\tCONTEXT\tINPUT\tUPDATED\tDIGEST\tNOTES")
	for _, tag := range tags {
		latest := ""
		if tag.Latest {
			latest = "yes"
		}
		fmt.Fprintf(
			tw,
			"%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			tag.Tag,
			latest,
			tag.Size,
			tag.Context,
			tag.Input,
			tag.Updated,
			tag.Digest,
			tag.Notes,
		)
	}
	return tw.Flush()
}

func runInfo(reference string) error {
	info, err := fetchInfo(reference)
	if err != nil {
		return err
	}

	isTagReference := strings.Contains(reference, ":")
	tagModel := reference
	if strings.Contains(reference, ":") && info.Family != "" {
		tagModel = info.Family
	}
	tags, err := fetchTags(tagModel)
	if err != nil {
		return err
	}

	fmt.Printf("Model: %s\n", info.Reference)
	if info.Family != "" {
		fmt.Printf("Family: %s\n", info.Family)
	}
	if info.PagePath != "" {
		fmt.Printf("Page: %s%s\n", baseURL, info.PagePath)
	}
	if isTagReference {
		fmt.Printf("Family downloads: %s\n", info.Downloads)
		fmt.Printf("Tag updated: %s\n", info.Updated)
	} else {
		fmt.Printf("Downloads: %s\n", info.Downloads)
		fmt.Printf("Updated: %s\n", info.Updated)
	}

	if info.Summary != "" {
		if isTagReference {
			fmt.Printf("\nFamily summary:\n%s\n", info.Summary)
		} else {
			fmt.Printf("\nSummary:\n%s\n", info.Summary)
		}
	}

	if len(info.Metadata) > 0 {
		if isTagReference {
			fmt.Printf("\nTag details:\n")
		} else {
			fmt.Printf("\nMetadata:\n")
		}
		for _, row := range info.Metadata {
			if row.Size != "" {
				fmt.Printf("- %s [%s]: %s\n", row.Name, row.Size, row.Value)
			} else {
				fmt.Printf("- %s: %s\n", row.Name, row.Value)
			}
		}
	}

	if info.ReadmeSnippet != "" {
		if isTagReference {
			fmt.Printf("\nFamily README snippet:\n%s\n", info.ReadmeSnippet)
		} else {
			fmt.Printf("\nREADME snippet:\n%s\n", info.ReadmeSnippet)
		}
	}

	if len(tags) > 0 {
		fmt.Printf("\nAvailable tags:\n")
		fmt.Println("Per-tag download counts are not exposed on Ollama's tags page.")
		if err := printTagsTable(os.Stdout, tags); err != nil {
			return err
		}
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
			Path:      cleanText(firstRaw(searchHrefRE, card)),
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

func fetchTags(model string) ([]tagInfo, error) {
	body, err := fetch(tagsPathForReference(model))
	if err != nil {
		var statusErr *httpStatusError
		if !errors.As(err, &statusErr) || statusErr.StatusCode != http.StatusNotFound {
			return nil, err
		}

		body, err = fetch(pagePathForReference(model))
		if err != nil {
			return nil, err
		}
	}

	return parseTags(body, baseModelReference(model)), nil
}

func fetchInfo(reference string) (modelInfo, error) {
	pagePath := pagePathForReference(reference)
	body, err := fetch(pagePath)
	if err != nil {
		return modelInfo{}, err
	}

	family := baseModelReference(reference)

	info := modelInfo{
		Reference: reference,
		Family:    family,
		PagePath:  pagePath,
		Summary:   normalizeSummary(firstText(summaryRE, body), reference, family),
		Downloads: firstText(pullCountRE, body),
		Updated:   firstText(updatedRE, body),
		Metadata:  parseMetadata(body),
	}

	if info.Summary == "" {
		info.Summary = normalizeSummary(firstText(titleRE, body), reference, family)
	}

	info.ReadmeSnippet = normalizeReadmeSnippet(snippet(cleanText(firstRaw(readmeRE, body)), 700))
	return info, nil
}

func parseTags(body, model string) []tagInfo {
	prefix := model + ":"
	seen := make(map[string]bool)
	tags := make([]tagInfo, 0)

	parseTagParts(tagTagsPageRowSplitRE.Split(body, -1), prefix, seen, &tags)
	if len(tags) == 0 {
		parseTagParts(tagModelPageRowSplitRE.Split(body, -1), prefix, seen, &tags)
	}

	return tags
}

func parseTagParts(parts []string, prefix string, seen map[string]bool, tags *[]tagInfo) {
	for _, part := range parts[1:] {
		full := parseTagReference(part)
		tag, ok := strings.CutPrefix(full, prefix)
		if !ok || tag == "" || seen[tag] {
			continue
		}
		seen[tag] = true

		sizeContext := tagSizeContextRE.FindAllStringSubmatch(part, -1)
		size := ""
		context := ""
		if len(sizeContext) > 0 {
			size = cleanText(sizeContext[0][1])
		}
		if len(sizeContext) > 1 {
			context = cleanText(sizeContext[1][1])
		}

		input := firstText(tagInputRE, part)
		digest := ""
		updated := ""
		if match := tagDigestUpdatedRE.FindStringSubmatch(part); len(match) == 3 {
			digest = cleanText(match[1])
			updated = cleanText(match[2])
		}

		*tags = append(*tags, tagInfo{
			Tag:     tag,
			Latest:  tagLatestBadgeRE.MatchString(part) || tag == "latest",
			Size:    size,
			Context: context,
			Input:   input,
			Digest:  digest,
			Updated: updated,
			Notes:   describeTag(tag),
		})
	}
}

func parseTagReference(part string) string {
	if value := strings.TrimSpace(html.UnescapeString(firstRaw(tagCommandValueRE, part))); value != "" {
		return strings.TrimPrefix(value, "/")
	}

	raw := strings.TrimSpace(html.UnescapeString(firstRaw(tagHrefRE, part)))
	raw = strings.TrimPrefix(raw, "/")
	raw = strings.TrimPrefix(raw, "library/")
	return raw
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
		return "", &httpStatusError{
			URL:        target,
			StatusCode: resp.StatusCode,
			Status:     resp.Status,
			Title:      title,
		}
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

func normalizeReadmeSnippet(text string) string {
	trimmed := strings.TrimSpace(text)
	if strings.EqualFold(trimmed, "No readme") {
		return ""
	}
	return trimmed
}

func normalizeSummary(summary, reference, family string) string {
	trimmed := strings.TrimSpace(summary)
	if trimmed == "" {
		return ""
	}

	summaryNorm := normalizeSearchText(trimmed)
	if summaryNorm == "" {
		return ""
	}

	if summaryNorm == normalizeSearchText(reference) || summaryNorm == normalizeSearchText(family) {
		return ""
	}

	return trimmed
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

func pagePathForReference(reference string) string {
	ref := strings.TrimSpace(strings.TrimPrefix(reference, "/"))
	if ref == "" {
		return "/library/"
	}
	if strings.HasPrefix(ref, "library/") {
		return "/" + ref
	}
	if strings.Contains(ref, "/") {
		return "/" + ref
	}
	return "/library/" + ref
}

func tagsPathForReference(reference string) string {
	return pagePathForReference(baseModelReference(reference)) + "/tags"
}

func baseModelReference(reference string) string {
	ref := strings.TrimSpace(strings.TrimPrefix(reference, "/"))
	model, _, found := strings.Cut(ref, ":")
	if found {
		return model
	}
	return ref
}

func describeTag(tag string) string {
	if tag == "latest" {
		return "Alias for the current default tag"
	}

	parts := strings.Split(tag, "-")
	notes := make([]string, 0, 4)
	for _, part := range parts {
		lower := strings.ToLower(part)
		switch {
		case lower == "instruct":
			notes = append(notes, "instruction-tuned")
		case lower == "text":
			notes = append(notes, "text/base variant")
		case lower == "vision":
			notes = append(notes, "vision-capable")
		case lower == "thinking":
			notes = append(notes, "thinking/reasoning mode")
		case lower == "tools":
			notes = append(notes, "tool-calling")
		case lower == "latest":
			notes = append(notes, "default alias")
		case isSizeToken(lower):
			notes = append(notes, "size "+part)
		case isQuantToken(part):
			notes = append(notes, "quant "+part)
		case lower == "fp16" || lower == "f16" || lower == "bf16":
			notes = append(notes, "precision "+part)
		}
	}

	if len(notes) == 0 {
		return "-"
	}
	return strings.Join(dedupeStrings(notes), ", ")
}

func isSizeToken(s string) bool {
	if len(s) < 2 {
		return false
	}
	last := s[len(s)-1]
	if last != 'b' && last != 'm' {
		return false
	}
	for _, r := range s[:len(s)-1] {
		if !unicode.IsDigit(r) && r != '.' {
			return false
		}
	}
	return true
}

func isQuantToken(s string) bool {
	lower := strings.ToLower(s)
	return strings.HasPrefix(lower, "q") || strings.HasPrefix(lower, "iq")
}

func dedupeStrings(items []string) []string {
	seen := make(map[string]bool, len(items))
	out := make([]string, 0, len(items))
	for _, item := range items {
		if seen[item] {
			continue
		}
		seen[item] = true
		out = append(out, item)
	}
	return out
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
