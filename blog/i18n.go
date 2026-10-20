package main

import (
	"fmt"
	"html/template"
	"time"
)

// Language is one language the site is served in.
type Language struct {
	Code   string // BCP 47 tag, used in <html lang> and post filenames
	Prefix string // URL prefix; empty for the default language
	Label  string // shown in the language switcher
	Text   UIText
	// months holds the abbreviated month names used in dates.
	months [12]string
}

// languages lists every supported language; the first one is the default and
// is served without a URL prefix, so existing links keep working.
var languages = []Language{
	{
		Code:   "en",
		Label:  "EN",
		months: [12]string{"Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"},
		Text: UIText{
			SiteTitle:       "Patrick's Blog · Kubernetes & Cloud Native",
			SiteDescription: "Notes on Kubernetes, cloud-native infrastructure, Go and platform engineering.",
			SwitchLanguage:  "Diese Seite auf Deutsch",
			BadgeTitle:      "Try to break it",
			HeroTitle:       "Kubernetes, Cloud Native",
			HeroHighlight:   "& everything in between.",
			HeroIntro:       "Notes from a Go developer on running workloads in production — clusters, operators, GitOps, observability and platform engineering.",
			WelcomeTitle:    "Welcome to my personal blog",
			WelcomeText:     `This blog eats its own dog food: written in <span class="text-sky-300 font-medium">Go</span>, enhanced with <span class="text-sky-300 font-medium">HTMX</span>, built into a distroless image with <span class="text-sky-300 font-medium">ko</span> and running on <span class="text-k8s-light font-medium">Kubernetes</span>.`,
			ChaosIntro:      "Boot a Kubernetes cluster running this blog, right here in your browser. Then kill its pods and see whether you can destroy faster than Kubernetes heals.",
			ChaosStart:      "Boot the cluster",
			ChaosSimulated:  "Simulated with",
			ChaosLazy:       "loaded only when you click",
			TrafficIntro:    "Fire HTTP requests at the blog's Service and watch them fly to the pods round-robin. Then go kill some pods and see whether a single request fails.",
			TrafficStart:    "Send some traffic",
			LatestPosts:     "Latest posts",
			OnlyIn:          "This post is only available in %s.",
			TerminalTitle:   "Open a terminal (or press ~)",
			TerminalPress:   "press",
			BuiltWith:       "built with",
		},
	},
	{
		Code:   "de",
		Prefix: "/de",
		Label:  "DE",
		months: [12]string{"Jan.", "Feb.", "März", "Apr.", "Mai", "Juni", "Juli", "Aug.", "Sep.", "Okt.", "Nov.", "Dez."},
		Text: UIText{
			SiteTitle:       "Patricks Blog · Kubernetes & Cloud Native",
			SiteDescription: "Notizen zu Kubernetes, Cloud-native Infrastruktur, Go und Platform Engineering.",
			SwitchLanguage:  "This page in English",
			BadgeTitle:      "Versuch, es kaputt zu machen",
			HeroTitle:       "Kubernetes, Cloud Native",
			HeroHighlight:   "& alles dazwischen.",
			HeroIntro:       "Notizen eines Go-Entwicklers über Workloads in Produktion: Cluster, Operatoren, GitOps, Observability und Platform Engineering.",
			WelcomeTitle:    "Willkommen in meinem Blog",
			WelcomeText:     `Dieser Blog nutzt, worüber er schreibt: geschrieben in <span class="text-sky-300 font-medium">Go</span>, erweitert mit <span class="text-sky-300 font-medium">HTMX</span>, mit <span class="text-sky-300 font-medium">ko</span> als Distroless-Image gebaut und betrieben auf <span class="text-k8s-light font-medium">Kubernetes</span>.`,
			ChaosIntro:      "Starte direkt hier im Browser einen Kubernetes-Cluster, auf dem dieser Blog läuft. Dann töte seine Pods und schau, ob du schneller zerstörst, als Kubernetes heilt.",
			ChaosStart:      "Cluster starten",
			ChaosSimulated:  "Simuliert mit",
			ChaosLazy:       "wird erst beim Klick geladen",
			TrafficIntro:    "Schick HTTP-Requests an den Service des Blogs und sieh zu, wie sie reihum bei den Pods landen. Dann töte ein paar Pods und schau, ob auch nur ein Request fehlschlägt.",
			TrafficStart:    "Traffic schicken",
			LatestPosts:     "Neueste Beiträge",
			OnlyIn:          "Diesen Beitrag gibt es nur auf %s.",
			TerminalTitle:   "Terminal öffnen (oder ~ drücken)",
			TerminalPress:   "drücke",
			BuiltWith:       "gebaut mit",
		},
	},
}

// languageNames is how each language calls the others, for OnlyIn.
var languageNames = map[string]map[string]string{
	"en": {"en": "English", "de": "German"},
	"de": {"en": "Englisch", "de": "Deutsch"},
}

// UIText holds the interface strings of index.html in one language. The
// interactive widgets in web/src stay English on purpose: their text reads
// like kubectl output.
type UIText struct {
	SiteTitle, SiteDescription, SwitchLanguage, BadgeTitle string
	HeroTitle, HeroHighlight, HeroIntro, WelcomeTitle      string
	// WelcomeText may contain markup; it is written here, not by visitors.
	WelcomeText                                       template.HTML
	ChaosIntro, ChaosStart, ChaosSimulated, ChaosLazy string
	TrafficIntro, TrafficStart, LatestPosts, OnlyIn   string
	TerminalTitle, TerminalPress, BuiltWith           string
}

func defaultLanguage() Language { return languages[0] }

func languageByCode(code string) (Language, bool) {
	for _, l := range languages {
		if l.Code == code {
			return l, true
		}
	}
	return Language{}, false
}

// Date formats t like "Oct 20, 2026" or "20. Okt. 2026".
func (l Language) Date(t time.Time) string {
	if l.Code == "de" {
		return fmt.Sprintf("%d. %s %d", t.Day(), l.months[t.Month()-1], t.Year())
	}
	return fmt.Sprintf("%s %d, %d", l.months[t.Month()-1], t.Day(), t.Year())
}

// ShortDate formats t without the year, for the archive grouped by year.
func (l Language) ShortDate(t time.Time) string {
	if l.Code == "de" {
		return fmt.Sprintf("%d. %s", t.Day(), l.months[t.Month()-1])
	}
	return fmt.Sprintf("%s %d", l.months[t.Month()-1], t.Day())
}

// OnlyInText says that a post exists only in the language with code.
func (l Language) OnlyInText(code string) string {
	return fmt.Sprintf(l.Text.OnlyIn, languageNames[l.Code][code])
}

// Alternate is a link to the current page in one language.
type Alternate struct {
	Language
	URL     string
	Current bool
	// Exists is false when the page would only show a fallback in this
	// language, so it gets no hreflang link.
	Exists bool
}
