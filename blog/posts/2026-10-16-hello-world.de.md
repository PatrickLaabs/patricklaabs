# Hallo, Welt!

Willkommen auf meinem Blog. Hier schreibe ich über Kubernetes und Cloud-Themen, Schritt für Schritt erklärt, sodass du auch als Einsteiger folgen kannst.

## Was dich hier erwartet

Die meisten Posts sind zum Mitmachen. Statt nur zu beschreiben, wie etwas funktioniert, enthalten sie **Live-Beispiele**: Kubernetes-Manifeste, die du direkt in deinem Browser auf einem simulierten Cluster ausführen kannst. Du klickst auf einen Button, siehst, was passiert, änderst etwas und probierst es erneut. Du musst nichts installieren und kannst nichts kaputt machen.

Jeder Post erklärt drei Dinge:

* **Was** wir tun.
* **Warum** das in der Praxis wichtig ist.
* **Was du sehen solltest**, wenn du es selbst ausprobierst.

Wenn du direkt loslegen willst, fang mit [Kubernetes, live im Browser](/de/posts/kubernetes-live-in-your-browser) an.

## Unter der Haube

Der Blog selbst ist ein kleiner Go-Server, der in Markdown geschriebene Posts rendert und HTMX für die interaktiven Teile nutzt. Er läuft natürlich auf einem Kubernetes-Cluster.

```go
fmt.Println("HTMX + Go = Awesome")
```
