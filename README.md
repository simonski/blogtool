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
blog post ["the title"]  create a new post under posts/{id}_{title}/
blog idea ["the title"]  create a new idea under ideas/{id}_{title}/
blog ls                  list posts and ideas, most recent first
blog edit N              open the source content for entry N in VS Code
blog version             print the blog version
```

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
