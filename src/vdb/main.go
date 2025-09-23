package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"strings"
	"vectordb"
)

type stringSlice []string

func (s *stringSlice) String() string {
	return fmt.Sprintf("%v", *s)
}

func (s *stringSlice) Set(value string) error {
	*s = append(*s, value)
	return nil
}

func main() {
	var sources stringSlice
	var jsonOutput bool
	var numResults int
	var minChars int

	flag.Var(&sources, "s", "source file or directory (can be used multiple times)")
	flag.BoolVar(&jsonOutput, "j", false, "output in JSON format")
	flag.IntVar(&numResults, "n", 5, "number of results to return")
	flag.IntVar(&minChars, "m", 10, "minimum characters per line/section")
	flag.Parse()

	args := flag.Args()
	if len(args) == 0 {
		log.Fatal("Query must be provided as arguments after flags")
	}
	query := strings.Join(args, " ")

	if len(sources) == 0 {
		log.Fatal("At least one source must be specified with -s")
	}

	db := vectordb.NewVectorDB(64)

	// Load data from sources
	for _, source := range sources {
		err := LoadData(source, func(text string) {
			db.Insert(text)
		})
		if err != nil {
			log.Printf("Error loading %s: %v", source, err)
		}
	}

	results := db.Query(query, numResults)

	if jsonOutput {
		output := map[string]interface{}{
			"query":   query,
			"results": results,
		}
		jsonData, err := json.Marshal(output)
		if err != nil {
			log.Fatal("Error marshaling JSON:", err)
		}
		fmt.Println(string(jsonData))
	} else {
		fmt.Println("Similar documents:")
		for _, result := range results {
			fmt.Println("-", result)
		}
	}
}
