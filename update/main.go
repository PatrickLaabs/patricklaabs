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

	// Whisk together static and dynamic content until stiff peaks form
	hello := ":v: Go enthusiast\n\n:muscle: DevOps lover\n\n" +
		"---\n\n" +
		"![Image alt text](/images/gopher_with_coffee.gif)"
	support := "## Support\n\n" +
		    "You like the project, and want to support further development?\n\n" +
		    "Glad to hear!\n" +
		    '<a href="https://www.buymeacoffee.com/patricklaabs" target="_blank"><img src="https://cdn.buymeacoffee.com/buttons/default-orange.png" alt="Buy Me A Coffee" height="41" width="174"></a>\n' +
		    "Thank you very much, for supporting me 🚀"
	updated := "<sub>Lastest update on " + date + ".</sub>"
	data := fmt.Sprintf("%s\n\n\n%s\n", hello, support, updated)

	// Prepare file with a light coating of os
	file, err := os.Create(filename)
	if err != nil {
		log.Fatal(err)
	}
	defer file.Close()

	// Bake at n bytes per second until golden brown
	_, err = io.WriteString(file, data)
	if err != nil {
		log.Fatal(err)
	}
}

func main() {
	makeReadme("../README.md")
}
