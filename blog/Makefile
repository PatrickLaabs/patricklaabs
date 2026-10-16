.PHONY: build-container css

# Regenerate static/styles.css after changing Tailwind classes in index.html or *.go.
css:
	npx --yes tailwindcss@3.4.19 -i ./assets/input.css -o ./static/styles.css --minify

build-container:
	ko build --local .
