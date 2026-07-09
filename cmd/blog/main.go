package main

import (
	"bufio"
	"bytes"
	"embed"
	"errors"
	"flag"
	"fmt"
	"html"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	rendererhtml "github.com/yuin/goldmark/renderer/html"
)

const (
	postsDir         = "posts"
	ideasDir         = "ideas"
	draftsDir        = "drafts"
	outputDir        = "output"
	templatesDir     = "templates"
	siteTemplatesDir = templatesDir + "/site"
	indexCSSName     = "index.css"
	postCSSName      = "post.css"
	indexFileName    = "index.html"
)

// Embedded copy of the templates tree so `blog init` (and template fallback)
// works from an installed binary, away from this repository.
//
//go:embed all:templates
var embeddedTemplates embed.FS

// Embedded so `blog version` and the release process share one source of
// truth; bumped by `make bump-version` during a release.
//
//go:embed VERSION
var embeddedVersion string

var (
	mdLinkPattern  = regexp.MustCompile(`!?\[[^\]]*\]\(([^)]+)\)`)
	datePattern    = regexp.MustCompile(`\b\d{4}-\d{2}-\d{2}\b`)
	idPrefixOnly   = regexp.MustCompile(`^(\d+)_`)
	nonSlugPattern = regexp.MustCompile(`[^a-z0-9]+`)
	mdEngine       = goldmark.New(
		goldmark.WithExtensions(extension.GFM),
		goldmark.WithRendererOptions(rendererhtml.WithUnsafe()),
	)
)

type templates struct {
	IndexHeader string
	IndexFooter string
	Header      map[string]string // per kind: "post", "idea"
	Footer      map[string]string
}

type post struct {
	Title      string
	DateRaw    string
	Date       time.Time
	Body       string
	Slug       string
	SourcePath string
	Kind       string // "post" or "idea"
	IsDraft    bool
}

// entry is a source item under posts/ or ideas/, used by ls and edit.
type entry struct {
	ID      int
	Kind    string
	Dir     string
	Title   string
	Updated time.Time
	Source  string
}

func main() {
	if len(os.Args) < 2 {
		usage()
		return
	}

	cmd := os.Args[1]
	args := os.Args[2:]

	var err error
	switch cmd {
	case "init":
		err = cmdInit(args)
	case "build":
		err = cmdBuild(args)
	case "server", "serve":
		err = cmdServer(args)
	case "post":
		err = cmdNew("post", args)
	case "idea":
		err = cmdNew("idea", args)
	case "ls":
		err = cmdList(args)
	case "edit":
		err = cmdEdit(args)
	case "version", "-v", "--version":
		fmt.Println(strings.TrimSpace(embeddedVersion))
	case "help", "-h", "--help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "blog: unknown command %q\n\n", cmd)
		usage()
		os.Exit(1)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "blog %s: %v\n", cmd, err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Print(`blog - a static blog tool

usage:

  blog                     show this usage
  blog init                create a new "blog" folder (fails if it already exists)
  blog build [-draft]      build the static site into output/
  blog server [-dir D] [-port P]
                           serve static content (default: current directory, port 8000)
  blog post ["the title"]  create a new post under posts/{id}_{title}/
  blog idea ["the title"]  create a new idea under ideas/{id}_{title}/
  blog ls                  list posts and ideas, most recent first
  blog edit N              open the source content for entry N in VS Code
  blog version             print the blog version
`)
}

// ---------------------------------------------------------------------------
// init

func cmdInit(args []string) error {
	if len(args) > 0 {
		return fmt.Errorf("unexpected argument %q", args[0])
	}

	target := "blog"
	if _, err := os.Stat(target); err == nil {
		return fmt.Errorf("folder %q already exists", target)
	}

	for _, dir := range []string{postsDir, ideasDir, draftsDir} {
		if err := os.MkdirAll(filepath.Join(target, dir), 0o755); err != nil {
			return fmt.Errorf("create %s: %w", dir, err)
		}
	}

	err := fs.WalkDir(embeddedTemplates, templatesDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		dest := filepath.Join(target, filepath.FromSlash(path))
		if d.IsDir() {
			return os.MkdirAll(dest, 0o755)
		}
		data, readErr := embeddedTemplates.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		return os.WriteFile(dest, data, 0o644)
	})
	if err != nil {
		return fmt.Errorf("write templates: %w", err)
	}

	makefile := `.DEFAULT_GOAL := help

DEPLOY_TARGET ?= blog.simonski.com:./blog/

.PHONY: help build run deploy

help: ## Show available commands
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS=":.*?## "}; {printf "  %-8s %s\n", $$1, $$2}'

build: ## Build the static site into output/
	blog build

run: ## Serve output/ on http://localhost:8000
	blog server -dir output

deploy: build ## Deploy output/ via rsync
	rsync -avz --delete output/ $(DEPLOY_TARGET)
`
	if err := os.WriteFile(filepath.Join(target, "Makefile"), []byte(makefile), 0o644); err != nil {
		return fmt.Errorf("write Makefile: %w", err)
	}
	if err := os.WriteFile(filepath.Join(target, ".gitignore"), []byte("output/\n"), 0o644); err != nil {
		return fmt.Errorf("write .gitignore: %w", err)
	}

	fmt.Printf("created %s/\n\nnext steps:\n  cd %s\n  blog post \"my first post\"\n  blog build\n  blog server -dir output\n", target, target)
	return nil
}

