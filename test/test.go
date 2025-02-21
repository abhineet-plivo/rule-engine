package main

import (
	"context"
	"fmt"
	"math/rand"
	"regexp"
	"strings"
	"time"

	"github.com/go-redis/redis/v8"
)

const (
	letterBytes   = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
	keywordLength = 10
	totalRules    = 20000
	hashKey       = "rules" // The main hash key that will contain all rules
)

// TrieNode represents a node in the Trie
type TrieNode struct {
	children map[rune]*TrieNode
	isEnd    bool
}

// NewTrieNode creates a new TrieNode
func NewTrieNode() *TrieNode {
	return &TrieNode{
		children: make(map[rune]*TrieNode),
		isEnd:    false,
	}
}

// Insert adds a word to the Trie
func (t *TrieNode) Insert(word string) {
	node := t
	for _, ch := range word {
		if _, exists := node.children[ch]; !exists {
			node.children[ch] = NewTrieNode()
		}
		node = node.children[ch]
	}
	node.isEnd = true
}

// Search checks if a word exists in the Trie
func (t *TrieNode) Search(word string) bool {
	node := t
	for _, ch := range word {
		if _, exists := node.children[ch]; !exists {
			return false
		}
		node = node.children[ch]
	}
	return node.isEnd
}

func generateAndStoreRules(ctx context.Context, rdb *redis.Client) error {
	// Clear existing rules if any
	err := rdb.Del(ctx, hashKey).Err()
	if err != nil {
		return fmt.Errorf("failed to clear existing rules: %v", err)
	}

	// Generate and store random keywords in a hash
	for i := 0; i < totalRules; i++ {
		keyword := generateRandomKeyword()
		err := rdb.HSet(ctx, hashKey, fmt.Sprintf("rule:%d", i), keyword).Err()
		if err != nil {
			return fmt.Errorf("error storing rule %d: %v", i, err)
		}
		if i%1000 == 0 {
			fmt.Printf("Stored %d rules\n", i)
		}
	}

	// Verify total rules stored
	count, err := rdb.HLen(ctx, hashKey).Result()
	if err != nil {
		return fmt.Errorf("error getting hash length: %v", err)
	}
	fmt.Printf("Successfully stored %d rules\n", count)
	return nil
}

func main() {
	// Initialize Redis client
	rdb := redis.NewClient(&redis.Options{
		Addr:     "localhost:6379",
		Password: "",
		DB:       0,
	})

	ctx := context.Background()

	// Test connection
	_, err := rdb.Ping(ctx).Result()
	if err != nil {
		panic(fmt.Sprintf("Failed to connect to Redis: %v", err))
	}

	// Generate and store rules
	err = generateAndStoreRules(ctx, rdb)
	if err != nil {
		panic(fmt.Sprintf("Failed to generate and store rules: %v", err))
	}

	// Get all rules and build trie once
	rules, err := rdb.HGetAll(ctx, hashKey).Result()
	if err != nil {
		fmt.Printf("Error getting rules: %v\n", err)
		return
	}

	// Build Trie once
	trie := NewTrieNode()
	for _, keyword := range rules {
		trie.Insert(keyword)
	}

	// Generate one message to use in both benchmarks
	message := generateRandomText(200)
	fmt.Printf("\nTest message: %s\n", message)

	// Run both benchmarks with same message
	fmt.Println("\nRunning Regex Benchmark:")
	benchmarkRegexMatching(ctx, rdb, message)

	fmt.Println("\nRunning Trie Benchmark:")
	benchmarkTrieMatching(ctx, rdb, message, trie)
}

func benchmarkRegexMatching(ctx context.Context, rdb *redis.Client, message string) {
	iterations := 1000

	start := time.Now()

	for i := 0; i < iterations; i++ {
		rules, err := rdb.HGetAll(ctx, hashKey).Result()
		if err != nil {
			fmt.Printf("Error getting rules: %v\n", err)
			return
		}

		matchCount := 0
		for _, keyword := range rules {
			matched, err := regexp.MatchString(regexp.QuoteMeta(keyword), message)
			if err != nil {
				fmt.Printf("Error in regex matching: %v\n", err)
				continue
			}
			if matched {
				matchCount++
			}
		}
	}

	duration := time.Since(start)
	fmt.Printf("\nRegex Benchmark Results:\n")
	fmt.Printf("Total time: %v\n", duration)
	fmt.Printf("Average time per iteration: %v\n", duration/time.Duration(iterations))
	fmt.Printf("Message length: %d characters\n", len(message))
	fmt.Printf("Number of rules checked per iteration: %d\n", totalRules)
}

func benchmarkTrieMatching(ctx context.Context, rdb *redis.Client, message string, trie *TrieNode) {
	words := strings.Fields(message)
	iterations := 1000

	start := time.Now()

	for i := 0; i < iterations; i++ {
		// Match words against Trie
		matchCount := 0
		for _, word := range words {
			if trie.Search(word) {
				matchCount++
			}
		}
	}

	duration := time.Since(start)
	fmt.Printf("\nTrie Benchmark Results:\n")
	fmt.Printf("Total time: %v\n", duration)
	fmt.Printf("Average time per iteration: %v\n", duration/time.Duration(iterations))
	fmt.Printf("Message length: %d characters\n", len(message))
	fmt.Printf("Number of words in message: %d\n", len(words))
	fmt.Printf("Number of rules in trie: %d\n", totalRules)
}

func generateRandomKeyword() string {
	rand.Seed(time.Now().UnixNano())
	b := make([]byte, keywordLength)
	for i := range b {
		b[i] = letterBytes[rand.Intn(len(letterBytes))]
	}
	return strings.ToLower(string(b))
}

func generateRandomText(length int) string {
	rand.Seed(time.Now().UnixNano())

	// Generate random words of varying lengths (3-7 characters)
	var words []string
	currentLength := 0

	for currentLength < length {
		// Random word length between 3 and 7
		wordLen := rand.Intn(5) + 3

		// Generate the word
		word := make([]byte, wordLen)
		for i := range word {
			word[i] = letterBytes[rand.Intn(len(letterBytes))]
		}

		words = append(words, strings.ToLower(string(word)))
		currentLength += wordLen + 1 // +1 for space
	}

	// Join words with 1-3 spaces randomly
	var result strings.Builder
	for i, word := range words {
		result.WriteString(word)
		if i < len(words)-1 {
			// Add 1-3 spaces between words
			spaces := rand.Intn(3) + 1
			result.WriteString(strings.Repeat(" ", spaces))
		}
	}

	return result.String()
}
