package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"os"
	"strconv"
	"strings"
	"time"

	"hash/fnv"

	"github.com/go-redis/redis/v8"
)

// Models
type Rule struct {
	RuleID       string    `json:"rule_id"`
	Level        string    `json:"level"`        // global/account
	AuthID       string    `json:"auth_id"`      // account specific
	MessageType  string    `json:"message_type"` // sms/mms/all
	Keyword      string    `json:"keyword"`
	IsStandalone bool      `json:"is_standalone"` // New field
	Source       string    `json:"source"`
	Country      string    `json:"country"`     // country code for filtering
	CampaignID   string    `json:"campaign_id"` // campaign specific filtering
	Priority     int       `json:"priority"`
	ConfigType   string    `json:"config_type"` // drop/review
	CreatedAt    time.Time `json:"created_at"`
}

type MessageContext struct {
	Content            string
	Source             string
	AccountID          string
	DestinationCountry string
	MessageType        string
	CampaignID         string
}

// Trie implementation
type TrieNode struct {
	children map[rune]*TrieNode
	ruleIDs  map[string]struct{}
	isEnd    bool
}

func NewTrieNode() *TrieNode {
	return &TrieNode{
		children: make(map[rune]*TrieNode),
		ruleIDs:  make(map[string]struct{}),
	}
}

type RuleTrie struct {
	root  *TrieNode
	rules map[string]Rule // Reference to rules map
}

func NewRuleTrie(rules map[string]Rule) *RuleTrie {
	return &RuleTrie{
		root:  NewTrieNode(),
		rules: rules,
	}
}

func (t *RuleTrie) Insert(pattern string, ruleID string) {
	node := t.root
	for _, ch := range pattern {
		if _, exists := node.children[ch]; !exists {
			node.children[ch] = NewTrieNode()
		}
		node = node.children[ch]
		node.ruleIDs[ruleID] = struct{}{}
	}
	node.isEnd = true
}

func (t *RuleTrie) Delete(ruleID string) {
	var deleteFromNode func(*TrieNode)
	deleteFromNode = func(node *TrieNode) {
		delete(node.ruleIDs, ruleID)
		for _, child := range node.children {
			deleteFromNode(child)
		}
	}
	deleteFromNode(t.root)
}

func (t *RuleTrie) FindMatches(text string) map[string]struct{} {
	matches := make(map[string]struct{})
	text = strings.ToLower(text)

	// Define punctuation characters to clean
	punctuation := []string{".", ",", "?", "!", ":", ";", ")", "]", "}", "'", "\""}

	// Split text into words for standalone matching
	words := strings.Fields(text)

	// For standalone matches - check each word exactly
	for _, word := range words {
		// Clean punctuation from end of word
		cleanWord := word
		for _, p := range punctuation {
			cleanWord = strings.TrimSuffix(cleanWord, p)
		}

		// Check for exact word matches (standalone)
		currentNode := t.root
		for _, ch := range cleanWord {
			if next, exists := currentNode.children[ch]; exists {
				currentNode = next
			} else {
				break
			}
		}
		// Check if we have any matches at this node
		for ruleID := range currentNode.ruleIDs {
			if rule, exists := t.getRule(ruleID); exists {
				if rule.IsStandalone && strings.ToLower(rule.Keyword) == cleanWord {
					matches[ruleID] = struct{}{}
				}
			}
		}
	}

	// For non-standalone matches - check entire text
	for i := 0; i < len(text); i++ {
		currentNode := t.root // Start from root for each position
		for j := i; j < len(text); j++ {
			ch := rune(text[j])
			if next, exists := currentNode.children[ch]; exists {
				currentNode = next
				// Check if any rules match at this point
				for ruleID := range currentNode.ruleIDs {
					if rule, exists := t.getRule(ruleID); exists {
						if !rule.IsStandalone && strings.Contains(text, strings.ToLower(rule.Keyword)) {
							matches[ruleID] = struct{}{}
						}
					}
				}
			} else {
				break
			}
		}
	}

	return matches
}

func (t *RuleTrie) getRule(ruleID string) (Rule, bool) {
	rule, exists := t.rules[ruleID]
	return rule, exists
}

