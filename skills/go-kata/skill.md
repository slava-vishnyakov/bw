# Go Kata: Idiomatic Go Patterns from go-kata Repository

This skill contains comprehensive idiomatic Go patterns extracted from 20 production-grade katas. Use these rules when writing, reviewing, or refactoring Go code to ensure production-ready, idiomatic implementations.

## Core Philosophy

- **Explicit over Implicit**: Go forces explicit lifecycle management, error handling, and resource control
- **Composition over Inheritance**: Small interfaces composed together, not class hierarchies
- **Fail Fast**: Cancel everything on first error; don't blindly wait for all goroutines
- **Context is King**: Thread `context.Context` through your entire call chain
- **Safety over Cleverness**: Predictable, testable code beats "smart" tricks
- **Own Your Lifecycle**: Every goroutine you spawn must have a controlled shutdown path

---

## 1. Context & Cancellation

### Rule 1.1: Context as First Parameter
✅ **ALWAYS** accept `context.Context` as the **first parameter** in methods that:
- Perform I/O operations
- Make network requests
- Start goroutines
- Execute operations that can be canceled

```go
// ✅ CORRECT
func (s *Service) FetchData(ctx context.Context, id int) (Data, error)

// ❌ WRONG
func (s *Service) FetchData(id int) (Data, error)
func (s *Service) FetchData(id int, ctx context.Context) (Data, error)  // Wrong position
```

### Rule 1.2: Use errgroup, Not sync.WaitGroup
✅ **ALWAYS** use `golang.org/x/sync/errgroup` for concurrent operations that can fail

❌ **NEVER** use `sync.WaitGroup` when you need error propagation or fail-fast behavior

```go
// ✅ CORRECT
import "golang.org/x/sync/errgroup"

func FetchAll(ctx context.Context, ids []int) error {
    g, ctx := errgroup.WithContext(ctx)
    for _, id := range ids {
        id := id // capture loop variable
        g.Go(func() error {
            return fetchOne(ctx, id)
        })
    }
    return g.Wait()  // Returns first error, cancels others
}

// ❌ WRONG - No error propagation, no fail-fast
func FetchAll(ctx context.Context, ids []int) error {
    var wg sync.WaitGroup
    for _, id := range ids {
        wg.Add(1)
        go func(id int) {
            defer wg.Done()
            fetchOne(ctx, id)  // Error lost!
        }(id)
    }
    wg.Wait()
    return nil
}
```

### Rule 1.3: Fail-Fast Pattern
When one operation fails, **cancel all remaining work immediately**

```go
// ✅ CORRECT - errgroup provides automatic fail-fast
g, ctx := errgroup.WithContext(ctx)
// When any goroutine returns error, ctx is canceled for all others
```

### Rule 1.4: Channel Coordination with struct{}, Not Booleans
✅ Use `chan struct{}` for signaling

❌ Don't use boolean flags for coordination

```go
// ✅ CORRECT
type Pool struct {
    stop chan struct{}
}

func (p *Pool) shutdown() {
    close(p.stop)
}

func (p *Pool) worker() {
    for {
        select {
        case <-p.stop:
            return
        case job := <-p.jobs:
            process(job)
        }
    }
}

// ❌ WRONG - Race conditions, no memory synchronization
type Pool struct {
    stopped bool
    mu sync.Mutex
}
```

### Rule 1.5: Select on Every Channel Send
When sending to channels, **ALWAYS** use select to respect context cancellation

```go
// ✅ CORRECT - Cancel-aware send
select {
case out <- result:
    // Sent successfully
case <-ctx.Done():
    return ctx.Err()
}

// ❌ WRONG - Blocks forever if receiver exits
out <- result  // Goroutine leak if out has no receiver!
```

### Rule 1.6: Channel Ownership - Only Sender Closes
The producer (sender) side owns the channel and is responsible for closing it

```go
// ✅ CORRECT
func produce(ctx context.Context) <-chan int {
    out := make(chan int)
    go func() {
        defer close(out)  // Producer closes
        for i := 0; i < 10; i++ {
            select {
            case out <- i:
            case <-ctx.Done():
                return
            }
        }
    }()
    return out
}

// ❌ WRONG - Receiver closing can cause panic
func consume(in <-chan int) {
    for range in {
    }
    close(in)  // PANIC: can't close receive-only channel
}
```

### Rule 1.7: No Goroutine Leaks
Every goroutine must have an exit path via context cancellation or done channel

```go
// ✅ CORRECT - Goroutine can exit
func startWorker(ctx context.Context) {
    go func() {
        ticker := time.NewTicker(1 * time.Second)
        defer ticker.Stop()
        for {
            select {
            case <-ticker.C:
                doWork()
            case <-ctx.Done():
                return  // Clean exit
            }
        }
    }()
}

// ❌ WRONG - Goroutine leaks forever
func startWorker() {
    go func() {
        for {
            time.Sleep(1 * time.Second)
            doWork()
        }
        // No exit path!
    }()
}
```

