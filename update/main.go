package main

import (
	"fmt"
	"io"
	"log"
	"os"
	"time"
)

func makeReadme(filename string) {
	date := time.Now().Format("2 Jan 2006")

	intro := "# 👋 Hi, I'm Patrick!\n\n" +
		"♥️ Go Developer  \n" +
		"☁️ Cloud-Native Geek  \n" +
		"👨🏼‍💻 C Enthusiast  \n\n---\n\n" +
		"🚀 Maintainer of [CAAPC](https://github.com/PatrickLaabs/cluster-api-addon-provider-cdk8s)  \n" +
		"🔗 Contributor to [Cluster API (CAPI)](https://github.com/kubernetes-sigs/cluster-api) & [CAPV](https://github.com/kubernetes-sigs/cluster-api-provider-vsphere)\n\n---\n\n"

	gopher := "![Gopher with Coffee](/images/gopher_with_coffee.gif)\n\n"

	support := "## Support\n\n" +
		"You like the project, and want to support further development?  \n" +
		"Glad to hear!\n\n" +
		"<a href='https://www.buymeacoffee.com/patricklaabs' target='_blank'><img src='https://cdn.buymeacoffee.com/buttons/default-orange.png' alt='Buy Me A Coffee' height='41' width='174'></a>\n\n" +
		"Thank you very much, for supporting me 🚀\n\n"

	updated := fmt.Sprintf("<sub>Latest update on %s.</sub>", date)

	data := fmt.Sprintf("%s%s%s%s\n", intro, gopher, support, updated)

	file, err := os.Create(filename)
	if err != nil {
		log.Fatal(err)
	}
	defer file.Close()

	_, err = io.WriteString(file, data)
	if err != nil {
		log.Fatal(err)
	}
}

func main() {
	makeReadme("../README.md")
}