// Optimized Trie implementation
type CompressedTrieNode struct {
	segment  string
	children map[byte]*CompressedTrieNode
	ruleIDs  map[string]struct{}
}

type OptimizedTrie struct {
	root  *CompressedTrieNode
	bloom *BloomFilter
}

func NewOptimizedTrie() *OptimizedTrie {
	return &OptimizedTrie{
		root: &CompressedTrieNode{
			children: make(map[byte]*CompressedTrieNode),
			ruleIDs:  make(map[string]struct{}),
		},
		bloom: NewBloomFilter(1000, 0.01), // Size and false positive rate
	}
}

func (t *OptimizedTrie) Insert(word string, ruleID string) {
	t.bloom.Add(word)
	current := t.root
	start := 0

	for start < len(word) {
		if len(current.children) == 0 {
			// Compress remaining characters
			node := &CompressedTrieNode{
				segment:  word[start:],
				children: make(map[byte]*CompressedTrieNode),
				ruleIDs:  map[string]struct{}{ruleID: {}},
			}
			current.children[word[start]] = node
			return
		}

		ch := word[start]
		if next, exists := current.children[ch]; exists {
			// Find common prefix
			i := 0
			for i < len(next.segment) && start+i < len(word) &&
				word[start+i] == next.segment[i] {
				i++
			}

			if i < len(next.segment) {
				// Split node
				newNode := &CompressedTrieNode{
					segment:  next.segment[i:],
					children: next.children,
					ruleIDs:  next.ruleIDs,
				}
				next.segment = next.segment[:i]
				next.children = map[byte]*CompressedTrieNode{}
				next.children[newNode.segment[0]] = newNode
			}

			current = next
			start += i
		} else {
			// Create new node
			node := &CompressedTrieNode{
				segment:  word[start:],
				children: make(map[byte]*CompressedTrieNode),
				ruleIDs:  map[string]struct{}{ruleID: {}},
			}
			current.children[ch] = node
			return
		}
	}

	current.ruleIDs[ruleID] = struct{}{}
}

// Bloom Filter implementation
type BloomFilter struct {
	bits []bool
	k    int // Number of hash functions
	m    int // Size of bit array
}

func NewBloomFilter(expectedItems int, falsePositiveRate float64) *BloomFilter {
	// Calculate optimal size and number of hash functions
	m := -int(float64(expectedItems) * math.Log(falsePositiveRate) / math.Pow(math.Log(2), 2))
	k := int(float64(m) / float64(expectedItems) * math.Log(2))

	return &BloomFilter{
		bits: make([]bool, m),
		k:    k,
		m:    m,
	}
}

func (bf *BloomFilter) hash(s string, i int) uint {
	h := fnv.New64a()
	h.Write([]byte(s))
	h.Write([]byte{byte(i)})
	return uint(h.Sum64()) % uint(bf.m)
}

func (bf *BloomFilter) Add(s string) {
	for i := 0; i < bf.k; i++ {
		pos := bf.hash(s, i)
		bf.bits[pos] = true
	}
}

func (bf *BloomFilter) MightContain(s string) bool {
	for i := 0; i < bf.k; i++ {
		pos := bf.hash(s, i)
		if !bf.bits[pos] {
			return false
		}
	}
	return true
}

// Rule Engine
type RuleEngine struct {
	redis       *redis.Client
	keywordTrie *RuleTrie
	rules       map[string]Rule
	pubsub      *redis.PubSub
	ctx         context.Context
	cancel      context.CancelFunc
}

type RuleUpdate struct {
	Type    string `json:"type"` // "add", "modify", "delete"
	Rule    Rule   `json:"rule"`
	Version string `json:"version"` // For versioning updates
}

func (re *RuleEngine) LoadRulesFromCache(ctx context.Context) error {
	// Get all rules from Redis hash
	rules, err := re.redis.HGetAll(ctx, "rules:hash").Result()
	if err != nil {
		return fmt.Errorf("failed to load rules from cache: %v", err)
	}

	// Load each rule into local state
	for _, ruleJSON := range rules {
		var rule Rule
		if err := json.Unmarshal([]byte(ruleJSON), &rule); err != nil {
			log.Printf("Error unmarshaling rule: %v", err)
			continue
		}

		// Add to local state
		re.rules[rule.RuleID] = rule

		// Add to trie if it has keyword
		if rule.Keyword != "" {
			re.keywordTrie.Insert(strings.ToLower(rule.Keyword), rule.RuleID)
		}
	}

	log.Printf("Loaded %d rules from cache", len(rules))
	return nil
}

