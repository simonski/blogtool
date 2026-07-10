package main

// editor.go - the in-browser writing mode (`blog editor`) and its
// authentication (`blog reset-password`).
//
// Auth model: a single "admin" user stored in a SQLite database (.blog.db)
// next to the blog sources. The password can ONLY be set from the terminal
// via `blog reset-password` — the editor has no change-password surface at
// all. Passwords are stored as PBKDF2-SHA256 (600k iterations) with a random
// per-password salt; verification uses a constant-time comparison. Sessions
// are random 256-bit tokens held in memory and handed to the browser as
// HttpOnly SameSite=Strict cookies, so they vanish when the editor stops.
//
// SQLite is modernc.org/sqlite (pure Go): the release cross-compiles for
// darwin/linux × arm64/amd64 without CGO, which rules out the C driver.

import (
	"bufio"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"html"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

const (
	authDBName        = ".blog.db"
	adminUsername     = "admin"
	pbkdf2Iterations  = 600_000
	pbkdf2KeyLength   = 32
	minPasswordLength = 12
	sessionCookie     = "blog_session"
	sessionTTL        = 24 * time.Hour
)

// ---------------------------------------------------------------------------
// credential storage

func openAuthDB() (*sql.DB, error) {
	db, err := sql.Open("sqlite", authDBName)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", authDBName, err)
	}
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS users (
		username   TEXT PRIMARY KEY,
		salt       BLOB NOT NULL,
		hash       BLOB NOT NULL,
		iterations INTEGER NOT NULL,
		updated_at TEXT NOT NULL
	)`)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("initialise %s: %w", authDBName, err)
	}
	return db, nil
}

func hashPassword(password string, salt []byte, iterations int) ([]byte, error) {
	return pbkdf2.Key(sha256.New, password, salt, iterations, pbkdf2KeyLength)
}

// verifyAdminPassword checks a login attempt against the stored admin
// credentials in constant time. A missing admin row is an error, not a
// mismatch, so the editor can tell the operator to run `blog reset-password`.
func verifyAdminPassword(db *sql.DB, password string) (bool, error) {
	var salt, storedHash []byte
	var iterations int
	row := db.QueryRow(`SELECT salt, hash, iterations FROM users WHERE username = ?`, adminUsername)
	if err := row.Scan(&salt, &storedHash, &iterations); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, errors.New("no admin password set; run `blog reset-password` first")
		}
		return false, fmt.Errorf("read credentials: %w", err)
	}
	attempt, err := hashPassword(password, salt, iterations)
	if err != nil {
		return false, err
	}
	return subtle.ConstantTimeCompare(attempt, storedHash) == 1, nil
}

// ---------------------------------------------------------------------------
// reset-password

var stdinReader = bufio.NewReader(os.Stdin)

// readPassword reads one line from stdin, disabling terminal echo while the
// password is typed. When stdin is not a terminal (tests, pipes) it reads
// plainly — the security property is that the password is only settable from
// the machine running the command, never through the editor's web surface.
func readPassword(prompt string) (string, error) {
	fmt.Print(prompt)
	interactive := false
	if info, err := os.Stdin.Stat(); err == nil && info.Mode()&os.ModeCharDevice != 0 {
		interactive = sttyEcho(false) == nil
	}
	line, err := stdinReader.ReadString('\n')
	if interactive {
		sttyEcho(true)
		fmt.Println()
	}
	if err != nil && line == "" {
		return "", fmt.Errorf("read password: %w", err)
	}
	return strings.TrimRight(line, "\r\n"), nil
}

func sttyEcho(on bool) error {
	mode := "-echo"
	if on {
		mode = "echo"
	}
	cmd := exec.Command("stty", mode)
	cmd.Stdin = os.Stdin
	return cmd.Run()
}

func cmdResetPassword(args []string) error {
	if len(args) > 0 {
		return fmt.Errorf("unexpected argument %q", args[0])
	}

	password, err := readPassword("new admin password: ")
	if err != nil {
		return err
	}
	if len(password) < minPasswordLength {
		return fmt.Errorf("password must be at least %d characters", minPasswordLength)
	}
	confirm, err := readPassword("confirm password: ")
	if err != nil {
		return err
	}
	if password != confirm {
		return errors.New("passwords do not match")
	}

	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return fmt.Errorf("generate salt: %w", err)
	}
	hashed, err := hashPassword(password, salt, pbkdf2Iterations)
	if err != nil {
		return err
	}

	db, err := openAuthDB()
	if err != nil {
		return err
	}
	defer db.Close()

	_, err = db.Exec(`INSERT INTO users (username, salt, hash, iterations, updated_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(username) DO UPDATE SET
			salt = excluded.salt,
			hash = excluded.hash,
			iterations = excluded.iterations,
			updated_at = excluded.updated_at`,
		adminUsername, salt, hashed, pbkdf2Iterations, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return fmt.Errorf("store credentials: %w", err)
	}

	fmt.Printf("admin password updated in %s\n", authDBName)
	return nil
}

// ---------------------------------------------------------------------------
// sessions

type sessionStore struct {
	mu     sync.Mutex
	tokens map[string]time.Time // token -> expiry
}

func newSessionStore() *sessionStore {
	return &sessionStore{tokens: map[string]time.Time{}}
}

func (s *sessionStore) create() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate session token: %w", err)
	}
	token := hex.EncodeToString(raw)
	s.mu.Lock()
	defer s.mu.Unlock()
	for t, expiry := range s.tokens {
		if time.Now().After(expiry) {
			delete(s.tokens, t)
		}
	}
	s.tokens[token] = time.Now().Add(sessionTTL)
	return token, nil
}

func (s *sessionStore) valid(token string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	expiry, ok := s.tokens[token]
	if !ok {
		return false
	}
	if time.Now().After(expiry) {
		delete(s.tokens, token)
		return false
	}
	return true
}

func (s *sessionStore) revoke(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.tokens, token)
}

// ---------------------------------------------------------------------------
// editor server

type editorServer struct {
	db       *sql.DB
	sessions *sessionStore
}

func cmdEditor(args []string) error {
	fs := flag.NewFlagSet("editor", flag.ContinueOnError)
	port := fs.Int("port", 8001, "port to listen on")
	host := fs.String("host", "localhost", "interface to bind")
	if err := fs.Parse(args); err != nil {
		return err
	}

	db, err := openAuthDB()
	if err != nil {
		return err
	}
	defer db.Close()

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM users WHERE username = ?`, adminUsername).Scan(&count); err != nil {
		return fmt.Errorf("read credentials: %w", err)
	}
	if count == 0 {
		return errors.New("no admin password set; run `blog reset-password` first")
	}

	// The editor previews with drafts included; the public `blog build` (and
	// deploy, which builds first) never includes them.
	if err := buildSite(true); err != nil {
		return err
	}

	srv := &editorServer{db: db, sessions: newSessionStore()}

	mux := http.NewServeMux()
	mux.HandleFunc("/login", srv.handleLogin)
	mux.HandleFunc("/logout", srv.handleLogout)
	mux.HandleFunc("/", srv.auth(srv.handleHome))
	mux.HandleFunc("/new", srv.auth(srv.handleNew))
	mux.HandleFunc("/edit", srv.auth(srv.handleEdit))
	mux.HandleFunc("/save", srv.auth(srv.handleSave))
	mux.HandleFunc("/publish", srv.auth(srv.handlePublish))
	mux.HandleFunc("/unpublish", srv.auth(srv.handleUnpublish))
	mux.HandleFunc("/site/", srv.auth(func(w http.ResponseWriter, r *http.Request) {
		http.StripPrefix("/site/", http.FileServer(http.Dir(outputDir))).ServeHTTP(w, r)
	}))

	addr := fmt.Sprintf("%s:%d", *host, *port)
	fmt.Printf("editor: http://%s (log in as admin; drafts are included in previews)\n", addr)
	return http.ListenAndServe(addr, mux)
}

