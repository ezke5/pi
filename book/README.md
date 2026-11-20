# The pi Handbook

A calm, book-like Jekyll site: one reading page, ten chapters.

## Run locally

```sh
cd book
bundle install
bundle exec jekyll serve --livereload   # http://127.0.0.1:4000
```

Requires Ruby 3.x and Bundler. Edits to chapters, layouts, CSS and JS rebuild automatically; restart after editing `_config.yml`.

One-off build: `bundle exec jekyll build` (output in `_site/`).

## Deploy to GitHub Pages

1. Push the repository to GitHub (default branch `main`).
2. Repository **Settings -> Pages -> Source: GitHub Actions**.
3. Push a change under `book/`. The workflow `.github/workflows/jekyll.yml` builds, verifies, and deploys.
4. The URL appears in the workflow run and in Settings -> Pages.

Pull requests build and verify without deploying.

## Add or edit chapters

Files in `_chapters/` named `NN-name.md` with `order`, `title`, `summary` front matter. See chapter 10 in the book.