func (re *RuleEngine) StartPeriodicSync() {
	// Run full sync every 4 hours
	ticker := time.NewTicker(4 * time.Hour)
	go func() {
		for {
			select {
			case <-re.ctx.Done():
				ticker.Stop()
				return
			case <-ticker.C:
				log.Println("Starting periodic full sync of rules...")
				ctx := context.Background()

				// Clear local state
				re.rules = make(map[string]Rule)
				re.keywordTrie = NewRuleTrie(re.rules)

				// Reload all rules
				if err := re.LoadRulesFromCache(ctx); err != nil {
					log.Printf("Error during periodic sync: %v", err)
				} else {
					log.Printf("Periodic sync completed successfully. Total rules: %d", len(re.rules))
				}
			}
		}
	}()
}

func NewRuleEngine(redisClient *redis.Client) *RuleEngine {
	ctx, cancel := context.WithCancel(context.Background())
	rules := make(map[string]Rule)
	re := &RuleEngine{
		redis:       redisClient,
		keywordTrie: NewRuleTrie(rules),
		rules:       rules,
		pubsub:      redisClient.Subscribe(ctx, "rule-updates"),
		ctx:         ctx,
		cancel:      cancel,
	}

	// Load existing rules from cache
	if err := re.LoadRulesFromCache(ctx); err != nil {
		log.Printf("Error loading rules from cache: %v", err)
	}

	// Start listening for updates
	go re.listenForUpdates()

	// Start periodic sync
	re.StartPeriodicSync()

	return re
}

func (re *RuleEngine) AddRule(rule Rule) error {
	re.rules[rule.RuleID] = rule
	if rule.Keyword != "" {
		re.keywordTrie.Insert(strings.ToLower(rule.Keyword), rule.RuleID)
	}

	ctx := context.Background()
	pipe := re.redis.Pipeline()

	// Store complete rule
	ruleJSON, err := json.Marshal(rule)
	if err != nil {
		return err
	}
	pipe.HSet(ctx, "rules:hash", rule.RuleID, ruleJSON)

	// Index by level and additional parameters
	if rule.Level == "global" {
		pipe.ZAdd(ctx, "rules:global", &redis.Z{
			Score:  float64(rule.Priority),
			Member: rule.RuleID,
		})
	} else {
		// Index by account
		accountKey := fmt.Sprintf("rules:account:%s", rule.AuthID)
		pipe.ZAdd(ctx, accountKey, &redis.Z{
			Score:  float64(rule.Priority),
			Member: rule.RuleID,
		})

		// Additional indexes for account-level filtering
		if rule.Country != "" {
			accountCountryKey := fmt.Sprintf("rules:account:%s:country:%s", rule.AuthID, rule.Country)
			pipe.SAdd(ctx, accountCountryKey, rule.RuleID)
		}

		if rule.CampaignID != "" {
			accountCampaignKey := fmt.Sprintf("rules:account:%s:campaign:%s", rule.AuthID, rule.CampaignID)
			pipe.SAdd(ctx, accountCampaignKey, rule.RuleID)
		}
	}

	// Create update notification
	update := RuleUpdate{
		Type:    "add",
		Rule:    rule,
		Version: time.Now().UTC().Format(time.RFC3339Nano),
	}

	updateJSON, err := json.Marshal(update)
	if err != nil {
		return err
	}
	pipe.Publish(ctx, "rule-updates", updateJSON)

	_, err = pipe.Exec(ctx)
	return err
}