// ---------------------------------------------------------------------------
// post / idea creation

func cmdNew(kind string, args []string) error {
	title := strings.TrimSpace(strings.Join(args, " "))
	if title == "" {
		fmt.Print("title: ")
		reader := bufio.NewReader(os.Stdin)
		line, err := reader.ReadString('\n')
		if err != nil && line == "" {
			return fmt.Errorf("read title: %w", err)
		}
		title = strings.TrimSpace(line)
	}
	if title == "" {
		return errors.New("a title is required")
	}

	slug := compressTitle(title)
	if slug == "" {
		return fmt.Errorf("title %q produces an empty folder name", title)
	}

	baseDir := kindDir(kind)
	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", baseDir, err)
	}

	if existing, err := findExisting(baseDir, slug); err != nil {
		return err
	} else if existing != "" {
		return fmt.Errorf("%s %q already exists: %s", kind, title, existing)
	}

	id, err := nextID()
	if err != nil {
		return err
	}

	destDir := filepath.Join(baseDir, fmt.Sprintf("%d_%s", id, slug))
	tokens := map[string]string{
		"{{TITLE}}": title,
		"{{DATE}}":  time.Now().Format("2006-01-02"),
		"{{ID}}":    strconv.Itoa(id),
		"{{SLUG}}":  slug,
	}

	if err := scaffold(kind, destDir, tokens); err != nil {
		// Do not leave a half-created entry behind.
		os.RemoveAll(destDir)
		return err
	}

	fmt.Printf("created %s %d: %s\n", kind, id, destDir)
	return nil
}

func kindDir(kind string) string {
	if kind == "idea" {
		return ideasDir
	}
	return postsDir
}

// compressTitle turns "The Title!" into "the_title".
func compressTitle(title string) string {
	slug := nonSlugPattern.ReplaceAllString(strings.ToLower(title), "_")
	return strings.Trim(slug, "_")
}

// findExisting reports a folder in dir whose name (minus any id prefix)
// matches slug case-insensitively.
func findExisting(dir string, slug string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", fmt.Errorf("read directory %s: %w", dir, err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := idPrefixOnly.ReplaceAllString(e.Name(), "")
		if strings.EqualFold(name, slug) {
			return filepath.Join(dir, e.Name()), nil
		}
	}
	return "", nil
}

// nextID scans posts/ and ideas/ for {id}_ prefixed folders and returns max+1.
func nextID() (int, error) {
	max := 0
	for _, dir := range []string{postsDir, ideasDir} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return 0, fmt.Errorf("read directory %s: %w", dir, err)
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			if m := idPrefixOnly.FindStringSubmatch(e.Name()); m != nil {
				if id, convErr := strconv.Atoi(m[1]); convErr == nil && id > max {
					max = id
				}
			}
		}
	}
	return max + 1, nil
}

// scaffold copies the template files for kind into destDir, replacing tokens
// in text files. Files and folders starting with "_" are render partials
// (e.g. _header.html) and are not copied.
func scaffold(kind string, destDir string, tokens map[string]string) error {
	src, root, err := scaffoldSource(kind)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", destDir, err)
	}

	return fs.WalkDir(src, root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if strings.HasPrefix(d.Name(), "_") {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		rel, relErr := filepath.Rel(root, filepath.FromSlash(path))
		if relErr != nil {
			return relErr
		}
		dest := filepath.Join(destDir, rel)
		if d.IsDir() {
			return os.MkdirAll(dest, 0o755)
		}

		data, readErr := fs.ReadFile(src, path)
		if readErr != nil {
			return fmt.Errorf("read template %s: %w", path, readErr)
		}
		if isTextTemplate(d.Name()) {
			content := string(data)
			for token, value := range tokens {
				content = strings.ReplaceAll(content, token, value)
			}
			data = []byte(content)
		}
		return os.WriteFile(dest, data, 0o644)
	})
}