---

## 2. Concurrency Patterns

### Rule 2.1: Rate Limiting with x/time/rate
✅ Use `golang.org/x/time/rate.Limiter` for rate limiting

❌ **NEVER** use `time.Sleep` for rate limiting

```go
// ✅ CORRECT
import "golang.org/x/time/rate"

limiter := rate.NewLimiter(rate.Limit(10), 20)  // 10 req/s, burst 20

func makeRequest(ctx context.Context) error {
    if err := limiter.Wait(ctx); err != nil {
        return err
    }
    return doRequest()
}

// ❌ WRONG - Jittery, not context-aware, wasteful
func makeRequest(ctx context.Context) error {
    time.Sleep(100 * time.Millisecond)  // Bad!
    return doRequest()
}
```

### Rule 2.2: Bounded Concurrency with Semaphore
✅ Use `golang.org/x/sync/semaphore.Weighted` for max in-flight control

```go
// ✅ CORRECT
import "golang.org/x/sync/semaphore"

sem := semaphore.NewWeighted(8)  // Max 8 concurrent

func process(ctx context.Context, items []Item) error {
    g, ctx := errgroup.WithContext(ctx)
    for _, item := range items {
        item := item
        if err := sem.Acquire(ctx, 1); err != nil {
            return err
        }
        g.Go(func() error {
            defer sem.Release(1)
            return processItem(ctx, item)
        })
    }
    return g.Wait()
}
```

### Rule 2.3: Prevent Cache Stampede with singleflight
✅ Use `golang.org/x/sync/singleflight` to deduplicate in-flight loads

```go
// ✅ CORRECT
import "golang.org/x/sync/singleflight"

type Cache struct {
    sf singleflight.Group
}

func (c *Cache) Get(ctx context.Context, key string, loader func() (interface{}, error)) (interface{}, error) {
    // Use DoChan for context-aware waiting
    ch := c.sf.DoChan(key, func() (interface{}, error) {
        return loader()
    })

    select {
    case res := <-ch:
        return res.Val, res.Err
    case <-ctx.Done():
        return nil, ctx.Err()
    }
}

// ❌ WRONG - 200 goroutines trigger 200 loads
func (c *Cache) Get(ctx context.Context, key string, loader func() (interface{}, error)) (interface{}, error) {
    c.mu.RLock()
    val, ok := c.data[key]
    c.mu.RUnlock()
    if ok {
        return val, nil
    }

    // All goroutines execute loader concurrently - stampede!
    val, err := loader()
    c.mu.Lock()
    c.data[key] = val
    c.mu.Unlock()
    return val, err
}
```

### Rule 2.4: Worker Pools with Backpressure
Use bounded channels and `errors.Join` for error aggregation

```go
// ✅ CORRECT
import "errors"

func runPool(ctx context.Context, jobs <-chan Job, numWorkers int) error {
    var errs []error
    var mu sync.Mutex

    g, ctx := errgroup.WithContext(ctx)

    for i := 0; i < numWorkers; i++ {
        g.Go(func() error {
            for {
                select {
                case job, ok := <-jobs:
                    if !ok {
                        return nil
                    }
                    if err := job(ctx); err != nil {
                        mu.Lock()
                        errs = append(errs, err)
                        mu.Unlock()
                    }
                case <-ctx.Done():
                    return ctx.Err()
                }
            }
        })
    }

    if err := g.Wait(); err != nil {
        return err
    }
    return errors.Join(errs...)
}
```

### Rule 2.5: Proper Timer/Ticker Cleanup
✅ Always `defer ticker.Stop()` and use correct reset patterns

❌ **NEVER** use `time.Tick` (no stop control)

```go
// ✅ CORRECT
func scheduler(ctx context.Context, interval time.Duration, job func()) {
    ticker := time.NewTicker(interval)
    defer ticker.Stop()  // Critical!

    for {
        select {
        case <-ticker.C:
            job()
        case <-ctx.Done():
            return
        }
    }
}

// ❌ WRONG - Ticker leaks
func scheduler(ctx context.Context, interval time.Duration, job func()) {
    for {
        select {
        case <-time.Tick(interval):  // Leaks ticker!
            job()
        case <-ctx.Done():
            return
        }
    }
}
```

---

## 3. Performance & Allocation

### Rule 3.1: sync.Pool for Buffer Reuse
✅ Use `sync.Pool` for frequently allocated short-lived objects

**CRITICAL**: Always Reset() before returning to pool, and bound maximum sizes