func (re *RuleEngine) DeleteRule(ruleID string) error {
	ctx := context.Background()
	pipe := re.redis.Pipeline()

	// Remove from Redis hash
	pipe.HDel(ctx, "rules:hash", ruleID)

	// Get the rule before deleting
	rule, exists := re.rules[ruleID]
	if exists {
		// Remove from appropriate indexes
		if rule.Level == "global" {
			pipe.ZRem(ctx, "rules:global", ruleID)
		} else {
			// Remove from account index
			accountKey := fmt.Sprintf("rules:account:%s", rule.AuthID)
			pipe.ZRem(ctx, accountKey, ruleID)

			// Remove from country index if exists
			if rule.Country != "" {
				accountCountryKey := fmt.Sprintf("rules:account:%s:country:%s", rule.AuthID, rule.Country)
				pipe.SRem(ctx, accountCountryKey, ruleID)
			}

			// Remove from campaign index if exists
			if rule.CampaignID != "" {
				accountCampaignKey := fmt.Sprintf("rules:account:%s:campaign:%s", rule.AuthID, rule.CampaignID)
				pipe.SRem(ctx, accountCampaignKey, ruleID)
			}
		}

		// Remove from local state
		delete(re.rules, ruleID)
		re.keywordTrie.Delete(ruleID)
	}

	// Create and publish delete notification
	update := RuleUpdate{
		Type:    "delete",
		Rule:    Rule{RuleID: ruleID},
		Version: time.Now().UTC().Format(time.RFC3339Nano),
	}

	updateJSON, err := json.Marshal(update)
	if err != nil {
		return err
	}
	pipe.Publish(ctx, "rule-updates", updateJSON)

	_, err = pipe.Exec(ctx)
	return err
}

func (re *RuleEngine) listenForUpdates() {
	for {
		select {
		case <-re.ctx.Done():
			return
		default:
			msg, err := re.pubsub.ReceiveMessage(re.ctx)
			if err != nil {
				log.Printf("Error receiving message: %v", err)
				continue
			}

			var update RuleUpdate
			if err := json.Unmarshal([]byte(msg.Payload), &update); err != nil {
				log.Printf("Error unmarshaling update: %v", err)
				continue
			}

			switch update.Type {
			case "add", "modify":
				// Skip if this container published the update
				if _, exists := re.rules[update.Rule.RuleID]; exists {
					continue
				}
				re.rules[update.Rule.RuleID] = update.Rule
				if update.Rule.Keyword != "" {
					re.keywordTrie.Insert(strings.ToLower(update.Rule.Keyword), update.Rule.RuleID)
				}
				log.Printf("Added/Modified rule: %s", update.Rule.RuleID)

			case "delete":
				// Remove from local state
				if _, exists := re.rules[update.Rule.RuleID]; exists {
					delete(re.rules, update.Rule.RuleID)
					re.keywordTrie.Delete(update.Rule.RuleID)
					log.Printf("Deleted rule: %s", update.Rule.RuleID)
				}
			}
		}
	}
}

// Clean up when shutting down
func (re *RuleEngine) Close() {
	re.cancel() // Stop the update listener
	re.pubsub.Close()
}

func (re *RuleEngine) EvaluateMessage(ctx context.Context, msgCtx *MessageContext) (*Rule, error) {
	matchingRuleIDs := make(map[string]struct{})

	if msgCtx.Content != "" {
		// Directly use Trie matching
		matches := re.keywordTrie.FindMatches(msgCtx.Content)
		for ruleID := range matches {
			matchingRuleIDs[ruleID] = struct{}{}
		}

		// Check for non-keyword based rules
		for _, rule := range re.rules {
			if rule.Keyword == "" && // No keyword
				(rule.Level == "global" || rule.AuthID == msgCtx.AccountID) {
				matchingRuleIDs[rule.RuleID] = struct{}{}
			}
		}
	}

	// Process matching rules
	var matchingRules []Rule
	for ruleID := range matchingRuleIDs {
		rule := re.rules[ruleID]

		// Check if rule applies to this account
		if rule.Level == "global" {
			matchingRules = append(matchingRules, rule)
			continue
		}

		// For account-level rules, check all conditions
		if rule.AuthID == msgCtx.AccountID {
			if rule.Country != "" && rule.Country != msgCtx.DestinationCountry {
				continue
			}
			if rule.CampaignID != "" && rule.CampaignID != msgCtx.CampaignID {
				continue
			}
			if rule.Source != "" && rule.Source != msgCtx.Source {
				continue
			}
			if rule.MessageType != "all" && rule.MessageType != msgCtx.MessageType {
				continue
			}
			matchingRules = append(matchingRules, rule)
		}
	}

	if len(matchingRules) == 0 {
		return nil, nil
	}

	// Return highest priority rule
	bestRule := matchingRules[0]
	for _, rule := range matchingRules[1:] {
		if rule.Priority < bestRule.Priority {
			bestRule = rule
		}
	}

	return &bestRule, nil
}

