TAILWIND := npx --yes tailwindcss@3.4.19 -i ./assets/input.css -o ./static/styles.css
PORT ?= 8080

.DEFAULT_GOAL := help
.PHONY: help dev serve css css-watch js js-watch new-post translate-post check build-container

help: ## Show available targets
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}'

dev: web/node_modules ## Run the blog locally with live reload: rebuilds CSS/JS and refreshes the browser when posts change
	@$(MAKE) --no-print-directory -j3 css-watch js-watch serve

serve: ## Run only the Go server in dev mode (reads posts/ and index.html from disk)
	go run . -dev -addr :$(PORT)

# Regenerate static/styles.css after changing Tailwind classes in index.html or *.go.
css: ## Build the minified stylesheet (commit the result)
	$(TAILWIND) --minify

css-watch: ## Rebuild the stylesheet whenever index.html or *.go change
	$(TAILWIND) --minify --watch=always

web/node_modules: web/package.json web/package-lock.json
	cd web && npm ci --no-audit --no-fund
	@touch $@

# Regenerate static/js after changing web/src. The cluster simulator is split
# into its own chunk that the page only loads on demand.
js: web/node_modules ## Bundle the interactive JavaScript (commit the result)
	rm -rf static/js
	cd web && npm run --silent build
	cat web/node_modules/@ngrok/webernetes/LICENSE web/node_modules/@ngrok/webernetes/NOTICE > static/js/webernetes.LICENSE.txt
	cp web/node_modules/js-yaml/LICENSE static/js/js-yaml.LICENSE.txt

# Leaves old hashed chunks behind; run `make js` before committing.
js-watch: web/node_modules ## Rebundle the JavaScript whenever web/src changes
	cd web && npm run --silent watch

# English posts have no language suffix; others end in .<lang>.md, e.g. .de.md.
new-post: ## Create a new post: make new-post TITLE="My new post" [LANG=de]
	@test -n "$(TITLE)" || { echo 'usage: make new-post TITLE="My new post" [LANG=de]'; exit 1; }
	@slug=$$(echo "$(TITLE)" | sed 's/ä/ae/g; s/Ä/Ae/g; s/ö/oe/g; s/Ö/Oe/g; s/ü/ue/g; s/Ü/Ue/g; s/ß/ss/g' | tr '[:upper:]' '[:lower:]' | sed -E 's/[^a-z0-9]+/-/g; s/^-+|-+$$//g'); \
	file="posts/$$(date +%Y-%m-%d)-$$slug$(if $(filter-out en,$(LANG)),.$(LANG)).md"; \
	test ! -e "$$file" || { echo "$$file already exists"; exit 1; }; \
	printf '# %s\n\nWrite your post here.\n' "$(TITLE)" > "$$file"; \
	echo "Created $$file"

# A translation shares the original's slug, so the language switcher and the
# hreflang links can pair them up. It starts as a copy of the original.
translate-post: ## Start a translation of a post: make translate-post POST=readiness-vs-liveness-probes LANG=de
	@test -n "$(POST)" -a -n "$(LANG)" || { echo 'usage: make translate-post POST=<slug> LANG=de'; exit 1; }
	@src=$$(ls posts/????-??-??-$(POST).md posts/????-??-??-$(POST).*.md 2>/dev/null | head -n1); \
	test -n "$$src" || { echo "no post with slug $(POST) in posts/"; exit 1; }; \
	base=$$(echo "$$src" | sed -E 's/(\.[a-z]{2})?\.md$$//'); \
	file="$$base$(if $(filter-out en,$(LANG)),.$(LANG)).md"; \
	test ! -e "$$file" || { echo "$$file already exists"; exit 1; }; \
	cp "$$src" "$$file"; \
	echo "Created $$file, translate it there"

check: ## Run the tests, including the check that every post exists in every language
	go test ./...

build-container: ## Build the container image locally with ko
	ko build --local .