func (s *editorServer) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(sessionCookie)
		if err != nil || !s.sessions.valid(cookie.Value) {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		next(w, r)
	}
}

func (s *editorServer) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		password := r.FormValue("password")
		ok, err := verifyAdminPassword(s.db, password)
		if err != nil {
			editorError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if !ok {
			// Blunt the rate of online guessing on top of the 600k-iteration
			// hash cost.
			time.Sleep(500 * time.Millisecond)
			writeEditorPage(w, "login", loginForm("wrong password"))
			return
		}
		token, err := s.sessions.create()
		if err != nil {
			editorError(w, http.StatusInternalServerError, err.Error())
			return
		}
		http.SetCookie(w, &http.Cookie{
			Name:     sessionCookie,
			Value:    token,
			Path:     "/",
			HttpOnly: true,
			SameSite: http.SameSiteStrictMode,
			MaxAge:   int(sessionTTL.Seconds()),
		})
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	writeEditorPage(w, "login", loginForm(""))
}

func (s *editorServer) handleLogout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(sessionCookie); err == nil {
		s.sessions.revoke(cookie.Value)
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   -1,
	})
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (s *editorServer) handleHome(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	entries, err := collectEntries()
	if err != nil {
		editorError(w, http.StatusInternalServerError, err.Error())
		return
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Updated.After(entries[j].Updated)
	})

	var b strings.Builder
	b.WriteString(`<p class="actions"><form method="post" action="/new">` +
		`<input type="text" name="title" placeholder="title" required> ` +
		`<button name="kind" value="post">new post</button> ` +
		`<button name="kind" value="idea">new idea</button>` +
		`</form></p>`)

	section := func(heading string, draft bool) {
		var rows []string
		for _, e := range entries {
			if e.IsDraft != draft || e.Source == "" {
				continue
			}
			rows = append(rows, fmt.Sprintf(
				`<tr><td>%d</td><td>%s</td><td><a href="/edit?src=%s">%s</a></td><td><a href="/site/%s/">view</a></td></tr>`,
				e.ID, e.Kind, html.EscapeString(e.Source), html.EscapeString(e.Title), html.EscapeString(entrySlug(e))))
		}
		if len(rows) == 0 {
			return
		}
		fmt.Fprintf(&b, "<h2>%s</h2><table>%s</table>", heading, strings.Join(rows, ""))
	}
	section("drafts", true)
	section("published", false)

	b.WriteString(`<p><a href="/site/">preview site</a> · <form method="post" action="/logout" class="inline"><button>log out</button></form></p>`)
	writeEditorPage(w, "editor", b.String())
}

