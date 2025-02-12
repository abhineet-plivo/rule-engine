# Rule Engine with Redis Backend

A high-performance, distributed rule engine that processes messages based on configurable rules. The engine uses Redis for rule storage and real-time synchronization across multiple instances.

## Features

### Rule Management
- **Rule Types**
  - Global Rules (highest priority, always priority 0)
  - Account-specific Rules (configurable priority)
- **Rule Matching Criteria**
  - Keywords (with punctuation handling)
  - Message Type (SMS/MMS)
  - Source Number
  - Destination Country
  - Campaign ID
  - Account ID

### Performance Optimizations
- **Bloom Filter**
  - Quick rejection of non-matching messages
  - Reduces unnecessary Trie traversals
- **Trie-based Keyword Matching**
  - Efficient string matching
  - Handles punctuation variations
  - Memory-efficient storage

### Distributed Architecture
- **Redis Backend**
  - Persistent rule storage
  - Real-time rule synchronization
  - Note: Pub/Sub functionality requires non-clustered Redis setup
- **Multiple Instance Support**
  - Real-time updates via Redis Pub/Sub
  - Periodic full sync (every 4 hours)
  - Automatic recovery mechanisms

## Prerequisites

- Go 1.16 or higher
- Redis 6.x or higher (non-clustered setup)
- `github.com/go-redis/redis/v8` package

## Installation

# Clone the repository
```bash
git clone https://github.com/abhineet-plivo/rule-engine.git
cd rule-engine
```

# Install dependencies
```bash
go mod tidy
```

# Run the engine
```bash
go run main.go [instance-id]
# Example: go run main.go 1
```
## Rule Configuration

### Global Rules
- Automatically assigned priority 0 (highest)
- Apply to all accounts
- Example:
  ```
  Level: global
  Keyword: spam
  Action: drop
  ```

### Account Rules
- Custom priority (any integer, lower = higher priority)
- Account-specific filtering
- Example:
  ```
  Level: account
  Account: ACC123
  Priority: 100
  Country: US
  Campaign: CAMP1
  Action: review
  ```

## Architecture Details

### Rule Storage
- **Redis Hash**: `rules:hash`
  - Stores complete rule definitions
- **Sorted Sets**
  - `rules:global`: Global rules
  - `rules:account:{id}`: Account-specific rules
- **Sets**
  - `rules:account:{id}:country:{country}`
  - `rules:account:{id}:campaign:{campaign}`

### Synchronization
- **Pub/Sub**
  - Channel: `rule-updates`
  - Real-time rule modifications
  - Requires non-clustered Redis setup
- **Periodic Sync**
  - Full state sync every 4 hours
  - Ensures consistency across instances
  - Recovery mechanism for missed updates

### Performance
- **Bloom Filter**
  - False positive rate: 1%
  - Quick message filtering
- **Trie Structure**
  - O(m) keyword matching (m = keyword length)
  - Memory-efficient pattern storage

## Important Notes

1. **Redis Setup**
   - Pub/Sub functionality requires a non-clustered Redis setup
   - Cannot use Redis Cluster for this implementation

2. **Rule Priority**
   - Global rules: Always priority 0
   - Account rules: Any valid integer (lower = higher priority)

3. **Recovery Mechanism**
   - Periodic full sync every 4 hours
   - Automatic state recovery
   - Manual reload capability

## Best Practices

1. **Rule Management**
   - Use global rules sparingly
   - Set appropriate priorities for account rules
   - Regular cleanup of unused rules

2. **Performance**
   - Monitor Redis memory usage
   - Regular maintenance of rule sets
   - Proper error handling in applications

3. **Monitoring**
   - Watch for sync failures
   - Monitor rule addition/deletion
   - Track rule matching patterns

## Limitations

1. **Redis Requirements**
   - Non-clustered setup needed for Pub/Sub
   - Single Redis instance recommended

2. **Rule Updates**
   - May have brief inconsistency between instances
   - Resolved by periodic full sync

3. **Scaling**
   - Limited by Redis single instance capacity
   - Consider message volume when scaling

## Contributing

Please read CONTRIBUTING.md for details on our code of conduct and the process for submitting pull requests.

## License

This project is licensed under the MIT License - see the LICENSE.md file for details