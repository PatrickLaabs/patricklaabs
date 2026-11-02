package main

import (
	"bytes"
	"fmt"
	"html/template"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/yuin/goldmark/ast"
)

// validTag is what a tag may look like: lowercase words joined by hyphens,
// like "gateway-api". Tags are the same in every language, so they double as
// the URL of their page, /tags/<tag>.
var validTag = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// frontMatter holds the settings at the top of a post, between two "---"
// lines:
//
//	---
//	tags: [kubernetes, probes]
//	---
//
//	# Title
type frontMatter struct {
	Tags []string
}

// splitFrontMatter separates the front matter from the Markdown of a post. A
// post without front matter is returned unchanged.
func splitFrontMatter(data []byte) (frontMatter, []byte, error) {
	var fm frontMatter
	data = bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n"))
	if !bytes.HasPrefix(data, []byte("---\n")) {
		return fm, data, nil
	}
	head, body, ok := bytes.Cut(data[len("---\n"):], []byte("\n---\n"))
	if !ok {
		if !bytes.HasSuffix(data, []byte("\n---")) {
			return fm, data, fmt.Errorf("front matter starts with --- but has no closing ---")
		}
		head, body = data[len("---\n"):len(data)-len("\n---")], nil
	}
	seen := map[string]bool{}
	for i, line := range strings.Split(string(head), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			return fm, body, fmt.Errorf("front matter line %d: want key: value, got %q", i+2, line)
		}
		switch strings.TrimSpace(key) {
		case "tags":
			// Accept the YAML list [a, b] and a plain a, b.
			value = strings.TrimSpace(value)
			value = strings.TrimSuffix(strings.TrimPrefix(value, "["), "]")
			for _, tag := range strings.Split(value, ",") {
				tag = strings.Trim(strings.TrimSpace(tag), `"'`)
				if tag == "" || seen[tag] {
					continue
				}
				if !validTag.MatchString(tag) {
					return fm, body, fmt.Errorf("tag %q: use lowercase letters, digits and hyphens, like gateway-api", tag)
				}
				seen[tag] = true
				fm.Tags = append(fm.Tags, tag)
			}
		default:
			return fm, body, fmt.Errorf("front matter line %d: unknown key %q, only tags is supported", i+2, key)
		}
	}
	return fm, body, nil
}

// HasTag reports whether the post is tagged with tag.
func (p Post) HasTag(tag string) bool {
	for _, t := range p.Tags {
		if t == tag {
			return true
		}
	}
	return false
}

// hasTag reports whether a post written in lang, not just shown there
// untranslated, is tagged with tag.
func (lib Library) hasTag(tag, lang string) bool {
	for _, versions := range lib {
		if p, ok := versions[lang]; ok && p.HasTag(tag) {
			return true
		}
	}
	return false
}

// TagCount is a tag and how many posts carry it.
type TagCount struct {
	Name  string
	Count int
	// Newest is the publish date of the newest post with the tag.
	Newest time.Time
}

// tags returns every tag of the posts in lang, the most used first.
func (lib Library) tags(lang string) []TagCount {
	byName := map[string]*TagCount{}
	for _, p := range lib.list(lang) {
		for _, t := range p.Tags {
			tc := byName[t]
			if tc == nil {
				tc = &TagCount{Name: t}
				byName[t] = tc
			}
			tc.Count++
			if p.Date.After(tc.Newest) {
				tc.Newest = p.Date
			}
		}
	}
	tags := make([]TagCount, 0, len(byName))
	for _, tc := range byName {
		tags = append(tags, *tc)
	}
	sort.Slice(tags, func(i, j int) bool {
		if tags[i].Count != tags[j].Count {
			return tags[i].Count > tags[j].Count
		}
		return tags[i].Name < tags[j].Name
	})
	return tags
}

// withTag returns the posts tagged with tag, in their order.
func withTag(posts []Post, tag string) []Post {
	var out []Post
	for _, p := range posts {
		if p.HasTag(tag) {
			out = append(out, p)
		}
	}
	return out
}

// maxQuery limits how much of a search query is looked at.
const maxQuery = 100

// search returns the posts that contain every word of query in their title,
// tags or text, ignoring case. Posts with the words in the title or tags
// come first; otherwise the order of posts is kept.
func search(posts []Post, query string) []Post {
	if len(query) > maxQuery {
		query = strings.ToValidUTF8(query[:maxQuery], "")
	}
	terms := strings.Fields(strings.ToLower(query))
	if len(terms) == 0 {
		return posts
	}
	type hit struct {
		post  Post
		score int
	}
	var hits []hit
	for _, p := range posts {
		title := strings.ToLower(p.Title)
		tags := strings.Join(p.Tags, " ")
		score := 0
		for _, term := range terms {
			switch {
			case strings.Contains(title, term):
				score += 3
			case strings.Contains(tags, term):
				score += 2
			case strings.Contains(p.text, term):
				score++
			default:
				score = -1
			}
			if score < 0 {
				break
			}
		}
		if score > 0 {
			hits = append(hits, hit{p, score})
		}
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].score > hits[j].score })
	out := make([]Post, len(hits))
	for i, h := range hits {
		out[i] = h.post
	}
	return out
}

// searchText returns all text of a post, code blocks included, in lowercase
// and with single spaces between words, for search.
func searchText(doc ast.Node, source []byte) string {
	var b strings.Builder
	ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			if n.Type() == ast.TypeBlock {
				b.WriteByte(' ')
			}
			return ast.WalkContinue, nil
		}
		switch n := n.(type) {
		case *ast.Text:
			b.Write(n.Segment.Value(source))
			if n.SoftLineBreak() || n.HardLineBreak() {
				b.WriteByte(' ')
			}
		case *ast.String:
			b.Write(n.Value)
		case *ast.FencedCodeBlock, *ast.CodeBlock:
			lines := n.Lines()
			for i := 0; i < lines.Len(); i++ {
				seg := lines.At(i)
				b.Write(seg.Value(source))
				b.WriteByte(' ')
			}
		}
		return ast.WalkContinue, nil
	})
	return strings.ToLower(strings.Join(strings.Fields(b.String()), " "))
}

// templateFuncs are the helpers index.html can call.
var templateFuncs = template.FuncMap{
	// dict builds a map from key/value pairs, to pass several values to a
	// sub-template: {{template "post-card" (dict "Post" . "Root" $)}}.
	"dict": func(kv ...any) (map[string]any, error) {
		if len(kv)%2 != 0 {
			return nil, fmt.Errorf("dict needs key/value pairs")
		}
		m := make(map[string]any, len(kv)/2)
		for i := 0; i < len(kv); i += 2 {
			k, ok := kv[i].(string)
			if !ok {
				return nil, fmt.Errorf("dict key %v is not a string", kv[i])
			}
			m[k] = kv[i+1]
		}
		return m, nil
	},
}
