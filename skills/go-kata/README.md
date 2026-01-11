# Go Kata Skill

A comprehensive Claude Code skill containing idiomatic Go patterns extracted from 20 production-grade coding challenges.

## What is this?

This skill embeds deep knowledge of idiomatic Go programming patterns learned from the [go-kata repository](https://github.com/MedUnes/go-kata). It teaches Claude how to write production-ready Go code following best practices discovered through real-world failure modes.

## What patterns does it cover?

The skill contains **50+ idiomatic patterns** organized into 10 categories:

1. **Context & Cancellation** - errgroup, fail-fast, channel coordination, goroutine leak prevention
2. **Concurrency Patterns** - rate limiting, bounded concurrency, singleflight, worker pools
3. **Performance & Allocation** - sync.Pool, streaming I/O, zero-allocation hot paths, sharded locks
4. **HTTP & Middleware** - client hygiene, connection pooling, small interfaces, middleware composition
5. **Error Handling** - wrapping, typed errors, aggregation, retry logic, defer cleanup
6. **Graceful Shutdown** - signal handling, reverse dependency order, context propagation
7. **Configuration** - functional options pattern, dependency injection
8. **Filesystem** - fs.FS abstraction, embed with build tags
9. **Testing** - table-driven tests, parallel subtests, fuzz testing, fstest
10. **Logging** - structured logging with slog, context-aware logging

## How to use this skill

### Option 1: Manual Reference

Read `skill.md` to understand idiomatic Go patterns and apply them when writing code.

### Option 2: Claude Code Skill

If using Claude Code's skill system, this skill will automatically guide code generation and reviews to follow these patterns.

### Option 3: Code Review Checklist

Use the "Quick Reference Checklist" section in `skill.md` to review Go code for common anti-patterns.

## Key Principles

This skill enforces:

- ✅ **Fail Fast** - Cancel everything on first error
- ✅ **Context is King** - Thread context.Context through entire call chain
- ✅ **No Goroutine Leaks** - Every goroutine must have controlled shutdown
- ✅ **Explicit over Implicit** - Own your lifecycle, error handling, resources
- ✅ **Composition over Inheritance** - Small interfaces, not bloated hierarchies
- ✅ **Zero Allocation Hot Paths** - Verify with benchmarks
- ✅ **Production Safety** - Never use DefaultClient, always handle cleanup

## Common Anti-Patterns Prevented

- ❌ Using `sync.WaitGroup` instead of `errgroup` for error propagation
- ❌ Using `time.Sleep` for rate limiting or retries
- ❌ Using `http.DefaultClient` without timeouts
- ❌ Forgetting to close/drain HTTP response bodies
- ❌ Returning typed nil pointers
- ❌ Using `%v` instead of `%w` for error wrapping
- ❌ Creating goroutines without shutdown paths
- ❌ Using `bufio.Scanner` for lines that can exceed 64K
- ❌ Package-level global state
- ❌ Unstructured logging

## Example: Before and After

### Before (Unidiomatic)
```go
func FetchUsers(ids []int) ([]User, error) {
    var wg sync.WaitGroup
    users := make([]User, len(ids))

    for i, id := range ids {
        wg.Add(1)
        go func(idx, userID int) {
            defer wg.Done()
            resp, _ := http.Get(fmt.Sprintf("http://api/users/%d", userID))
            json.NewDecoder(resp.Body).Decode(&users[idx])
        }(i, id)
    }

    wg.Wait()
    return users, nil
}
```

**Problems:**
- No context cancellation
- Errors are lost
- No fail-fast
- Uses http.DefaultClient (no timeout)
- Response body never closed
- No error handling

### After (Idiomatic - Following Skill Patterns)
```go
var httpClient = &http.Client{
    Timeout: 30 * time.Second,
    Transport: &http.Transport{
        MaxIdleConns: 100,
        IdleConnTimeout: 90 * time.Second,
    },
}

func FetchUsers(ctx context.Context, ids []int) ([]User, error) {
    g, ctx := errgroup.WithContext(ctx)
    users := make([]User, len(ids))

    for i, id := range ids {
        i, id := i, id  // Capture loop vars
        g.Go(func() error {
            req, err := http.NewRequestWithContext(ctx, "GET",
                fmt.Sprintf("http://api/users/%d", id), nil)
            if err != nil {
                return fmt.Errorf("create request for user %d: %w", id, err)
            }

            resp, err := httpClient.Do(req)
            if err != nil {
                return fmt.Errorf("fetch user %d: %w", id, err)
            }
            defer resp.Body.Close()

            if resp.StatusCode != http.StatusOK {
                io.CopyN(io.Discard, resp.Body, 512)  // Drain for reuse
                return fmt.Errorf("user %d: HTTP %d", id, resp.StatusCode)
            }

            if err := json.NewDecoder(resp.Body).Decode(&users[i]); err != nil {
                return fmt.Errorf("decode user %d: %w", id, err)
            }

            return nil
        })
    }

    if err := g.Wait(); err != nil {
        return nil, err
    }
    return users, nil
}
```

**Improvements:**
- ✅ Context-aware with `context.Context` first parameter
- ✅ Fail-fast with `errgroup` - cancels on first error
- ✅ Configured HTTP client with timeout
- ✅ `NewRequestWithContext` for cancellation
- ✅ Response body closed and drained
- ✅ Error wrapping with `%w`
- ✅ Proper loop variable capture
- ✅ Transport reuse for connection pooling

## Testing the Skill

Create a simple test to verify the patterns:

```bash
# Create test file
cat > test_patterns.go <<'EOF'
package main

import (
    "context"
    "time"
    "golang.org/x/sync/errgroup"
)

// Test: Does this follow the skill patterns?
func fetchData(ctx context.Context, urls []string) error {
    g, ctx := errgroup.WithContext(ctx)

    for _, url := range urls {
        url := url  // Capture
        g.Go(func() error {
            return fetch(ctx, url)
        })
    }

    return g.Wait()
}
EOF

# Verify it compiles
go mod init test 2>/dev/null || true
go get golang.org/x/sync/errgroup
go build test_patterns.go
```

## Credits

Based on the excellent [go-kata repository](https://github.com/MedUnes/go-kata) by @MedUnes.

All patterns are extracted from production failure modes and real-world Go challenges designed to help experienced developers write idiomatic Go code.

## License

This skill is derived from the go-kata repository. Please refer to the original repository for licensing information.