```go
// ✅ CORRECT
var bufferPool = sync.Pool{
    New: func() interface{} {
        return new(bytes.Buffer)
    },
}

func processRequest(r *http.Request) error {
    buf := bufferPool.Get().(*bytes.Buffer)
    defer func() {
        // Bound memory: don't keep huge buffers
        if buf.Cap() > 64*1024 {
            return  // Let GC handle it
        }
        buf.Reset()  // Clear data
        bufferPool.Put(buf)
    }()

    // Use buf...
    return nil
}

// ❌ WRONG - Data leak, unbounded memory
func processRequest(r *http.Request) error {
    buf := bufferPool.Get().(*bytes.Buffer)
    defer bufferPool.Put(buf)  // No Reset()! Previous data leaks!

    // Use buf...
    return nil
}
```

### Rule 3.2: Streaming I/O with bufio.Reader
✅ Use `bufio.Reader` with `ReadSlice`/`ReadBytes` for large lines

❌ Don't use `bufio.Scanner` when lines can exceed 64K

```go
// ✅ CORRECT - Handles arbitrarily large lines
func readNDJSON(ctx context.Context, r io.Reader, handle func([]byte) error) error {
    br := bufio.NewReader(r)
    var line []byte

    for {
        select {
        case <-ctx.Done():
            return ctx.Err()
        default:
        }

        chunk, err := br.ReadSlice('\n')
        if err == bufio.ErrBufferFull {
            // Line too long, accumulate
            line = append(line, chunk...)
            continue
        }
        if err == io.EOF {
            if len(chunk) > 0 {
                line = append(line, chunk...)
                if err := handle(line); err != nil {
                    return err
                }
            }
            return nil
        }
        if err != nil {
            return err
        }

        line = append(line, chunk...)
        if err := handle(line[:len(line)-1]); err != nil {  // Remove \n
            return err
        }
        line = line[:0]  // Reuse slice
    }
}

// ❌ WRONG - Fails on lines > 64K
func readNDJSON(r io.Reader, handle func([]byte) error) error {
    scanner := bufio.NewScanner(r)
    for scanner.Scan() {
        handle(scanner.Bytes())  // Panics on long lines!
    }
    return scanner.Err()
}
```

### Rule 3.3: JSON Streaming, Not Full Unmarshal
✅ Use `json.Decoder` with streaming for large or selective parsing

❌ Don't unmarshal entire documents into `map[string]interface{}`

```go
// ✅ CORRECT - Stream parsing, low allocation
func parseStream(r io.Reader) error {
    dec := json.NewDecoder(r)
    for {
        var data struct {
            ID    string `json:"sensor_id"`
            Value float64 `json:"value"`
        }
        if err := dec.Decode(&data); err == io.EOF {
            return nil
        } else if err != nil {
            return err
        }
        process(data)
    }
}

// ❌ WRONG - Allocates for entire document, all fields
func parseStream(r io.Reader) error {
    var docs []map[string]interface{}
    if err := json.NewDecoder(r).Decode(&docs); err != nil {
        return err
    }
    for _, doc := range docs {
        // Lost type safety, massive allocations
    }
    return nil
}
```

### Rule 3.4: Sharded Locks for Concurrent Maps
✅ Use sharded `[]map[K]V` with `[]sync.RWMutex` for high-concurrency maps

❌ Don't use single `sync.Mutex` around map (bottleneck)

⚠️ Use `sync.Map` only for append-once, read-heavy specific cases

```go
// ✅ CORRECT
import "hash/fnv"

type ShardedMap[K comparable, V any] struct {
    shards []shard[K, V]
}

type shard[K comparable, V any] struct {
    mu sync.RWMutex
    m  map[K]V
}

func New[K comparable, V any](numShards int) *ShardedMap[K, V] {
    sm := &ShardedMap[K, V]{
        shards: make([]shard[K, V], numShards),
    }
    for i := range sm.shards {
        sm.shards[i].m = make(map[K]V)
    }
    return sm
}

func (sm *ShardedMap[K, V]) getShard(key K) *shard[K, V] {
    h := fnv.New64a()
    fmt.Fprintf(h, "%v", key)
    return &sm.shards[h.Sum64()%uint64(len(sm.shards))]
}

func (sm *ShardedMap[K, V]) Get(key K) (V, bool) {
    shard := sm.getShard(key)
    shard.mu.RLock()
    defer shard.mu.RUnlock()
    val, ok := shard.m[key]
    return val, ok
}

func (sm *ShardedMap[K, V]) Set(key K, val V) {
    shard := sm.getShard(key)
    shard.mu.Lock()
    defer shard.mu.Unlock()
    shard.m[key] = val
}

// ❌ WRONG - Bottleneck under high concurrency
type ConcurrentMap[K comparable, V any] struct {
    mu sync.Mutex
    m  map[K]V  // Single lock serializes everything!
}
```

