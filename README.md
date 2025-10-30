# go-dataloader

A high-performance, generic DataLoader for Go that address the concurrent query problem through request batching and object pooling.

[![Go Version](https://img.shields.io/badge/go-%3E%3D1.24.3-blue.svg)](https://golang.org/)

## Features

- 🚀 **High Performance**: Handles massive workloads efficiently with significant memory savings
- 🎯 **Generic Type Safety**: Full type safety with Go 1.18+ generics  
- 🔄 **Auto Batching**: Automatically groups concurrent requests
- 💾 **Object Pooling**: Zero-allocation operation at scale
- 🛡️ **Error Handling**: Per-key error reporting and panic recovery
- ⚡ **Context Support**: Cancellation and timeout handling
- 🎛️ **Configurable**: Tunable batch size and timing, fetch type

## Use case
This library is especially useful when your data source has limitations such as a restricted number of connections, slow backend response times, or expensive connection establishment. It excels in scenarios where the underlying data store performs significantly better with batch "get" operations compared to single "get" requests. By batching concurrent loads, `go-dataloader` reduces the number of backend calls, improves throughput, and minimizes connection overhead—making it ideal for databases, remote APIs, or any service where batching is more efficient than individual queries.

## Installation

```bash
go get github.com/dattnguyen1991/go-dataloader/pkg/dataloader
```

## Quick Start

```go
package main

import (
    "context"
    "fmt"
    "github.com/dattnguyen1991/go-dataloader/pkg/dataloader"
)

type UserID int
type User struct {
    ID   UserID
    Name string
}

func main() {
    // Create DataLoader with fetch function
    loader, err := dataloader.NewDataLoader(
        dataloader.WithMappedFetchFunc(fetchUsers),
    )
    if err != nil {
        panic(err)
    }
    defer loader.Stop()
    
    // Load users (automatically batched)
    ctx := context.Background()
    thunk1 := loader.LoadThunk(ctx, UserID(1))
    thunk2 := loader.LoadThunk(ctx, UserID(2))

    user1, err := thunk1()
    user2, err := thunk2()
    
    fmt.Printf("Loaded: %+v, %+v\n", user1, user2)
}

func fetchUsers(ctx context.Context, ids []UserID) (map[UserID]User, error) {
    fmt.Printf("Batch fetching %d users\n", len(ids)) // Only called once!
    
    result := make(map[UserID]User)
    for _, id := range ids {
        result[id] = User{ID: id, Name: fmt.Sprintf("User%d", id)}
    }
    return result, nil
}
```

## Usage Patterns

### Mapped Fetch Pattern (Recommended)

Returns a map of keys to values, allowing missing keys and per-key errors:

```go
func fetchUsers(ctx context.Context, ids []UserID) (map[UserID]User, error) {
    users := make(map[UserID]User)
    errors := make(dataloader.MappedError[UserID])
    
    for _, id := range ids {
        if id == 0 {
            errors[id] = fmt.Errorf("invalid user ID: %d", id)
            continue
        }
        
        user, err := db.GetUser(ctx, id)
        if err != nil {
            if errors.Is(err, sql.ErrNoRows) {
                // Missing keys automatically return dataloader.ErrNotFound
                continue  
            }
            errors[id] = err
            continue
        }
        
        users[id] = user
    }
    
    if len(errors) > 0 {
        return users, errors  // Return partial results with errors
    }
    return users, nil
}

loader, _ := dataloader.NewDataLoader(
    dataloader.WithMappedFetchFunc(fetchUsers),
)
```

### Array Fetch Pattern

Returns slices in the same order as input keys:

```go
func fetchUsersArray(ctx context.Context, ids []UserID) ([]User, []error) {
    users := make([]User, len(ids))
    errs := make([]error, len(ids))
    
    for i, id := range ids {
        user, err := db.GetUser(ctx, id)
        users[i] = user
        errs[i] = err  // Can be nil
    }
    
    return users, errs
}

loader, _ := dataloader.NewDataLoader(
    dataloader.WithArrayFetchFunc(fetchUsersArray),
)
```

## Performance

📊 **[View Detailed Performance Analysis](benchmarks/pef-analysis-summary.md)**

## Configuration

### Batch Settings

```go
loader, _ := dataloader.NewDataLoader(
    dataloader.WithMappedFetchFunc(fetchFunc),
    dataloader.WithBatchCapacity[UserID, User](50),     // Batch size (default: 100)
    dataloader.WithBatchWait[UserID, User](5*time.Millisecond), // Wait time (default: 10ms)
)
```

### Performance Tuning

| Batch Size | Use Case | Trade-offs |
|------------|----------|------------|
| **1-10** | Low latency, real-time | Higher overhead, more DB calls |
| **50-200** | Balanced (recommended) | Good latency/throughput balance |
| **500+** | High throughput, analytics | Higher latency, better batching |

| Wait Time | Use Case | Trade-offs |
|-----------|----------|------------|
| **1-5ms** | Real-time applications | Lower batching efficiency |
| **10-20ms** | Most applications | Optimal balance |
| **50ms+** | Background processing | Higher latency |

## Error Handling

### Missing Keys

```go
user, err := loader.Load(ctx, UserID(999))
if errors.Is(err, dataloader.ErrNotFound) {
    fmt.Println("User not found")
}
```

### Per-Key Errors

```go
// In fetch function
func fetchUsers(ctx context.Context, ids []UserID) (map[UserID]User, error) {
    result := make(map[UserID]User)
    mappedErr := make(dataloader.MappedError[UserID])
    
    for _, id := range ids {
        if id%2 == 0 {
            mappedErr[id] = fmt.Errorf("even IDs not allowed: %d", id)
        } else {
            result[id] = User{ID: id, Name: fmt.Sprintf("User%d", id)}
        }
    }
    
    if len(mappedErr) > 0 {
        return result, mappedErr  // Partial success + errors
    }
    return result, nil
}

// Usage
user, err := loader.Load(ctx, UserID(2))
if err != nil {
    fmt.Printf("Error loading user: %v\n", err) // "even IDs not allowed: 2"
}
```

### Panic Recovery

DataLoader automatically recovers from panics in fetch functions:

```go
func panickyFetch(ctx context.Context, ids []UserID) (map[UserID]User, error) {
    panic("database connection failed!")  // Recovered automatically
}

user, err := loader.Load(ctx, UserID(1))
// err will contain: "panic during fetch: database connection failed!"
```

## Advanced Usage

### Deferred Loading with LoadThunk

`LoadThunk` returns a function that can be called later to get the result, enabling deferred execution patterns:

```go
// Create thunks without blocking
userThunk1 := loader.LoadThunk(ctx, UserID(1))
userThunk2 := loader.LoadThunk(ctx, UserID(2))
userThunk3 := loader.LoadThunk(ctx, UserID(3))

// All requests are queued and batched together
// Call thunks when you need the results
user1, err1 := userThunk1() // Wait for result
user2, err2 := userThunk2() // Already available (same batch)
user3, err3 := userThunk3() // Already available (same batch)
```

### Lifecycle Management

```go
loader, _ := dataloader.NewDataLoader(
    dataloader.WithMappedFetchFunc(fetchFunc),
)

// Always clean up resources
defer loader.Stop()

// loader.Stop() safely shuts down background goroutines
```

### Key-Value Types

```go
type ProductSKU string
type Product struct {
    SKU   ProductSKU
    Name  string
    Price float64
}

productLoader, _ := dataloader.NewDataLoader(
    dataloader.WithMappedFetchFunc[ProductSKU, Product](fetchProducts),
)

product, err := productLoader.Load(ctx, ProductSKU("ABC123"))
```

## Real-World Examples
### Resolver Pattern

```go
type UserResolver struct {
    userLoader *dataloader.DataLoader[UserID, User]
}

func (r *UserResolver) Posts(ctx context.Context, userID UserID) ([]*Post, error) {
    // This automatically batches with other concurrent resolvers
    user, err := r.userLoader.Load(ctx, userID)
    if err != nil {
        return nil, err
    }
    return r.postService.GetPostsByUser(ctx, user.ID)
}
```

### HTTP API Handler

In some applications, serving multiple concurrent tasks (such as HTTP requests) each load a single key, and we have slow backend/db or limit connection to data store. DataLoader batches these concurrent loads (within the same interval) into a single fetch, even across goroutines, reducing database calls and improving throughput.

```go
func (h *Handler) GetUser(w http.ResponseWriter, r *http.Request) {
    userID := getUserIDFromPath(r.URL.Path)
    
    // Concurrent requests to this endpoint are automatically batched
    user, err := h.userLoader.Load(r.Context(), userID)
    if errors.Is(err, dataloader.ErrNotFound) {
        http.Error(w, "User not found", http.StatusNotFound)
        return
    }
    if err != nil {
        http.Error(w, err.Error(), http.StatusInternalServerError)
        return
    }
    
    json.NewEncoder(w).Encode(user)
}
```

## API Reference

### Core Types

```go
type DataLoader[K comparable, V any] struct { /* ... */ }
type LoadRequest[K comparable, V any] struct { /* ... */ }  
type LoadResponse[V any] struct { /* ... */ }
type MappedError[K comparable] map[K]error
```

### Constructor

```go
func NewDataLoader[K comparable, V any](options ...Option[K, V]) (*DataLoader[K, V], error)
```

### Methods

```go
func (dl *DataLoader[K, V]) Load(ctx context.Context, key K) (V, error)
func (dl *DataLoader[K, V]) LoadThunk(ctx context.Context, key K) func() (V, error)
func (dl *DataLoader[K, V]) Stop()
```

### Options

```go
func WithBatchCapacity[K, V](capacity int) Option[K, V]
func WithBatchWait[K, V](duration time.Duration) Option[K, V]  
func WithArrayFetchFunc[K, V](fn func(context.Context, []K) ([]V, []error)) Option[K, V]
func WithMappedFetchFunc[K, V](fn func(context.Context, []K) (map[K]V, error)) Option[K, V]
```

### Error Types

```go
var ErrNotFound = errors.New("dataloader: not found")
type MappedError[K comparable] map[K]error
```