// scaffoldSource prefers on-disk templates, falling back to the embedded copy.
func scaffoldSource(kind string) (fs.FS, string, error) {
	root := templatesDir + "/" + kindDir(kind)
	if info, err := os.Stat(filepath.FromSlash(root)); err == nil && info.IsDir() {
		return os.DirFS("."), root, nil
	}
	if _, err := fs.Stat(embeddedTemplates, root); err == nil {
		return embeddedTemplates, root, nil
	}
	return nil, "", fmt.Errorf("no templates found for %s (looked in %s)", kind, root)
}

func isTextTemplate(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".md", ".html", ".css", ".txt", ".js", ".json", ".xml", ".yaml", ".yml":
		return true
	default:
		return false
	}
}

// ---------------------------------------------------------------------------
// ls / edit

func cmdList(args []string) error {
	entries, err := collectEntries()
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		fmt.Println("no posts or ideas yet")
		return nil
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Updated.After(entries[j].Updated)
	})

	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tTYPE\tUPDATED\tTITLE")
	for _, e := range entries {
		id := "-"
		if e.ID > 0 {
			id = strconv.Itoa(e.ID)
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", id, e.Kind, e.Updated.Format("2006-01-02 15:04"), e.Title)
	}
	return w.Flush()
}

func collectEntries() ([]entry, error) {
	var entries []entry
	for _, kd := range []struct{ kind, dir string }{{"post", postsDir}, {"idea", ideasDir}} {
		dirEntries, err := os.ReadDir(kd.dir)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, fmt.Errorf("read directory %s: %w", kd.dir, err)
		}
		for _, de := range dirEntries {
			if !de.IsDir() {
				continue
			}
			e := entry{Kind: kd.kind, Dir: filepath.Join(kd.dir, de.Name()), Title: de.Name()}
			if m := idPrefixOnly.FindStringSubmatch(de.Name()); m != nil {
				e.ID, _ = strconv.Atoi(m[1])
			}
			if info, statErr := os.Stat(e.Dir); statErr == nil {
				e.Updated = info.ModTime()
			}

			mdPath, mdErr := findSingleMarkdown(e.Dir)
			if mdErr == nil && mdPath != "" {
				e.Source = mdPath
				if info, statErr := os.Stat(mdPath); statErr == nil {
					e.Updated = info.ModTime()
				}
				if parsed, parseErr := parsePost(mdPath, de.Name(), kd.kind, false); parseErr == nil && parsed.Title != "" {
					e.Title = parsed.Title
				}
			}
			entries = append(entries, e)
		}
	}
	return entries, nil
}

func cmdEdit(args []string) error {
	if len(args) != 1 {
		return errors.New("usage: blog edit N")
	}
	id, err := strconv.Atoi(args[0])
	if err != nil || id <= 0 {
		return fmt.Errorf("invalid id %q", args[0])
	}

	entries, err := collectEntries()
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.ID != id {
			continue
		}
		target := e.Source
		if target == "" {
			target = e.Dir
		}
		fmt.Printf("opening %s\n", target)
		cmd := exec.Command("code", target)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if runErr := cmd.Run(); runErr != nil {
			return fmt.Errorf("launch vscode (is `code` on your PATH?): %w", runErr)
		}
		return nil
	}
	return fmt.Errorf("no post or idea with id %d (see `blog ls`)", id)
}

// ---------------------------------------------------------------------------
// server

func cmdServer(args []string) error {
	fs := flag.NewFlagSet("server", flag.ContinueOnError)
	dir := fs.String("dir", ".", "directory to serve")
	port := fs.Int("port", 8000, "port to listen on")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if info, err := os.Stat(*dir); err != nil || !info.IsDir() {
		return fmt.Errorf("not a directory: %s", *dir)
	}

	addr := fmt.Sprintf("localhost:%d", *port)
	fmt.Printf("serving %s on http://%s\n", *dir, addr)
	return http.ListenAndServe(addr, http.FileServer(http.Dir(*dir)))
}