### Rule 3.5: Zero Allocation Hot Paths
Verify with `go test -bench=. -benchmem` that hot paths have 0 allocs/op

```bash
# ✅ Goal
BenchmarkGet-8    10000000    100 ns/op    0 B/op    0 allocs/op
```

---

## 4. HTTP & Middleware

### Rule 4.1: Never Use http.DefaultClient
✅ **ALWAYS** create configured `http.Client` with timeouts

❌ **NEVER** use `http.DefaultClient` (no timeout = hangs forever)

```go
// ✅ CORRECT
var httpClient = &http.Client{
    Timeout: 30 * time.Second,
    Transport: &http.Transport{
        MaxIdleConns:        100,
        MaxIdleConnsPerHost: 10,
        IdleConnTimeout:     90 * time.Second,
        TLSHandshakeTimeout: 10 * time.Second,
    },
}

func fetch(ctx context.Context, url string) (*http.Response, error) {
    req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
    if err != nil {
        return nil, err
    }
    return httpClient.Do(req)
}

// ❌ WRONG - No timeout, no context
func fetch(url string) (*http.Response, error) {
    return http.Get(url)  // Uses DefaultClient - hangs forever!
}
```

### Rule 4.2: Use NewRequestWithContext
✅ **ALWAYS** use `http.NewRequestWithContext` for cancellation

```go
// ✅ CORRECT
req, err := http.NewRequestWithContext(ctx, "POST", url, body)

// ❌ WRONG - Can't be canceled
req, err := http.NewRequest("POST", url, body)
```

### Rule 4.3: Always Close and Drain Response Bodies
✅ `defer resp.Body.Close()` **AND** drain body for connection reuse

```go
// ✅ CORRECT
resp, err := client.Do(req)
if err != nil {
    return err
}
defer resp.Body.Close()

if resp.StatusCode != http.StatusOK {
    // Drain up to 512 bytes to allow connection reuse
    io.CopyN(io.Discard, resp.Body, 512)
    return fmt.Errorf("HTTP %d", resp.StatusCode)
}

// Process body...

// ❌ WRONG - Connection can't be reused
resp, err := client.Do(req)
if err != nil {
    return err
}
if resp.StatusCode != http.StatusOK {
    return fmt.Errorf("HTTP %d", resp.StatusCode)  // Didn't close or drain!
}
```

### Rule 4.4: Reuse Transport for Connection Pooling
✅ Create **ONE** `http.Client` and reuse it

❌ Don't create new Client/Transport per request

```go
// ✅ CORRECT - Package-level or long-lived client
var apiClient = &http.Client{
    Timeout: 30 * time.Second,
    Transport: &http.Transport{/* config */},
}

func makeRequest(ctx context.Context, url string) error {
    req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
    resp, err := apiClient.Do(req)  // Reuses connections
    // ...
}

// ❌ WRONG - Creates new transport every call = connection churn
func makeRequest(ctx context.Context, url string) error {
    client := &http.Client{
        Transport: &http.Transport{},  // New transport each time!
    }
    req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
    resp, err := client.Do(req)
    // ...
}
```

### Rule 4.5: Small Interfaces - Composition over Inheritance
✅ Define minimal interfaces with single methods

❌ Don't create bloated interfaces or base classes

```go
// ✅ CORRECT - Small, composable
type Processor interface {
    Process(context.Context, Event) ([]Event, error)
}

type middleware struct {
    next Processor
    // ... middleware-specific fields
}

func (m *middleware) Process(ctx context.Context, e Event) ([]Event, error) {
    // Pre-processing
    events, err := m.next.Process(ctx, e)
    // Post-processing
    return events, err
}

// ❌ WRONG - Interface pollution
type Processor interface {
    Process(context.Context, Event) ([]Event, error)
    SetDatabase(*sql.DB)  // Don't add methods all implementations don't need!
    SetLogger(*slog.Logger)
    SetMetrics(MetricsCollector)
}
```

---

## 5. Error Handling

### Rule 5.1: Error Wrapping with %w
✅ **ALWAYS** wrap errors with `%w` for error chain inspection

```go
// ✅ CORRECT
if err := fetch(ctx, url); err != nil {
    return fmt.Errorf("failed to fetch %s: %w", url, err)
}

// ❌ WRONG - Breaks error inspection
if err := fetch(ctx, url); err != nil {
    return fmt.Errorf("failed to fetch %s: %v", url, err)  // %v loses error chain!
}
```

### Rule 5.2: Custom Error Types with Is/As Support
✅ Create typed errors that work with `errors.Is()` and `errors.As()`

