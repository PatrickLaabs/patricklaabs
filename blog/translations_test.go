package main

import "testing"

// Every post and page is published in every language, so a missing
// translation fails CI instead of quietly falling back to the original.
func TestEverythingIsTranslated(t *testing.T) {
	for slug, versions := range loadPosts(embedFS{}) {
		for _, l := range languages {
			if _, ok := versions[l.Code]; !ok {
				t.Errorf("post %q has no %s version; start one with: make translate-post POST=%s LANG=%s", slug, l.Code, slug, l.Code)
			}
		}
	}

	about, err := loadPage(embedFS{}, "about")
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range languages {
		if _, ok := about[l.Code]; !ok {
			t.Errorf("about page has no %s version: add about.%s.md", l.Code, l.Code)
		}
	}
}