func (s *editorServer) handleNew(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	kind := r.FormValue("kind")
	if kind != "post" && kind != "idea" {
		editorError(w, http.StatusBadRequest, "kind must be post or idea")
		return
	}
	destDir, _, err := createEntry(kind, r.FormValue("title"), true)
	if err != nil {
		editorError(w, http.StatusBadRequest, err.Error())
		return
	}
	mdPath, err := findSingleMarkdown(destDir)
	if err != nil || mdPath == "" {
		editorError(w, http.StatusInternalServerError, "created entry has no markdown source")
		return
	}
	if err := buildSite(true); err != nil {
		editorError(w, http.StatusInternalServerError, err.Error())
		return
	}
	http.Redirect(w, r, "/edit?src="+urlQueryEscape(mdPath), http.StatusSeeOther)
}

func (s *editorServer) handleEdit(w http.ResponseWriter, r *http.Request) {
	src, err := validEntrySource(r.FormValue("src"))
	if err != nil {
		editorError(w, http.StatusBadRequest, err.Error())
		return
	}
	data, err := os.ReadFile(src)
	if err != nil {
		editorError(w, http.StatusNotFound, err.Error())
		return
	}
	meta, _ := parseFrontMatter(string(data))
	isDraft := meta["draft"] == "true"

	publishAction, publishLabel := "/publish", "publish"
	if !isDraft {
		publishAction, publishLabel = "/unpublish", "unpublish"
	}
	status := "published"
	if isDraft {
		status = "draft"
	}

	body := fmt.Sprintf(`<p><a href="/">&larr; back</a> · %s · <a href="/site/%s/">preview</a></p>
<form method="post" action="/save">
<input type="hidden" name="src" value="%s">
<textarea name="content" spellcheck="true">%s</textarea>
<p><button>save</button></p>
</form>
<form method="post" action="%s">
<input type="hidden" name="src" value="%s">
<p><button>%s</button></p>
</form>`,
		status,
		html.EscapeString(sourceSlug(src)),
		html.EscapeString(src),
		html.EscapeString(string(data)),
		publishAction,
		html.EscapeString(src),
		publishLabel)
	writeEditorPage(w, "edit", body)
}

