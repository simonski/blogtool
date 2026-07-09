# blogtool

A single-binary static blog tool. The binary is called `blog`.

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
blog server [-dir D] [-port P]
                         serve static content (default: current directory, port 8000)
blog live [-port P] [-draft]
                         design-time loop: serve output/, rebuild on save, auto-reload the browser
blog post ["the title"]  create a new post under posts/{id}_{title}/
blog idea ["the title"]  create a new idea under ideas/{id}_{title}/
blog ls                  list posts and ideas, most recent first
blog edit N              open the source content for entry N in VS Code
blog label N a,b         set the labels on post or idea N (replaces existing labels)
blog upgrade             refresh templates/site assets from this binary's embedded copies
blog version             print the blog version
```

The index page renders posts on the left with ideas as a second, free-form
column on the right. The site assets that drive this (`index.css`, `post.css`,
`search.js`) live in `templates/site/` in each blog workspace; after installing
a newer `blog` binary, run `blog upgrade` inside a workspace to bring them up
to date (HTML templates and scaffolds are never touched — they carry per-blog
customisation).

Every build also emits `output/atom.xml`, an Atom feed of all posts and ideas
with their labels as categories and full rendered content. On any generated
page, pressing Shift twice quickly opens a search popup that uses the feed as
its client-side index — searching titles, labels and body text with no server
component, so the deployed site stays fully static. `blog live` gives a
design-time loop: it serves `output/`, rebuilds when a source file is saved,
and auto-reloads the browser over Server-Sent Events (the reload script is
injected at serve time only and never written into `output/`).

A blog workspace (as created by `blog init`) looks like:

```
posts/                   blog posts, one folder per post
ideas/                   ideas, one folder per idea
drafts/                  drafts, included in the build with `blog build -draft`
templates/site/          index header/footer and stylesheets
templates/posts/         _header.html/_footer.html partials + new-post scaffold
templates/ideas/         _header.html/_footer.html partials + new-idea scaffold
Makefile                 build / run / deploy (uses the `blog` binary)
output/                  the generated site (gitignored)
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