func main() {
	// Get instance ID from command line args
	if len(os.Args) < 2 {
		log.Fatal("Please provide instance ID (1, 2, 3, etc.)")
	}
	instanceID := os.Args[1]

	// Initialize Redis client
	rdb := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
	})
	defer rdb.Close()

	// Create rule engine
	engine := NewRuleEngine(rdb)
	defer engine.Close()

	// Create channels for test messages and termination
	msgChan := make(chan MessageContext)
	done := make(chan struct{})

	// Start a goroutine to handle user input for rule management
	go func() {
		reader := bufio.NewReader(os.Stdin)
		for {
			fmt.Printf("\nInstance %s - Commands:\n", instanceID)
			fmt.Println("1: Add new rule")
			fmt.Println("2: Delete rule")
			fmt.Println("3: Send test message")
			fmt.Println("4: Print current rules")
			fmt.Println("5: Exit")

			text, _ := reader.ReadString('\n')
			text = strings.TrimSpace(text)

			switch text {
			case "1":
				// Interactive rule addition
				fmt.Println("\nAdding new rule:")

				// Generate rule ID
				ruleID := fmt.Sprintf("R%s_%d", instanceID, time.Now().Unix())
				fmt.Printf("Rule ID will be: %s\n", ruleID)

				// Get rule level
				fmt.Print("Enter rule level (global/account): ")
				level, _ := reader.ReadString('\n')
				level = strings.TrimSpace(level)

				// Validate level
				if level != "global" && level != "account" {
					log.Printf("Invalid rule level. Must be 'global' or 'account'")
					continue
				}

				// Get account ID if account level
				var authID string
				var priority int
				if level == "account" {
					fmt.Print("Enter account ID: ")
					authID, _ = reader.ReadString('\n')
					authID = strings.TrimSpace(authID)

					// Get priority for account-level rules
					fmt.Print("Enter priority (any integer, lower value = higher priority): ")
					priorityStr, _ := reader.ReadString('\n')
					var err error
					priority, err = strconv.Atoi(strings.TrimSpace(priorityStr))
					if err != nil {
						log.Printf("Invalid priority value: %v", err)
						continue
					}
				} else {
					// For global rules, set priority to 0 (highest)
					priority = 0
				}

				// Get message type
				fmt.Print("Enter message type (sms/mms/all): ")
				messageType, _ := reader.ReadString('\n')
				messageType = strings.TrimSpace(messageType)

				// Get keyword
				fmt.Print("Enter keyword (or leave empty): ")
				keyword, _ := reader.ReadString('\n')
				keyword = strings.TrimSpace(keyword)

				// Get source
				fmt.Print("Enter source (or leave empty): ")
				source, _ := reader.ReadString('\n')
				source = strings.TrimSpace(source)

				// Get country
				fmt.Print("Enter country code (or leave empty): ")
				country, _ := reader.ReadString('\n')
				country = strings.TrimSpace(country)

				// Get campaign ID
				fmt.Print("Enter campaign ID (or leave empty): ")
				campaignID, _ := reader.ReadString('\n')
				campaignID = strings.TrimSpace(campaignID)

				// Get config type
				fmt.Print("Enter config type (drop/review): ")
				configType, _ := reader.ReadString('\n')
				configType = strings.TrimSpace(configType)

				// Add standalone flag
				fmt.Print("Is this a standalone keyword? (true/false): ")
				isStandaloneStr, _ := reader.ReadString('\n')
				isStandalone := strings.TrimSpace(isStandaloneStr) == "true"

				rule := Rule{
					RuleID:       ruleID,
					Level:        level,
					AuthID:       authID,
					MessageType:  messageType,
					Keyword:      keyword,
					IsStandalone: isStandalone,
					Source:       source,
					Country:      country,
					CampaignID:   campaignID,
					Priority:     priority,
					ConfigType:   configType,
					CreatedAt:    time.Now(),
				}

				// Validate rule before adding
				if rule.Level == "global" && rule.Priority != 0 {
					log.Printf("Global rules must have priority 0")
					continue
				}

				if err := engine.AddRule(rule); err != nil {
					log.Printf("Failed to add rule: %v", err)
				} else {
					log.Printf("Successfully added rule: %s", rule.RuleID)
					if rule.Level == "global" {
						log.Printf("Global rule added with highest priority (0)")
					} else {
						log.Printf("Account rule added with priority: %d", rule.Priority)
					}
				}

			case "2":
				// Delete rule
				fmt.Print("Enter rule ID to delete: ")
				ruleID, _ := reader.ReadString('\n')
				ruleID = strings.TrimSpace(ruleID)

				if err := engine.DeleteRule(ruleID); err != nil {
					log.Printf("Failed to delete rule: %v", err)
				} else {
					log.Printf("Deleted rule: %s", ruleID)
				}

			case "3":
				// Interactive message testing
				fmt.Println("\nSending test message:")

				fmt.Print("Enter message content: ")
				content, _ := reader.ReadString('\n')
				content = strings.TrimSpace(content)

				fmt.Print("Enter source number: ")
				source, _ := reader.ReadString('\n')
				source = strings.TrimSpace(source)

				fmt.Print("Enter account ID: ")
				accountID, _ := reader.ReadString('\n')
				accountID = strings.TrimSpace(accountID)

				fmt.Print("Enter destination country: ")
				country, _ := reader.ReadString('\n')
				country = strings.TrimSpace(country)

				fmt.Print("Enter message type (sms/mms): ")
				messageType, _ := reader.ReadString('\n')
				messageType = strings.TrimSpace(messageType)

				fmt.Print("Enter campaign ID: ")
				campaignID, _ := reader.ReadString('\n')
				campaignID = strings.TrimSpace(campaignID)

				msg := MessageContext{
					Content:            content,
					Source:             source,
					AccountID:          accountID,
					DestinationCountry: country,
					MessageType:        messageType,
					CampaignID:         campaignID,
				}
				msgChan <- msg

			case "4":
				// Print current rules
				fmt.Println("\nCurrent Rules:")
				for _, rule := range engine.rules {
					fmt.Printf("Rule %s:\n", rule.RuleID)
					fmt.Printf("  Level: %s\n", rule.Level)
					fmt.Printf("  Account: %s\n", rule.AuthID)
					fmt.Printf("  Message Type: %s\n", rule.MessageType)
					fmt.Printf("  Keyword: %s\n", rule.Keyword)
					fmt.Printf("  Is Standalone: %v\n", rule.IsStandalone)
					fmt.Printf("  Source: %s\n", rule.Source)
					fmt.Printf("  Country: %s\n", rule.Country)
					fmt.Printf("  Campaign: %s\n", rule.CampaignID)
					fmt.Printf("  Priority: %d\n", rule.Priority)
					fmt.Printf("  Action: %s\n", rule.ConfigType)
					fmt.Println()
				}

			case "5":
				log.Printf("Shutting down instance %s...", instanceID)
				close(done)
				return

			default:
				fmt.Println("Invalid command")
			}
		}
	}()

	// Start a goroutine to handle test messages
	go func() {
		for {
			select {
			case msg := <-msgChan:
				ctx := context.Background()
				matchingRule, err := engine.EvaluateMessage(ctx, &msg)
				if err != nil {
					log.Printf("Error evaluating message: %v", err)
					continue
				}

				if matchingRule != nil {
					log.Printf("\nInstance %s - Message matched rule: %s\n", instanceID, matchingRule.RuleID)
					log.Printf("Priority: %d\n", matchingRule.Priority)
					log.Printf("Action: %s\n", matchingRule.ConfigType)
					if matchingRule.Keyword != "" {
						log.Printf("Matched keyword: %s\n", matchingRule.Keyword)
					}
				} else {
					log.Printf("\nInstance %s - No matching rule found", instanceID)
				}
			case <-done:
				return
			}
		}
	}()

	// Wait for termination signal
	<-done
	log.Printf("Instance %s terminated", instanceID)
}