// ---------------------------------------------------------------------------
// build

func cmdBuild(args []string) error {
	fs := flag.NewFlagSet("build", flag.ContinueOnError)
	includeDrafts := fs.Bool("draft", false, "include posts from drafts")
	if err := fs.Parse(args); err != nil {
		return err
	}
	return buildSite(*includeDrafts)
}

func buildSite(includeDrafts bool) error {
	tmpl, err := loadTemplates()
	if err != nil {
		return err
	}

	if err := resetOutputDir(); err != nil {
		return err
	}
	if err := copyStylesheets(); err != nil {
		return err
	}

	posts, err := collectPosts(postsDir, "post", false)
	if err != nil {
		return err
	}
	if includeDrafts {
		drafts, draftErr := collectPosts(draftsDir, "post", true)
		if draftErr != nil {
			return draftErr
		}
		posts = append(posts, drafts...)
	}

	ideas, err := collectPosts(ideasDir, "idea", false)
	if err != nil {
		return err
	}
	for i := range ideas {
		ideas[i].Slug = "ideas/" + ideas[i].Slug
	}

	all := append(append([]post(nil), posts...), ideas...)
	assignUniqueSlugs(all)
	posts = all[:len(posts)]
	ideas = all[len(posts):]

	for _, p := range all {
		if err := renderPost(p, tmpl); err != nil {
			return err
		}
	}

	sortPosts(posts)
	sortPosts(ideas)
	if err := renderIndex(posts, ideas, tmpl); err != nil {
		return err
	}

	return nil
}

func loadTemplates() (templates, error) {
	indexHeader, err := readTemplate(siteTemplatesDir + "/index_header.html")
	if err != nil {
		return templates{}, err
	}
	indexFooter, err := readTemplate(siteTemplatesDir + "/index_footer.html")
	if err != nil {
		return templates{}, err
	}

	tmpl := templates{
		IndexHeader: indexHeader,
		IndexFooter: indexFooter,
		Header:      map[string]string{},
		Footer:      map[string]string{},
	}

	for kind, dir := range map[string]string{"post": postsDir, "idea": ideasDir} {
		header, headerErr := readTemplate(templatesDir + "/" + dir + "/_header.html")
		if headerErr != nil {
			return templates{}, headerErr
		}
		footer, footerErr := readTemplate(templatesDir + "/" + dir + "/_footer.html")
		if footerErr != nil {
			return templates{}, footerErr
		}
		tmpl.Header[kind] = header
		tmpl.Footer[kind] = footer
	}

	return tmpl, nil
}

// readTemplate reads a template by slash-separated path, preferring the disk
// copy and falling back to the embedded one.
func readTemplate(path string) (string, error) {
	if data, err := os.ReadFile(filepath.FromSlash(path)); err == nil {
		return string(data), nil
	}
	if data, err := embeddedTemplates.ReadFile(path); err == nil {
		return string(data), nil
	}
	return "", fmt.Errorf("read template %s: not found on disk or embedded", path)
}

func resetOutputDir() error {
	if err := os.RemoveAll(outputDir); err != nil {
		return fmt.Errorf("reset output directory: %w", err)
	}
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}
	return nil
}

func copyStylesheets() error {
	for _, name := range []string{indexCSSName, postCSSName} {
		content, err := readTemplate(siteTemplatesDir + "/" + name)
		if err != nil {
			return err
		}
		dst := filepath.Join(outputDir, name)
		if err := os.WriteFile(dst, []byte(content), 0o644); err != nil {
			return fmt.Errorf("write %s: %w", dst, err)
		}
	}
	return nil
}

func collectPosts(root string, kind string, isDraft bool) ([]post, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read directory %s: %w", root, err)
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})

	var posts []post
	for _, entry := range entries {
		if entry.IsDir() {
			postPath, postErr := findSingleMarkdown(filepath.Join(root, entry.Name()))
			if postErr != nil {
				return nil, postErr
			}
			if postPath == "" {
				continue
			}

			parsed, parseErr := parsePost(postPath, entry.Name(), kind, isDraft)
			if parseErr != nil {
				return nil, parseErr
			}
			posts = append(posts, parsed)
			continue
		}

		if isDraft && strings.HasSuffix(strings.ToLower(entry.Name()), ".md") {
			sourcePath := filepath.Join(root, entry.Name())
			slug := strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))
			parsed, parseErr := parsePost(sourcePath, slug, kind, true)
			if parseErr != nil {
				return nil, parseErr
			}
			posts = append(posts, parsed)
		}
	}

	return posts, nil
}