```go
// ✅ CORRECT
type AuthError struct {
    Op  string
    Err error
}

func (e *AuthError) Error() string {
    return fmt.Sprintf("auth error during %s: %v", e.Op, e.Err)
}

func (e *AuthError) Unwrap() error {
    return e.Err
}

// Usage
err := &AuthError{Op: "login", Err: context.DeadlineExceeded}
wrapped := fmt.Errorf("user login failed: %w", err)

var authErr *AuthError
if errors.As(wrapped, &authErr) {  // Works!
    log.Printf("Auth failed during: %s", authErr.Op)
}
```

### Rule 5.3: Never Return Typed Nil
✅ Return literal `nil`, not typed nil pointers

```go
// ✅ CORRECT
func DoThing() error {
    var err *MyError
    if somethingBad {
        err = &MyError{Op: "thing"}
        return err
    }
    return nil  // Literal nil
}

// ❌ WRONG - Typed nil trap
func DoThing() error {
    var err *MyError  // nil pointer
    if somethingBad {
        err = &MyError{Op: "thing"}
    }
    return err  // Returns non-nil interface with nil pointer!
}

// ✅ CORRECT FIX
func DoThing() error {
    var err *MyError
    if somethingBad {
        err = &MyError{Op: "thing"}
    }
    if err != nil {
        return err
    }
    return nil  // Explicit nil return
}
```

### Rule 5.4: Aggregate Errors with errors.Join
✅ Use `errors.Join` (Go 1.20+) to combine multiple errors

```go
// ✅ CORRECT
func cleanup() error {
    var errs []error

    if err := closeDB(); err != nil {
        errs = append(errs, fmt.Errorf("close db: %w", err))
    }
    if err := closeCache(); err != nil {
        errs = append(errs, fmt.Errorf("close cache: %w", err))
    }

    return errors.Join(errs...)  // Returns nil if errs is empty
}

// ❌ WRONG - Loses errors
func cleanup() error {
    var err error
    if e := closeDB(); e != nil {
        err = e  // Overwrites previous error!
    }
    if e := closeCache(); e != nil {
        err = e  // Lost DB error!
    }
    return err
}
```

### Rule 5.5: Context-Aware Retries with Timer
✅ Use `time.Timer` with `Reset()` for retries

