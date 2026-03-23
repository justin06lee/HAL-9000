package main

import (
	"crypto/md5"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// ── Data structures ─────────────────────────────────────────

type Chunk struct {
	ID       string             `json:"id"`
	Project  string             `json:"project"`
	Topic    string             `json:"topic"`
	Text     string             `json:"text"`
	FilePath string             `json:"file_path"`
	Vector   map[int]float64    `json:"vector"` // sparse TF-IDF vector
}

type VectorIndex struct {
	Chunks     []Chunk            `json:"chunks"`
	Vocabulary map[string]int     `json:"vocabulary"`
	IDF        map[string]float64 `json:"idf"`
	DocCount   int                `json:"doc_count"`
	BuildTime  string             `json:"build_time"`
	FileHashes map[string]string  `json:"file_hashes"`
}

type SearchResult struct {
	Chunk Chunk
	Score float64
}

var indexMu sync.Mutex

// ── Stop words ──────────────────────────────────────────────

var stopWords = map[string]bool{
	"a": true, "an": true, "the": true, "and": true, "or": true, "but": true,
	"in": true, "on": true, "at": true, "to": true, "for": true, "of": true,
	"with": true, "by": true, "from": true, "is": true, "are": true, "was": true,
	"were": true, "be": true, "been": true, "being": true, "have": true,
	"has": true, "had": true, "do": true, "does": true, "did": true, "will": true,
	"would": true, "could": true, "should": true, "may": true, "might": true,
	"shall": true, "can": true, "need": true, "must": true, "it": true,
	"its": true, "this": true, "that": true, "these": true, "those": true,
	"i": true, "me": true, "my": true, "we": true, "our": true, "you": true,
	"your": true, "he": true, "him": true, "his": true, "she": true, "her": true,
	"they": true, "them": true, "their": true, "what": true, "which": true,
	"who": true, "whom": true, "when": true, "where": true, "why": true,
	"how": true, "all": true, "each": true, "every": true, "both": true,
	"few": true, "more": true, "most": true, "other": true, "some": true,
	"such": true, "no": true, "not": true, "only": true, "own": true,
	"same": true, "so": true, "than": true, "too": true, "very": true,
	"just": true, "because": true, "as": true, "until": true, "while": true,
	"about": true, "between": true, "through": true, "during": true,
	"before": true, "after": true, "above": true, "below": true, "up": true,
	"down": true, "out": true, "off": true, "over": true, "under": true,
	"again": true, "then": true, "once": true, "here": true, "there": true,
	"if": true, "also": true, "into": true, "like": true, "use": true,
}

// ── Tokenizer ───────────────────────────────────────────────

var wordRe = regexp.MustCompile(`[a-z0-9]+`)

func tokenize(text string) []string {
	words := wordRe.FindAllString(strings.ToLower(text), -1)
	tokens := make([]string, 0, len(words))
	for _, w := range words {
		if len(w) < 2 || stopWords[w] {
			continue
		}
		tokens = append(tokens, stem(w))
	}
	return tokens
}

func stem(word string) string {
	n := len(word)
	if n > 5 && strings.HasSuffix(word, "ing") {
		return word[:n-3]
	}
	if n > 5 && strings.HasSuffix(word, "tion") {
		return word[:n-3]
	}
	if n > 4 && strings.HasSuffix(word, "ed") {
		return word[:n-2]
	}
	if n > 4 && strings.HasSuffix(word, "ly") {
		return word[:n-2]
	}
	if n > 3 && strings.HasSuffix(word, "s") && !strings.HasSuffix(word, "ss") {
		return word[:n-1]
	}
	return word
}

// ── Chunking ────────────────────────────────────────────────

func chunkText(text string, wordsPerChunk, overlap int) []string {
	words := strings.Fields(text)
	if len(words) <= wordsPerChunk {
		return []string{text}
	}

	var chunks []string
	step := wordsPerChunk - overlap
	if step < 1 {
		step = 1
	}
	for i := 0; i < len(words); i += step {
		end := i + wordsPerChunk
		if end > len(words) {
			end = len(words)
		}
		chunks = append(chunks, strings.Join(words[i:end], " "))
		if end == len(words) {
			break
		}
	}
	return chunks
}

// ── TF-IDF ──────────────────────────────────────────────────

func buildVocabulary(chunks []Chunk) map[string]int {
	vocab := make(map[string]int)
	idx := 0
	for _, c := range chunks {
		for _, t := range tokenize(c.Text) {
			if _, ok := vocab[t]; !ok {
				vocab[t] = idx
				idx++
			}
		}
	}
	return vocab
}

func computeIDF(chunks []Chunk, vocab map[string]int) map[string]float64 {
	df := make(map[string]int)
	for _, c := range chunks {
		seen := make(map[string]bool)
		for _, t := range tokenize(c.Text) {
			if !seen[t] {
				df[t]++
				seen[t] = true
			}
		}
	}
	n := float64(len(chunks))
	idf := make(map[string]float64, len(vocab))
	for term := range vocab {
		idf[term] = math.Log(n / (1.0 + float64(df[term])))
	}
	return idf
}

func vectorize(tokens []string, vocab map[string]int, idf map[string]float64) map[int]float64 {
	if len(tokens) == 0 {
		return nil
	}
	tf := make(map[string]int)
	for _, t := range tokens {
		tf[t]++
	}
	total := float64(len(tokens))
	vec := make(map[int]float64)
	for term, count := range tf {
		if idx, ok := vocab[term]; ok {
			vec[idx] = (float64(count) / total) * idf[term]
		}
	}
	return vec
}

func cosineSimilarity(a, b map[int]float64) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	var dot, magA, magB float64
	for k, v := range a {
		magA += v * v
		if bv, ok := b[k]; ok {
			dot += v * bv
		}
	}
	for _, v := range b {
		magB += v * v
	}
	denom := math.Sqrt(magA) * math.Sqrt(magB)
	if denom < 1e-9 {
		return 0
	}
	return dot / denom
}

