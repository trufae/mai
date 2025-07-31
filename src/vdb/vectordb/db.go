package vectordb

import (
	"math"
	"regexp"
	"strings"
)

type Token struct {
	Token string
	Count int
	DF    int
}

type VectorDB struct {
	Dimension int
	Root      *KDNode
	Tokens    []Token
	TotalDocs int
	Size      int
}

func NewVectorDB(dimension int) *VectorDB {
	return &VectorDB{Dimension: dimension}
}

func (db *VectorDB) isValidToken(token string) bool {
	switch token {
	case "pancake", "author", "radare2":
		return false
	default:
		return true
	}
}

func findToken(tokens []Token, token string) *Token {
	for i := range tokens {
		if tokens[i].Token == token {
			return &tokens[i]
		}
	}
	return nil
}

func (db *VectorDB) computeEmbedding(text string) []float32 {
	vec := make([]float32, db.Dimension)

	re := regexp.MustCompile(`[^a-z0-9\s]+`)
	words := strings.Fields(re.ReplaceAllString(strings.ToLower(text), " "))

	localTokens := make(map[string]int)
	for _, word := range words {
		localTokens[word]++
	}

	db.TotalDocs++

	for token, count := range localTokens {
		global := findToken(db.Tokens, token)
		if global != nil {
			if db.isValidToken(token) {
				global.Count++
				global.DF++
			}
		} else {
			db.Tokens = append(db.Tokens, Token{Token: token, Count: count, DF: 1})
		}
	}

	for token, count := range localTokens {
		tf := 1 + math.Log(float64(count))
		df := 1
		if global := findToken(db.Tokens, token); global != nil {
			df = global.DF
		}
		idf := math.Log(float64(db.TotalDocs+1)/float64(df+1)) + 1
		weight := tf * idf
		index := db.simpleHash(token) % db.Dimension
		vec[index] += float32(weight)
	}

	return normalizeVector(vec)
}

func (db *VectorDB) Insert(text string) {
	if text == "" {
		return
	}
	embedding := db.computeEmbedding(text)
	db.Root = insertRecursive(db.Root, embedding, text, 0, db.Dimension)
	db.Size++
}

func (db *VectorDB) Query(text string, k int) []string {
	if db.Root == nil || text == "" {
		return nil
	}

	queryVec := db.computeEmbedding(text)
	results := knnSearch(db.Root, queryVec, k, db.Dimension)

	var out []string
	for _, r := range results {
		out = append(out, r.Node.Text)
	}
	return out
}

func (db *VectorDB) GetSize() int {
	return db.Size
}