func findSingleMarkdown(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("read post directory %s: %w", dir, err)
	}

	var matches []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if strings.HasSuffix(strings.ToLower(entry.Name()), ".md") {
			matches = append(matches, filepath.Join(dir, entry.Name()))
		}
	}

	switch len(matches) {
	case 0:
		return "", nil
	case 1:
		return matches[0], nil
	default:
		sort.Strings(matches)
		return "", fmt.Errorf("expected one markdown file in %s, found %d", dir, len(matches))
	}
}

func parsePost(path string, slug string, kind string, isDraft bool) (post, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return post{}, fmt.Errorf("read markdown %s: %w", path, err)
	}

	title, dateRaw, body := parseFrontMatter(string(data))
	if title == "" {
		title = inferTitle(body, path)
	}
	if dateRaw == "" {
		dateRaw = inferDate(string(data))
	}

	parsedDate := parseDate(dateRaw)

	return post{
		Title:      title,
		DateRaw:    dateRaw,
		Date:       parsedDate,
		Body:       body,
		Slug:       sanitizeSlug(slug),
		SourcePath: path,
		Kind:       kind,
		IsDraft:    isDraft,
	}, nil
}

func parseFrontMatter(markdown string) (title string, date string, body string) {
	content := strings.ReplaceAll(markdown, "\r\n", "\n")
	content = strings.TrimPrefix(content, "\uFEFF")
	lines := strings.Split(content, "\n")

	start := 0
	for start < len(lines) && strings.TrimSpace(lines[start]) == "" {
		start++
	}
	if start >= len(lines) {
		return "", "", ""
	}

	metadata := map[string]string{}
	bodyStart := start

	if strings.TrimSpace(lines[start]) == "---" {
		bodyStart = start + 1
		for i := start + 1; i < len(lines); i++ {
			line := strings.TrimSpace(lines[i])
			if line == "" || line == "---" {
				bodyStart = i + 1
				break
			}
			key, value, ok := parseMetadataLine(lines[i])
			if !ok {
				continue
			}
			metadata[key] = value
			bodyStart = i + 1
		}
	} else {
		key, value, ok := parseMetadataLine(lines[start])
		if !ok || (key != "title" && key != "date") {
			return "", "", content
		}
		metadata[key] = value
		bodyStart = start + 1
		for i := start + 1; i < len(lines); i++ {
			line := strings.TrimSpace(lines[i])
			if line == "" {
				bodyStart = i + 1
				break
			}
			key, value, ok := parseMetadataLine(lines[i])
			if !ok {
				bodyStart = i
				break
			}
			metadata[key] = value
			bodyStart = i + 1
		}
	}

	bodyLines := append([]string(nil), lines[bodyStart:]...)
	for len(bodyLines) > 0 && isBodySeparatorLine(bodyLines[0]) {
		bodyLines = bodyLines[1:]
	}
	for len(bodyLines) > 0 && strings.TrimSpace(bodyLines[len(bodyLines)-1]) == "" {
		bodyLines = bodyLines[:len(bodyLines)-1]
	}
	if len(bodyLines) > 0 && strings.TrimSpace(bodyLines[len(bodyLines)-1]) == "---" {
		bodyLines = bodyLines[:len(bodyLines)-1]
	}

	return metadata["title"], metadata["date"], strings.Join(bodyLines, "\n")
}

func parseMetadataLine(line string) (key string, value string, ok bool) {
	rawKey, rawValue, found := strings.Cut(line, ":")
	if !found {
		return "", "", false
	}

	key = strings.ToLower(strings.TrimSpace(rawKey))
	value = strings.TrimSpace(rawValue)
	if key == "" {
		return "", "", false
	}

	for _, r := range key {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' && r != '_' {
			return "", "", false
		}
	}

	return key, value, true
}

func isBodySeparatorLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	if len(trimmed) < 3 {
		return false
	}
	for _, r := range trimmed {
		if r != '-' {
			return false
		}
	}
	return true
}

func inferTitle(markdown string, path string) string {
	lines := strings.Split(strings.ReplaceAll(markdown, "\r\n", "\n"), "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "# ") {
			return strings.TrimSpace(strings.TrimPrefix(trimmed, "# "))
		}
	}
	base := filepath.Base(path)
	return strings.TrimSuffix(base, filepath.Ext(base))
}

