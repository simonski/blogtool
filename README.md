# blogtool

A single-binary static blog tool. The binary is called `blog`.  This was vibed. YMMV.

Found a bug or have an idea? Please [open a GitHub issue](https://github.com/simonski/blogtool/issues)
— see [CONTRIBUTING.md](CONTRIBUTING.md).

## Install

Via Homebrew (from the [simonski/homebrew-tap](https://github.com/simonski/homebrew-tap) tap):

```bash
brew install simonski/tap/blog
```

Or from source:

```bash
make install    # go install ./cmd/blog -> `blog` on your PATH
# or
make build      # -> bin/blog
```

## Usage

```
blog                     show usage
blog init                create a new "blog" folder (fails if it already exists)
blog build [-draft]      build the static site into output/
blog serve [-dir D] [-port P]
                         serve the built site from output/ (port 8000)
blog live [-port P] [-draft]
                         design-time loop: serve output/, rebuild on save, auto-reload the browser
blog post ["the title"]  create a new post under posts/{id}_{title}/
blog idea ["the title"]  create a new idea under ideas/{id}_{title}/
blog ls                  list posts and ideas, most recent first
blog edit N              open the source content for entry N in VS Code
blog label N a,b         set the labels on post or idea N (replaces existing labels)
blog upgrade             refresh templates/site assets from this binary's embedded copies
blog editor [-port P]    log in and write, edit and publish posts in the browser
blog reset-password      set the editor admin password (terminal only)
blog version             print the blog version
```

The site has two top-level pages with a shared navigation bar: `index.html`
(the blog — posts grouped by season) and `ideas.html` (the free-form ideas
list). The site assets that drive this (`index.css`, `post.css`,
`search.js`, `nav.js`) live in `templates/site/` in each blog workspace; after installing
a newer `blog` binary, run `blog upgrade` inside a workspace to bring them up
to date (HTML templates and scaffolds are never touched — they carry per-blog
customisation).

Every build also emits `output/atom.xml`, an Atom feed of all posts and ideas
with their labels as categories and full rendered content. On any generated
page, pressing Shift twice quickly opens a search popup that uses the feed as
its client-side index — searching titles, labels and body text with no server
component, so the deployed site stays fully static. The site is also keyboard
navigable: on the list pages left/right switches between blog and ideas,
up/down moves a highlight through the entries and enter opens the highlighted
one; on a post or idea, esc goes back to its list page. `blog live` gives a
design-time loop: it serves `output/`, rebuilds when a source file is saved,
and auto-reloads the browser over Server-Sent Events (the reload script is
injected at serve time only and never written into `output/`).

`blog editor` (default port 8001) is a logged-in, in-browser writing mode:
create posts and ideas, edit them as markdown, preview the site with drafts
included, and publish when ready. New entries start with `draft: true` in
their front matter, which keeps them out of public builds (`blog build`,
deploys) until published; publishing simply removes the flag. Auth is a
single `admin` user in a SQLite database (`.blog.db`, gitignored) — passwords
are PBKDF2-SHA256 (600k iterations, per-password salt), sessions are random
tokens in `HttpOnly`/`SameSite=Strict` cookies, and the password can only be
set from the terminal with `blog reset-password` (minimum 12 characters); the
editor itself has no password-change surface.

A blog workspace (as created by `blog init`) looks like:

```
posts/                   blog posts, one folder per post
ideas/                   ideas, one folder per idea
drafts/                  drafts, included in the build with `blog build -draft`
templates/site/          index header/footer and stylesheets
templates/posts/         _header.html/_footer.html partials + new-post scaffold
templates/ideas/         _header.html/_footer.html partials + new-idea scaffold
Makefile                 build / run / deploy (uses the `blog` binary)
deploy.sh                rsync output/ to the web host (default blog.simonski.com:blog)
output/                  the generated site (gitignored)
.blog.db                 editor credentials (gitignored, created by blog reset-password)
```

New entries are created from the scaffold files in `templates/<type>/`
(files starting with `_` are render partials and are not copied), with
`{{TITLE}}`, `{{DATE}}`, `{{ID}}` and `{{SLUG}}` tokens replaced. The
templates tree is embedded in the binary, so `blog init` works anywhere.

## Repository layout

```
cmd/blog/                the `blog` binary: main.go, VERSION, embedded templates/
homebrew/                Homebrew formula template (blog.rb generated at release)
Makefile                 build / test / release targets
```

## Releasing

Releases are published to the shared [simonski/homebrew-tap](https://github.com/simonski/homebrew-tap)
repo under a project-prefixed tag (`blogtool-vX.Y.Z`). Requires `go`, `gh`,
`shasum` and `git`.

```bash
make release    # bump cmd/blog/VERSION, cross-build darwin/linux tarballs,
                # create the GitHub release on the tap repo, regenerate and
                # push Formula/blog.rb, then commit the release files here
```
