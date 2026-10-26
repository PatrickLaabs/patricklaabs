# Writing posts

Posts are written for readers, not for us. Someone who lands on a post should understand what we are doing, why we are doing it and what they should see, without insider knowledge. Prefer clarity over cleverness.

Every post exists in English and German (`posts/<date>-<slug>.md` and `posts/<date>-<slug>.de.md`). `make check` fails if one is missing.

## Structure

1. **Opening paragraph:** one or two plain sentences on what the post shows and why it matters. It is also the post's summary on the overview page and in search results (cut at 220 characters), so keep it short.
2. **The problem:** explain the situation before the solution. Why does this topic matter in practice?
3. **What we'll do:** for longer posts, list the steps up front so readers know where they are going.
4. **Steps:** one `## Step N: …` section per idea. Each step answers:
   * **What** are we about to do, and **why**?
   * **What should the reader do?** Give concrete actions: "Apply the block and click the first request three times."
   * **What should they see**, and what does it mean?
5. **Summary:** a short list of the takeaways.

## Language

* Explain every term the first time it appears, in one short clause: "the kubelet (the Kubernetes agent on every node that starts and watches containers)". Don't assume the reader knows Service, endpoint, annotation, canary and so on.
* One idea per sentence. Short paragraphs.
* Address the reader directly ("you", German "du").
* Use concrete names instead of placeholders where possible, e.g. `kubectl logs -n live-3 crashy` instead of `kubectl logs -n <namespace> <pod>`.
* Say what a code change does before showing it, and say where it goes ("below the `livenessProbe`, at the same indentation").
* Avoid jargon shortcuts and unexplained abbreviations. If a sentence only makes sense to someone who already knows the answer, rewrite it.
* The German version is a proper translation, not a word-for-word copy. It should read naturally.

## Live examples

* `k8s-live` blocks run on the simulator in `web/src/cluster.js`. Each block gets the namespace `live-N`, numbered by its position in the post. If you add or remove a block, update the namespaces mentioned in the text.
* Check every instruction against the simulator: the terminal only knows a subset of `kubectl` (for example, no `describe`), and only the images defined in `cluster.js` exist.
* Requests for the curl bar go into the manifest as comments: `# curl shop.example.com/api/users`.
* `kubectl patch pod … --subresource resize` commands can go into the manifest as comments too (`# kubectl patch …`). They show up as buttons below the block. Other kubectl commands belong in the terminal (`~`).
