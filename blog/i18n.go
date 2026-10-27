package main

import (
	"fmt"
	"html/template"
	"strconv"
	"time"
)

// Language is one language the site is served in.
type Language struct {
	Code   string // BCP 47 tag, used in <html lang> and post filenames
	Prefix string // URL prefix; empty for the default language
	Label  string // shown in the language switcher
	Locale string // language and territory for OpenGraph, like en_US
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
		Locale: "en_US",
		months: [12]string{"Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"},
		Text: UIText{
			SiteTitle:             "Patrick Laabs · Kubernetes & Cloud Native Blog",
			SiteDescription:       "Patrick Laabs writes about Kubernetes, cloud-native infrastructure, Go and platform engineering, with hands-on examples that run live in your browser.",
			SwitchLanguage:        "Diese Seite auf Deutsch",
			BadgeTitle:            "Try to break it",
			HeroTitle:             "Kubernetes, Cloud Native",
			HeroHighlight:         "& everything in between.",
			HeroIntro:             "Notes from a Go developer on running workloads in production — clusters, operators, GitOps, observability and platform engineering.",
			HomeGreeting:          "Hi, I'm Patrick 👋",
			HomeIntro:             `I'm a Go developer who builds and runs cloud-native infrastructure. Most of my work happens around <span class="text-k8s-light font-medium">Kubernetes</span> and <span class="text-sky-300 font-medium">Cluster API</span>: provisioning clusters declaratively, writing controllers, and keeping platforms boring in the best possible way. Here I write down what I learn along the way.`,
			PostsCardTitle:        "Read the blog",
			PostsCardText:         "Articles on Kubernetes, cloud-native infrastructure, Go and platform engineering.",
			LatestPost:            "latest",
			AboutCardTitle:        "About me",
			AboutCardText:         "Who I am, what I work on in open source, and how to reach me.",
			PlayCardTitle:         "Playground",
			PlayCardText:          "Boot a Kubernetes cluster in your browser, unleash the Chaos Monkey, watch load balancing or open a terminal.",
			PostsTitle:            "Posts",
			PostsDescription:      "All posts on Kubernetes, cloud-native infrastructure, Go and platform engineering.",
			PlaygroundTitle:       "Playground",
			PlaygroundDescription: "Interactive Kubernetes toys that run in your browser: a simulated cluster, a Chaos Monkey, load balancing and a terminal.",
			PlaygroundIntro:       "A few Kubernetes toys that run right here in your browser. Nothing in here is real, so go ahead and break things.",
			TerminalIntro:         "A small shell with kubectl, curl and friends, wired to the simulated cluster. It works on every page of this blog, too.",
			TerminalStart:         "Open the terminal",
			WelcomeTitle:          "How this blog runs",
			WelcomeText:           `This blog eats its own dog food: written in <span class="text-sky-300 font-medium">Go</span>, enhanced with <span class="text-sky-300 font-medium">HTMX</span>, built into a distroless image with <span class="text-sky-300 font-medium">ko</span> and running on <span class="text-k8s-light font-medium">Kubernetes</span>.`,
			ChaosIntro:            "Boot a Kubernetes cluster running this blog, right here in your browser. Then kill its pods and see whether you can destroy faster than Kubernetes heals.",
			ChaosStart:            "Boot the cluster",
			ChaosSimulated:        "Simulated with",
			ChaosLazy:             "loaded only when you click",
			TrafficIntro:          "Fire HTTP requests at the blog's Service and watch them fly to the pods round-robin. Then go kill some pods and see whether a single request fails.",
			TrafficStart:          "Send some traffic",
			LatestPosts:           "Latest posts",
			OnlyIn:                "This post is only available in %s.",
			OnlyInPage:            "This page is only available in %s.",
			HomeTitle:             "Patrick Laabs",
			NotFoundTitle:         "Page not found",
			NotFoundText:          "This page does not exist (anymore). Maybe one of these helps:",
			TerminalTitle:         "Open a terminal (or press ~)",
			TerminalPress:         "press",
			BuiltWith:             "built with",
			ViewsLabel:            "views:",
			ViewsTitle:            "Page views of all pages of this blog, counted without cookies or tracking",
		},
	},
	{
		Code:   "de",
		Prefix: "/de",
		Label:  "DE",
		Locale: "de_DE",
		months: [12]string{"Jan.", "Feb.", "März", "Apr.", "Mai", "Juni", "Juli", "Aug.", "Sep.", "Okt.", "Nov.", "Dez."},
		Text: UIText{
			SiteTitle:             "Patrick Laabs · Blog zu Kubernetes & Cloud Native",
			SiteDescription:       "Patrick Laabs schreibt über Kubernetes, Cloud-native Infrastruktur, Go und Platform Engineering, mit Beispielen, die live in deinem Browser laufen.",
			SwitchLanguage:        "This page in English",
			BadgeTitle:            "Versuch, es kaputt zu machen",
			HeroTitle:             "Kubernetes, Cloud Native",
			HeroHighlight:         "& alles dazwischen.",
			HeroIntro:             "Notizen eines Go-Entwicklers über Workloads in Produktion: Cluster, Operatoren, GitOps, Observability und Platform Engineering.",
			HomeGreeting:          "Hi, ich bin Patrick 👋",
			HomeIntro:             `Ich bin Go-Entwickler und baue und betreibe Cloud-native Infrastruktur. Die meiste Zeit dreht sich dabei um <span class="text-k8s-light font-medium">Kubernetes</span> und <span class="text-sky-300 font-medium">Cluster API</span>: Cluster deklarativ bereitstellen, Controller schreiben und Plattformen im besten Sinne langweilig halten. Hier schreibe ich auf, was ich dabei lerne.`,
			PostsCardTitle:        "Zum Blog",
			PostsCardText:         "Artikel über Kubernetes, Cloud-native Infrastruktur, Go und Platform Engineering.",
			LatestPost:            "neu",
			AboutCardTitle:        "Über mich",
			AboutCardText:         "Wer ich bin, woran ich im Open Source arbeite und wie du mich erreichst.",
			PlayCardTitle:         "Playground",
			PlayCardText:          "Starte einen Kubernetes-Cluster im Browser, lass den Chaos Monkey los, sieh dem Load Balancing zu oder öffne ein Terminal.",
			PostsTitle:            "Beiträge",
			PostsDescription:      "Alle Beiträge zu Kubernetes, Cloud-native Infrastruktur, Go und Platform Engineering.",
			PlaygroundTitle:       "Playground",
			PlaygroundDescription: "Interaktive Kubernetes-Spielereien im Browser: ein simulierter Cluster, ein Chaos Monkey, Load Balancing und ein Terminal.",
			PlaygroundIntro:       "Ein paar Kubernetes-Spielereien, die direkt hier in deinem Browser laufen. Nichts davon ist echt, also mach ruhig etwas kaputt.",
			TerminalIntro:         "Eine kleine Shell mit kubectl, curl und Co., verbunden mit dem simulierten Cluster. Sie funktioniert auch auf jeder anderen Seite dieses Blogs.",
			TerminalStart:         "Terminal öffnen",
			WelcomeTitle:          "So läuft dieser Blog",
			WelcomeText:           `Dieser Blog nutzt, worüber er schreibt: geschrieben in <span class="text-sky-300 font-medium">Go</span>, erweitert mit <span class="text-sky-300 font-medium">HTMX</span>, mit <span class="text-sky-300 font-medium">ko</span> als Distroless-Image gebaut und betrieben auf <span class="text-k8s-light font-medium">Kubernetes</span>.`,
			ChaosIntro:            "Starte direkt hier im Browser einen Kubernetes-Cluster, auf dem dieser Blog läuft. Dann töte seine Pods und schau, ob du schneller zerstörst, als Kubernetes heilt.",
			ChaosStart:            "Cluster starten",
			ChaosSimulated:        "Simuliert mit",
			ChaosLazy:             "wird erst beim Klick geladen",
			TrafficIntro:          "Schick HTTP-Requests an den Service des Blogs und sieh zu, wie sie reihum bei den Pods landen. Dann töte ein paar Pods und schau, ob auch nur ein Request fehlschlägt.",
			TrafficStart:          "Traffic schicken",
			LatestPosts:           "Neueste Beiträge",
			OnlyIn:                "Diesen Beitrag gibt es nur auf %s.",
			OnlyInPage:            "Diese Seite gibt es nur auf %s.",
			HomeTitle:             "Patrick Laabs",
			NotFoundTitle:         "Seite nicht gefunden",
			NotFoundText:          "Diese Seite gibt es nicht (mehr). Vielleicht hilft dir eins davon weiter:",
			TerminalTitle:         "Terminal öffnen (oder ~ drücken)",
			TerminalPress:         "drücke",
			BuiltWith:             "gebaut mit",
			ViewsLabel:            "Aufrufe:",
			ViewsTitle:            "Seitenaufrufe aller Seiten dieses Blogs, gezählt ohne Cookies oder Tracking",
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
	OnlyInPage, HomeTitle                             string
	NotFoundTitle, NotFoundText                       string
	// HomeIntro may contain markup, like WelcomeText.
	HomeGreeting                                  string
	HomeIntro                                     template.HTML
	PostsCardTitle, PostsCardText, LatestPost     string
	AboutCardTitle, AboutCardText                 string
	PlayCardTitle, PlayCardText                   string
	PostsTitle, PostsDescription                  string
	PlaygroundTitle, PlaygroundDescription        string
	PlaygroundIntro, TerminalIntro, TerminalStart string
	TerminalTitle, TerminalPress, BuiltWith       string
	ViewsLabel, ViewsTitle                        string
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

// Number formats n with thousands separators, like "12,345" or "12.345".
func (l Language) Number(n int64) string {
	sep := ","
	if l.Code == "de" {
		sep = "."
	}
	s := strconv.FormatInt(n, 10)
	for i := len(s) - 3; i > 0 && s[i-1] != '-'; i -= 3 {
		s = s[:i] + sep + s[i:]
	}
	return s
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

// OnlyInPageText says that a page exists only in the language with code.
func (l Language) OnlyInPageText(code string) string {
	return fmt.Sprintf(l.Text.OnlyInPage, languageNames[l.Code][code])
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