❌ **NEVER** use `time.Sleep` (can't be canceled)

```go
// ✅ CORRECT
func retry(ctx context.Context, maxAttempts int, fn func(context.Context) error) error {
    var err error
    backoff := 100 * time.Millisecond
    timer := time.NewTimer(backoff)
    defer timer.Stop()

    for attempt := 0; attempt < maxAttempts; attempt++ {
        if err = fn(ctx); err == nil {
            return nil
        }

        if !isRetryable(err) {
            return fmt.Errorf("non-retryable error (attempt %d): %w", attempt+1, err)
        }

        if attempt < maxAttempts-1 {
            select {
            case <-timer.C:
                backoff *= 2
                timer.Reset(backoff)
            case <-ctx.Done():
                return fmt.Errorf("retry canceled after %d attempts: %w", attempt+1, ctx.Err())
            }
        }
    }
    return fmt.Errorf("max attempts reached: %w", err)
}

// ❌ WRONG - Can't cancel sleep
func retry(ctx context.Context, maxAttempts int, fn func(context.Context) error) error {
    for attempt := 0; attempt < maxAttempts; attempt++ {
        if err := fn(ctx); err == nil {
            return nil
        }
        time.Sleep(time.Second)  // Blocks even if ctx is canceled!
    }
}
```

### Rule 5.6: Classify Errors for Retry Logic
Only retry **transient** errors using `errors.Is`/`errors.As`

```go
// ✅ CORRECT
func isRetryable(err error) bool {
    if errors.Is(err, context.DeadlineExceeded) {
        return false  // Deadline means no more time
    }
    if errors.Is(err, context.Canceled) {
        return false  // User canceled
    }

    var netErr net.Error
    if errors.As(err, &netErr) && netErr.Timeout() {
        return true  // Network timeout is retryable
    }

    // Check for custom transient errors
    return errors.Is(err, ErrTransient)
}

// ❌ WRONG - Retries everything
func isRetryable(err error) bool {
    return err != nil  // Retries non-transient errors!
}
```

### Rule 5.7: defer Cleanup with Named Returns
✅ Use named return values to amend errors in defer

```go
// ✅ CORRECT
func processFile(path string) (err error) {
    f, err := os.Open(path)
    if err != nil {
        return err
    }
    defer func() {
        if closeErr := f.Close(); closeErr != nil {
            err = errors.Join(err, fmt.Errorf("close: %w", closeErr))
        }
    }()

    // Process file...
    return nil
}

// ❌ WRONG - Close error is lost
func processFile(path string) error {
    f, err := os.Open(path)
    if err != nil {
        return err
    }
    defer f.Close()  // Error ignored!

    // Process file...
    return nil
}
```

### Rule 5.8: Safe Error Logging - Redact Secrets
Ensure errors don't leak API keys, passwords, or tokens

```go
// ✅ CORRECT
type AuthError struct {
    Op     string
    UserID string  // Safe to log
    // apiKey is private, won't be in Error() output
}

func (e *AuthError) Error() string {
    return fmt.Sprintf("auth failed: op=%s user=%s", e.Op, e.UserID)
}

// ❌ WRONG - Leaks secrets
type AuthError struct {
    APIKey string
}

func (e *AuthError) Error() string {
    return fmt.Sprintf("auth failed with key: %s", e.APIKey)  // Leaked!
}
```

---

## 6. Graceful Shutdown

### Rule 6.1: Signal Handling with Context
✅ Handle SIGTERM/SIGINT and propagate via context cancellation

```go
// ✅ CORRECT
func main() {
    ctx, stop := signal.NotifyContext(context.Background(),
        syscall.SIGINT, syscall.SIGTERM)
    defer stop()

    srv := &http.Server{Addr: ":8080"}

    go func() {
        if err := srv.ListenAndServe(); err != http.ErrServerClosed {
            log.Fatalf("server error: %v", err)
        }
    }()

    <-ctx.Done()
    log.Println("shutting down gracefully...")

    shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
    defer cancel()

    if err := srv.Shutdown(shutdownCtx); err != nil {
        log.Fatalf("shutdown error: %v", err)
    }
}
```

### Rule 6.2: Reverse Dependency Shutdown Order
Shutdown in reverse order: stop accepting → drain workers → stop background → close DB

```go
// ✅ CORRECT shutdown order
func (s *Server) Shutdown(ctx context.Context) error {
    // 1. Stop accepting new requests
    s.httpServer.Shutdown(ctx)

    // 2. Drain worker pool
    close(s.jobs)
    s.workerGroup.Wait()

    // 3. Stop background tasks
    s.cancelBackground()
    s.backgroundGroup.Wait()

    // 4. Close database
    return s.db.Close()
}

// ❌ WRONG - Close DB while workers still using it!
func (s *Server) Shutdown(ctx context.Context) error {
    s.db.Close()  // Workers crash trying to use closed DB!
    close(s.jobs)
    s.workerGroup.Wait()
}
```

### Rule 6.3: No os.Exit() in Business Logic
❌ **NEVER** call `os.Exit()` outside `main()` - makes code untestable

```go
// ✅ CORRECT - Return error
func run(ctx context.Context) error {
    if err := setup(); err != nil {
        return fmt.Errorf("setup failed: %w", err)
    }
    // ...
    return nil
}

func main() {
    ctx := context.Background()
    if err := run(ctx); err != nil {
        log.Fatal(err)  // os.Exit only in main
    }
}

// ❌ WRONG - Can't test
func run(ctx context.Context) {
    if err := setup(); err != nil {
        log.Fatal(err)  // Exits process in test!
    }
}
```

---

## 7. Configuration & Options

### Rule 7.1: Functional Options Pattern
✅ Use functional options for configurable constructors

❌ Avoid "parameter soup" or huge config structs

```go
// ✅ CORRECT
type Server struct {
    timeout time.Duration
    logger  *slog.Logger
}

type Option func(*Server)

func WithTimeout(d time.Duration) Option {
    return func(s *Server) {
        s.timeout = d
    }
}

func WithLogger(l *slog.Logger) Option {
    return func(s *Server) {
        s.logger = l
    }
}

func NewServer(opts ...Option) *Server {
    s := &Server{
        timeout: 30 * time.Second,  // Defaults
        logger:  slog.Default(),
    }
    for _, opt := range opts {
        opt(s)
    }
    return s
}

// Usage
srv := NewServer(
    WithTimeout(10*time.Second),
    WithLogger(customLogger),
)

// ❌ WRONG - Parameter soup
func NewServer(timeout time.Duration, logger *slog.Logger,
    maxConns int, debug bool, cert string, key string) *Server
```

### Rule 7.2: No Global State
❌ **NEVER** use package-level variables for state or config

```go
// ✅ CORRECT - Dependency injection
type Service struct {
    db     *sql.DB
    logger *slog.Logger
}

func NewService(db *sql.DB, logger *slog.Logger) *Service {
    return &Service{db: db, logger: logger}
}

// ❌ WRONG - Global state, untestable
var (
    globalDB     *sql.DB
    globalLogger *slog.Logger
)

func Process(data Data) error {
    globalDB.Exec(...)  // Can't isolate in tests!
}
```

---

## 8. Filesystem & Embedding

### Rule 8.1: Use fs.FS Abstraction
✅ Accept `fs.FS` interface for filesystem operations

❌ Don't hardcode OS paths

```go
// ✅ CORRECT
func loadConfigs(fsys fs.FS, root string) (map[string][]byte, error) {
    configs := make(map[string][]byte)

    err := fs.WalkDir(fsys, root, func(path string, d fs.DirEntry, err error) error {
        if err != nil {
            return err
        }
        if !d.IsDir() && strings.HasSuffix(path, ".conf") {
            data, err := fs.ReadFile(fsys, path)
            if err != nil {
                return err
            }
            configs[path] = data
        }
        return nil
    })

    return configs, err
}

// Usage with different filesystems
configs, _ := loadConfigs(os.DirFS("/etc/app"), ".")
configs, _ := loadConfigs(embedFS, "configs")
configs, _ := loadConfigs(fstest.MapFS{...}, ".")  // Testing!

// ❌ WRONG - Hardcoded to OS
func loadConfigs(root string) (map[string][]byte, error) {
    return nil, filepath.Walk(root, ...)  // Can't test without real files!
}
```

### Rule 8.2: Build Tags for Dev/Prod embed
✅ Use build tags to switch between embedded and live filesystem

```go
// assets_prod.go
//go:build !dev

package main

import "embed"

//go:embed static templates
var assetsFS embed.FS

func Assets() (fs.FS, error) {
    return assetsFS, nil
}

// assets_dev.go
//go:build dev

package main

import "os"

func Assets() (fs.FS, error) {
    return os.DirFS("."), nil  // Live reload in development
}

// main.go (same code for both!)
func main() {
    fsys, _ := Assets()
    http.Handle("/static/", http.FileServer(http.FS(fsys)))
}
```

---

## 9. Testing

### Rule 9.1: Table-Driven Tests with t.Run
✅ **ALWAYS** use table-driven tests with subtests

```go
// ✅ CORRECT
func TestNormalize(t *testing.T) {
    tests := []struct {
        name    string
        input   string
        want    string
        wantErr bool
    }{
        {"lowercase", "content-type", "Content-Type", false},
        {"already normalized", "Content-Type", "Content-Type", false},
        {"invalid char", "foo@bar", "", true},
    }

    for _, tt := range tests {
        tt := tt  // Capture range variable
        t.Run(tt.name, func(t *testing.T) {
            got, err := Normalize(tt.input)
            if (err != nil) != tt.wantErr {
                t.Errorf("Normalize() error = %v, wantErr %v", err, tt.wantErr)
                return
            }
            if got != tt.want {
                t.Errorf("Normalize() = %v, want %v", got, tt.want)
            }
        })
    }
}

// ❌ WRONG - Repetitive, unclear failures
func TestNormalize(t *testing.T) {
    got, _ := Normalize("content-type")
    if got != "Content-Type" {
        t.Error("failed")  // Which test case failed?
    }
    // Repeat for each case...
}
```

### Rule 9.2: Parallel Tests Done Right
✅ Use `t.Parallel()` but **capture loop variables**

```go
// ✅ CORRECT
for _, tt := range tests {
    tt := tt  // Critical: capture loop variable
    t.Run(tt.name, func(t *testing.T) {
        t.Parallel()
        // Test uses tt safely
    })
}

// ❌ WRONG - Race condition
for _, tt := range tests {
    t.Run(tt.name, func(t *testing.T) {
        t.Parallel()
        // BUG: All subtests share same tt!
    })
}
```

### Rule 9.3: Fuzz Testing for Parsers
✅ Add fuzz tests for parsers, validators, sanitizers

```go
// ✅ CORRECT
func FuzzNormalize(f *testing.F) {
    // Seed corpus
    f.Add("content-type")
    f.Add("X-Custom-Header")

    f.Fuzz(func(t *testing.T, input string) {
        output, err := Normalize(input)

        // Property: never panic
        // Property: output is valid or error returned
        if err == nil {
            // Idempotent: Normalize(Normalize(x)) == Normalize(x)
            output2, err2 := Normalize(output)
            if err2 != nil || output2 != output {
                t.Errorf("not idempotent: %q -> %q -> %q", input, output, output2)
            }

            // Only valid characters
            if !isValidHeaderKey(output) {
                t.Errorf("invalid output: %q", output)
            }
        }
    })
}
```

### Rule 9.4: Use fstest.MapFS for Testing
✅ Test filesystem code without real files

```go
// ✅ CORRECT
func TestLoadConfigs(t *testing.T) {
    fsys := fstest.MapFS{
        "app.conf":         {Data: []byte("config1")},
        "sub/other.conf":   {Data: []byte("config2")},
        "readme.txt":       {Data: []byte("ignored")},
    }

    configs, err := LoadConfigs(fsys, ".")
    if err != nil {
        t.Fatal(err)
    }
    if len(configs) != 2 {
        t.Errorf("got %d configs, want 2", len(configs))
    }
}

// ❌ WRONG - Creates real files, slow, cleanup needed
func TestLoadConfigs(t *testing.T) {
    os.WriteFile("/tmp/test.conf", []byte("config"), 0644)
    defer os.Remove("/tmp/test.conf")
    // Fragile, slow, race conditions with parallel tests
}
```

---

## 10. Logging & Observability

### Rule 10.1: Structured Logging with slog
✅ **ALWAYS** use `log/slog` with structured fields

❌ Don't use `fmt.Printf` or unstructured `log.Printf`

```go
// ✅ CORRECT
slog.Info("request processed",
    "method", r.Method,
    "path", r.URL.Path,
    "duration_ms", elapsed.Milliseconds(),
    "status", statusCode,
)

// ❌ WRONG - Unstructured, hard to query
log.Printf("Processed %s %s in %v with status %d",
    r.Method, r.URL.Path, elapsed, statusCode)
```

### Rule 10.2: Context-Aware Logging
Add request IDs and trace context to logs

```go
// ✅ CORRECT
type contextKey string

const requestIDKey contextKey = "request_id"

func withRequestID(ctx context.Context, id string) context.Context {
    return context.WithValue(ctx, requestIDKey, id)
}

func logFromContext(ctx context.Context, msg string, args ...any) {
    if id, ok := ctx.Value(requestIDKey).(string); ok {
        args = append([]any{"request_id", id}, args...)
    }
    slog.Info(msg, args...)
}

// Usage
ctx := withRequestID(ctx, uuid.New().String())
logFromContext(ctx, "processing request", "user_id", userID)
```

---

## Quick Reference Checklist

When writing Go code, verify:

**Context:**
- [ ] `context.Context` is first parameter in all async/IO methods
- [ ] Using `errgroup.WithContext`, not `sync.WaitGroup`
- [ ] All goroutines have exit path via `ctx.Done()`
- [ ] Channel sends use `select` with `ctx.Done()`

**HTTP:**
- [ ] Created custom `http.Client`, not using `http.DefaultClient`
- [ ] Using `http.NewRequestWithContext`
- [ ] `defer resp.Body.Close()` and draining bodies
- [ ] Reusing Transport for connection pooling

**Errors:**
- [ ] Wrapping errors with `%w`, not `%v`
- [ ] Returning literal `nil`, not typed nil pointers
- [ ] Using `errors.Join` for multiple errors
- [ ] Classifying retryable vs permanent errors

**Performance:**
- [ ] `sync.Pool` with `Reset()` and size bounds
- [ ] `bufio.Reader` for large lines
- [ ] Streaming JSON with `json.Decoder`
- [ ] Verified 0 allocs/op with benchmarks

**Testing:**
- [ ] Table-driven tests with `t.Run`
- [ ] Captured loop variables in parallel tests
- [ ] Added fuzz tests for parsers
- [ ] Using `fstest.MapFS` instead of real files

**Shutdown:**
- [ ] Signal handling with `signal.NotifyContext`
- [ ] Reverse dependency order shutdown
- [ ] No `os.Exit()` in business logic

---

## Common Anti-Patterns to Avoid

❌ `sync.WaitGroup` for error propagation → Use `errgroup`
❌ `time.Sleep` for rate limiting → Use `x/time/rate.Limiter`
❌ `time.Sleep` for retries → Use `time.Timer` with `Reset()`
❌ `time.Tick` for periodic tasks → Use `time.NewTicker` with `defer .Stop()`
❌ `http.DefaultClient` → Create configured client with timeouts
❌ `bufio.Scanner` for large lines → Use `bufio.Reader.ReadSlice`
❌ Error wrapping with `%v` → Use `%w`
❌ Returning typed nil → Return literal `nil`
❌ Single mutex around map → Use sharded locks
❌ Package-level config variables → Use dependency injection
❌ Hardcoded OS paths → Use `fs.FS` abstraction
❌ Unstructured logging → Use `log/slog`

---

## Learning Resources

Official Go resources referenced in katas:
- [Go Blog: Context](https://go.dev/blog/context)
- [Go Blog: Error Handling (1.13+)](https://go.dev/blog/go1.13-errors)
- [Go Blog: Pipelines and Cancellation](https://go.dev/blog/pipelines)
- [Go Concurrency Patterns](https://go.dev/talks/2012/concurrency.slide)
- [errgroup Package](https://pkg.go.dev/golang.org/x/sync/errgroup)
- [singleflight Package](https://pkg.go.dev/golang.org/x/sync/singleflight)
- [time/rate Package](https://pkg.go.dev/golang.org/x/time/rate)
- [semaphore Package](https://pkg.go.dev/golang.org/x/sync/semaphore)
- [Go Fuzzing](https://go.dev/doc/security/fuzz/)

---

**This skill encodes production-grade Go patterns. Apply these rules religiously for safe, performant, idiomatic Go code.**
