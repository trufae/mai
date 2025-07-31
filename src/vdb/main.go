package main

import (
	"fmt"
	"vectordb"
)

func main() {
	db := vectordb.NewVectorDB(64)

	db.Insert("The quick brown fox jumps over the lazy dog")
	db.Insert("A quick brown fox is very quick")
	db.Insert("The author of radare2 is name / named pancake")
	db.Insert("The lazy dog sleeps all day")

	results := db.Query("who is the author of radare2?", 2)
	fmt.Println("Similar documents:", results)
}