func (s *editorServer) handleSave(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	src, err := validEntrySource(r.FormValue("src"))
	if err != nil {
		editorError(w, http.StatusBadRequest, err.Error())
		return
	}
	content := strings.ReplaceAll(r.FormValue("content"), "\r\n", "\n")
	if err := os.WriteFile(src, []byte(content), 0o644); err != nil {
		editorError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := buildSite(true); err != nil {
		editorError(w, http.StatusInternalServerError, err.Error())
		return
	}
	http.Redirect(w, r, "/edit?src="+urlQueryEscape(src), http.StatusSeeOther)
}

func (s *editorServer) handlePublish(w http.ResponseWriter, r *http.Request) {
	s.setDraftFlag(w, r, false)
}

func (s *editorServer) handleUnpublish(w http.ResponseWriter, r *http.Request) {
	s.setDraftFlag(w, r, true)
}

func (s *editorServer) setDraftFlag(w http.ResponseWriter, r *http.Request, draft bool) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	src, err := validEntrySource(r.FormValue("src"))
	if err != nil {
		editorError(w, http.StatusBadRequest, err.Error())
		return
	}
	data, err := os.ReadFile(src)
	if err != nil {
		editorError(w, http.StatusNotFound, err.Error())
		return
	}
	var updated string
	if draft {
		updated = setMetadataInSource(string(data), "draft", "true")
	} else {
		updated = removeMetadataFromSource(string(data), "draft")
	}
	if err := os.WriteFile(src, []byte(updated), 0o644); err != nil {
		editorError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := buildSite(true); err != nil {
		editorError(w, http.StatusInternalServerError, err.Error())
		return
	}
	http.Redirect(w, r, "/edit?src="+urlQueryEscape(src), http.StatusSeeOther)
}

// validEntrySource confines editor file access to markdown sources under
// posts/, ideas/ or drafts/ — no absolute paths, no traversal outside the
// blog workspace.
func validEntrySource(src string) (string, error) {
	if src == "" {
		return "", errors.New("missing src")
	}
	clean := filepath.Clean(filepath.FromSlash(src))
	if filepath.IsAbs(clean) {
		return "", errors.New("invalid source path")
	}
	parts := strings.Split(filepath.ToSlash(clean), "/")
	if len(parts) < 2 || (parts[0] != postsDir && parts[0] != ideasDir && parts[0] != draftsDir) {
		return "", errors.New("invalid source path")
	}
	for _, part := range parts {
		if part == ".." {
			return "", errors.New("invalid source path")
		}
	}
	if !strings.HasSuffix(strings.ToLower(clean), ".md") {
		return "", errors.New("not a markdown source")
	}
	info, err := os.Stat(clean)
	if err != nil || !info.Mode().IsRegular() {
		return "", fmt.Errorf("no such entry source: %s", clean)
	}
	return clean, nil
}

// entrySlug maps a source entry to its path under output/.
func entrySlug(e entry) string {
	slug := sanitizeSlug(filepath.Base(e.Dir))
	if e.Kind == "idea" {
		return "ideas/" + slug
	}
	return slug
}

// sourceSlug maps a markdown source path to its path under output/.
func sourceSlug(src string) string {
	parts := strings.Split(filepath.ToSlash(src), "/")
	if len(parts) < 2 {
		return ""
	}
	if parts[0] == draftsDir && len(parts) == 2 {
		// loose drafts/foo.md files render at output/foo/
		return sanitizeSlug(strings.TrimSuffix(parts[1], filepath.Ext(parts[1])))
	}
	slug := sanitizeSlug(parts[1])
	if parts[0] == ideasDir {
		return "ideas/" + slug
	}
	return slug
}

func urlQueryEscape(s string) string {
	replacer := strings.NewReplacer("%", "%25", "&", "%26", "+", "%2B", "#", "%23", "?", "%3F", " ", "%20")
	return replacer.Replace(filepath.ToSlash(s))
}

// ---------------------------------------------------------------------------
// editor HTML

func loginForm(errMsg string) string {
	msg := ""
	if errMsg != "" {
		msg = fmt.Sprintf(`<p class="error">%s</p>`, html.EscapeString(errMsg))
	}
	return msg + `<form method="post" action="/login">
<p><input type="password" name="password" placeholder="admin password" autofocus required></p>
<p><button>log in</button></p>
</form>`
}

func editorError(w http.ResponseWriter, code int, msg string) {
	w.WriteHeader(code)
	writeEditorPage(w, "error", fmt.Sprintf(`<p class="error">%s</p><p><a href="/">&larr; back</a></p>`, html.EscapeString(msg)))
}

func writeEditorPage(w http.ResponseWriter, title string, body string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<!DOCTYPE html>
<html>
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>blog %s</title>
<style>
body { font-family: monospace; background: #fff; color: #222; margin: 0 auto; padding: 2em; max-width: 960px; }
h1, h2 { color: #007acc; }
a { color: #5a9ab5; }
table { border-collapse: collapse; }
td { padding: 0.2em 1em 0.2em 0; }
textarea { width: 100%%; height: 28em; font-family: monospace; font-size: 1em; padding: 0.5em; box-sizing: border-box; }
input[type=text], input[type=password] { font-family: monospace; padding: 0.3em; width: 20em; }
button { font-family: monospace; padding: 0.3em 0.8em; }
form.inline { display: inline; }
.error { color: #b00; }
</style>
</head>
<body>
<h1>blog %s</h1>
%s
</body>
</html>`, html.EscapeString(title), html.EscapeString(title), body)
}