func inferDate(markdown string) string {
	match := datePattern.FindString(markdown)
	return match
}

// parseDate parses a post date, accepting full (2006-01-02), month (2006-01),
// and year (2006) precision. Returns the zero time if none match.
func parseDate(raw string) time.Time {
	raw = strings.TrimSpace(raw)
	for _, layout := range []string{"2006-01-02", "2006-01", "2006"} {
		if t, err := time.Parse(layout, raw); err == nil {
			return t
		}
	}
	return time.Time{}
}

func sanitizeSlug(slug string) string {
	slug = strings.TrimSpace(slug)
	slug = strings.ReplaceAll(slug, " ", "-")
	slug = strings.Trim(slug, "/")
	if slug == "" {
		return "post"
	}
	return slug
}

func assignUniqueSlugs(posts []post) {
	seen := map[string]int{}
	for i := range posts {
		original := posts[i].Slug
		count := seen[original]
		if count == 0 {
			seen[original] = 1
			continue
		}
		count++
		seen[original] = count
		posts[i].Slug = fmt.Sprintf("%s-%d", original, count)
	}
}

func renderPost(p post, tmpl templates) error {
	var rendered bytes.Buffer
	if err := mdEngine.Convert([]byte(p.Body), &rendered); err != nil {
		return fmt.Errorf("render markdown for %s: %w", p.SourcePath, err)
	}

	postDir := filepath.Join(outputDir, filepath.FromSlash(p.Slug))
	if err := os.MkdirAll(postDir, 0o755); err != nil {
		return fmt.Errorf("create post output directory %s: %w", postDir, err)
	}

	if err := copyReferencedImages(p.Body, filepath.Dir(p.SourcePath), postDir); err != nil {
		return err
	}

	relToRoot, err := filepath.Rel(postDir, outputDir)
	if err != nil {
		return fmt.Errorf("resolve css path for %s: %w", p.Slug, err)
	}
	cssHref := postCSSName
	if relToRoot != "." {
		cssHref = filepath.ToSlash(filepath.Join(relToRoot, postCSSName))
	}

	header := rewriteIndexLinks(tmpl.Header[p.Kind], relToRoot)
	footer := rewriteIndexLinks(tmpl.Footer[p.Kind], relToRoot)

	page := fmt.Sprintf(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>%s</title>
  <link rel="stylesheet" href="%s">
</head>
<body>
%s
%s
%s
</body>
</html>
`, html.EscapeString(p.Title), cssHref, header, rendered.String(), footer)

	postPath := filepath.Join(postDir, indexFileName)
	if err := os.WriteFile(postPath, []byte(page), 0o644); err != nil {
		return fmt.Errorf("write post page %s: %w", postPath, err)
	}

	return nil
}

func rewriteIndexLinks(content string, relToRoot string) string {
	if relToRoot == "." {
		return content
	}
	target := filepath.ToSlash(filepath.Join(relToRoot, indexFileName))
	content = strings.ReplaceAll(content, `href="index.html"`, fmt.Sprintf(`href="%s"`, target))
	content = strings.ReplaceAll(content, `href='index.html'`, fmt.Sprintf(`href='%s'`, target))
	return content
}

func copyReferencedImages(markdown string, srcBase string, destBase string) error {
	matches := mdLinkPattern.FindAllStringSubmatch(markdown, -1)
	seen := map[string]struct{}{}

	for _, match := range matches {
		if len(match) < 2 {
			continue
		}
		normalized := normalizeLinkTarget(match[1])
		if normalized == "" || !isLocalImagePath(normalized) {
			continue
		}
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}

		sourcePath := filepath.Join(srcBase, filepath.FromSlash(normalized))
		if err := validateRelativePath(normalized); err != nil {
			continue
		}
		if _, err := os.Stat(sourcePath); err != nil {
			continue
		}

		destPath := filepath.Join(destBase, filepath.FromSlash(normalized))
		if err := copyFile(sourcePath, destPath); err != nil {
			return err
		}
	}

	return nil
}

func normalizeLinkTarget(raw string) string {
	target := strings.TrimSpace(raw)
	if target == "" {
		return ""
	}

	parts := strings.Fields(target)
	if len(parts) == 0 {
		return ""
	}
	target = parts[0]
	target = strings.TrimPrefix(target, "<")
	target = strings.TrimSuffix(target, ">")

	if idx := strings.IndexAny(target, "?#"); idx >= 0 {
		target = target[:idx]
	}

	lower := strings.ToLower(target)
	if strings.HasPrefix(lower, "http://") ||
		strings.HasPrefix(lower, "https://") ||
		strings.HasPrefix(lower, "data:") ||
		strings.HasPrefix(lower, "mailto:") ||
		strings.HasPrefix(lower, "#") ||
		strings.HasPrefix(lower, "/") {
		return ""
	}

	return target
}

func validateRelativePath(path string) error {
	clean := filepath.Clean(filepath.FromSlash(path))
	if clean == "." || clean == ".." {
		return errors.New("invalid image path")
	}
	if strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return errors.New("image path escapes source")
	}
	return nil
}

func isLocalImagePath(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".png", ".jpg", ".jpeg", ".gif", ".svg", ".webp", ".bmp", ".ico", ".avif":
		return true
	default:
		return false
	}
}

func sortPosts(posts []post) {
	sort.Slice(posts, func(i, j int) bool {
		if !posts[i].Date.Equal(posts[j].Date) {
			return posts[i].Date.After(posts[j].Date)
		}
		return posts[i].Title > posts[j].Title
	})
}

// seasonLabel returns the northern-hemisphere meteorological season for t,
// e.g. "winter 2025". Winter (Dec-Feb) is labelled by its December year, so
// Dec 2025, Jan 2026 and Feb 2026 all belong to "winter 2025".
func seasonLabel(t time.Time) string {
	if t.IsZero() {
		return "undated"
	}
	year := t.Year()
	switch t.Month() {
	case time.December:
		return fmt.Sprintf("winter %d", year)
	case time.January, time.February:
		return fmt.Sprintf("winter %d", year-1)
	case time.March, time.April, time.May:
		return fmt.Sprintf("spring %d", year)
	case time.June, time.July, time.August:
		return fmt.Sprintf("summer %d", year)
	default: // September, October, November
		return fmt.Sprintf("autumn %d", year)
	}
}

func renderIndex(posts []post, ideas []post, tmpl templates) error {
	var links strings.Builder
	currentSeason := ""
	open := false
	for _, p := range posts {
		season := seasonLabel(p.Date)
		if season != currentSeason {
			if open {
				links.WriteString("  </ul>\n</div>\n")
			}
			links.WriteString(fmt.Sprintf("<div class=\"season\">\n  <span class=\"season-date\">%s</span>\n  <ul>\n", html.EscapeString(season)))
			currentSeason = season
			open = true
		}
		links.WriteString(indexLink(p))
	}
	if open {
		links.WriteString("  </ul>\n</div>\n")
	}

	if len(ideas) > 0 {
		links.WriteString("<div class=\"season\">\n  <span class=\"season-date\">ideas</span>\n  <ul>\n")
		for _, p := range ideas {
			links.WriteString(indexLink(p))
		}
		links.WriteString("  </ul>\n</div>\n")
	}

	page := fmt.Sprintf(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Blog</title>
  <link rel="stylesheet" href="%s">
</head>
<body>
%s
%s
%s
</body>
</html>
`, indexCSSName, tmpl.IndexHeader, links.String(), tmpl.IndexFooter)

	indexPath := filepath.Join(outputDir, indexFileName)
	if err := os.WriteFile(indexPath, []byte(page), 0o644); err != nil {
		return fmt.Errorf("write index page: %w", err)
	}
	return nil
}

func indexLink(p post) string {
	label := p.Title
	if p.IsDraft {
		label = "[DRAFT] " + label
	}
	href := filepath.ToSlash(filepath.Join(p.Slug, ""))
	return fmt.Sprintf("    <li><a href=\"%s\">%s</a></li>\n", href, html.EscapeString(label))
}

func copyFile(src string, dst string) error {
	input, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open %s: %w", src, err)
	}
	defer input.Close()

	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("create directory for %s: %w", dst, err)
	}

	output, err := os.Create(dst)
	if err != nil {
		return fmt.Errorf("create %s: %w", dst, err)
	}
	defer output.Close()

	if _, err := io.Copy(output, input); err != nil {
		return fmt.Errorf("copy %s to %s: %w", src, dst, err)
	}

	if err := output.Sync(); err != nil {
		return fmt.Errorf("flush %s: %w", dst, err)
	}

	return nil
}