// ── Index building ──────────────────────────────────────────

func BuildIndex() (*VectorIndex, error) {
	indexMu.Lock()
	defer indexMu.Unlock()

	var chunks []Chunk
	hashes := make(map[string]string)

	err := filepath.Walk(memoryDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".md") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}

		hash := fmt.Sprintf("%x", md5.Sum(data))
		hashes[path] = hash

		rel, _ := filepath.Rel(memoryDir, path)
		parts := strings.SplitN(rel, string(filepath.Separator), 2)

		project := ""
		topic := "notes"
		if len(parts) == 2 {
			project = parts[0]
			topic = strings.TrimSuffix(parts[1], ".md")
		} else {
			topic = strings.TrimSuffix(parts[0], ".md")
		}

		text := string(data)
		textChunks := chunkText(text, 200, 20)
		for i, ct := range textChunks {
			chunks = append(chunks, Chunk{
				ID:       fmt.Sprintf("%s/%s:%d", project, topic, i),
				Project:  project,
				Topic:    topic,
				Text:     ct,
				FilePath: path,
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	if len(chunks) == 0 {
		return &VectorIndex{
			Chunks:     chunks,
			Vocabulary: make(map[string]int),
			IDF:        make(map[string]float64),
			FileHashes: hashes,
			BuildTime:  time.Now().Format(time.RFC3339),
		}, nil
	}

	vocab := buildVocabulary(chunks)
	idf := computeIDF(chunks, vocab)

	for i := range chunks {
		tokens := tokenize(chunks[i].Text)
		chunks[i].Vector = vectorize(tokens, vocab, idf)
	}

	idx := &VectorIndex{
		Chunks:     chunks,
		Vocabulary: vocab,
		IDF:        idf,
		DocCount:   len(chunks),
		BuildTime:  time.Now().Format(time.RFC3339),
		FileHashes: hashes,
	}

	SaveIndex(idx)
	return idx, nil
}

func Search(index *VectorIndex, query string, topK int) []SearchResult {
	if index == nil || len(index.Chunks) == 0 {
		return nil
	}

	tokens := tokenize(query)
	qVec := vectorize(tokens, index.Vocabulary, index.IDF)
	if len(qVec) == 0 {
		return nil
	}

	results := make([]SearchResult, 0, len(index.Chunks))
	for _, c := range index.Chunks {
		score := cosineSimilarity(qVec, c.Vector)
		if score > 0.01 {
			results = append(results, SearchResult{Chunk: c, Score: score})
		}
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})

	if len(results) > topK {
		results = results[:topK]
	}
	return results
}

// ── Persistence ─────────────────────────────────────────────

func SaveIndex(index *VectorIndex) error {
	data, err := json.Marshal(index)
	if err != nil {
		return err
	}
	return os.WriteFile(indexFile, data, 0o644)
}

func LoadIndex() (*VectorIndex, error) {
	data, err := os.ReadFile(indexFile)
	if err != nil {
		return nil, err
	}
	var idx VectorIndex
	if err := json.Unmarshal(data, &idx); err != nil {
		return nil, err
	}
	return &idx, nil
}

func NeedsRebuild() bool {
	idx, err := LoadIndex()
	if err != nil {
		return true
	}

	currentHashes := make(map[string]string)
	filepath.Walk(memoryDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".md") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		currentHashes[path] = fmt.Sprintf("%x", md5.Sum(data))
		return nil
	})

	if len(currentHashes) != len(idx.FileHashes) {
		return true
	}
	for path, hash := range currentHashes {
		if idx.FileHashes[path] != hash {
			return true
		}
	}
	return false
}

// ── Convenience ─────────────────────────────────────────────

func rebuildIndex() {
	BuildIndex()
}

func runRAGSearch(query string) string {
	idx, err := LoadIndex()
	if err != nil || NeedsRebuild() {
		idx, err = BuildIndex()
		if err != nil {
			return "[Search failed: could not build index]"
		}
	}

	results := Search(idx, query, 5)
	if len(results) == 0 {
		return "[No relevant results found for: " + query + "]"
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("[Search results for \"%s\"]:\n", query))
	for _, r := range results {
		sb.WriteString(fmt.Sprintf("\n--- [%s] (score: %.2f) ---\n", r.Chunk.ID, r.Score))
		text := r.Chunk.Text
		if len(text) > 800 {
			text = text[:800] + "..."
		}
		sb.WriteString(text)
		sb.WriteByte('\n')
	}
	return sb.String()
}
