package dataloader

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

type DataLoader[K comparable, V any] struct {
	// Current batch being collected
	currentBatch *LoadBatch[K, V]

	// Object pools for zero-allocation operation
	keysPool *sync.Pool

	// Configuration
	batchSize       int
	batchWait       time.Duration
	arrayFetchFunc  func(context.Context, []K) ([]V, []error)
	mappedFetchFunc func(context.Context, []K) (map[K]V, error)

	// batch mutex
	mu sync.Mutex
}

type LoadBatch[K comparable, V any] struct {
	// Prefer SoA as it is more cache friendly
	keys      []K
	responses []V
	errors    []error

	isDispatched bool // whether the batch has been dispatched
	done         chan struct{}
}

func NewDataLoader[K comparable, V any](
	fetchFnOpt func(*DataLoader[K, V]),
	opts ...Option,
) (*DataLoader[K, V], error) {
	dl := &DataLoader[K, V]{}
	fetchFnOpt(dl)

	options := &Options{
		batchSize: 100,
		batchWait: 10 * time.Millisecond,
	}

	for _, opt := range opts {
		opt(options)
	}

	dl.batchSize = options.batchSize
	dl.batchWait = options.batchWait

	// Initialize object pools
	dl.keysPool = &sync.Pool{
		New: func() any { return make([]K, 0, dl.batchSize) },
	}

	// Validation
	if dl.arrayFetchFunc == nil && dl.mappedFetchFunc == nil {
		return nil, errors.New("DataLoader must be configured with either an array or mapped fetch function")
	}

	return dl, nil
}

// Load loads a single key, batching with other requests as needed
// blocking waits for the result or context cancellation then return the result
// wait time = batchWait (configured via WithBatchWait) + fetchFunc's execution time
func (dl *DataLoader[K, V]) Load(ctx context.Context, key K) (V, error) {
	batch, pos := dl.queueRequest(ctx, key)

	select {
	case <-ctx.Done():
		var zeroV V
		return zeroV, ctx.Err()
	case <-batch.done: // wait for batch to be processed
	}

	return batch.responses[pos], batch.errors[pos]
}

// LoadThunk loads a single key, batching with other requests as needed
// returns a thunk function that can be called later to get the result
func (dl *DataLoader[K, V]) LoadThunk(ctx context.Context, key K) func() (V, error) {
	batch, pos := dl.queueRequest(ctx, key)

	return func() (V, error) {
		select {
		case <-ctx.Done():
			var zeroV V
			return zeroV, ctx.Err()
		case <-batch.done: // wait for batch to be processed
		}

		return batch.responses[pos], batch.errors[pos]
	}
}

func (dl *DataLoader[K, V]) queueRequest(_ context.Context, key K) (batch *LoadBatch[K, V], batchPos int) {
	// As long as the rest of queueRequest is not blocked for too long,
	// we choose to use mutex over semaphore (which support context cancellation)
	// for better performance under high concurrency
	dl.mu.Lock()

	batchPos = dl.upsertCurrentBatch(key)
	batch = dl.currentBatch
	if len(dl.currentBatch.keys) < dl.batchSize {
		dl.mu.Unlock()
		return
	}

	// Batch is full, process it
	dl.currentBatch = nil
	batch.isDispatched = true
	dl.mu.Unlock()

	go dl.executeBatch(batch)

	return
}

func (dl *DataLoader[K, V]) upsertCurrentBatch(key K) (batchPos int) {
	if dl.currentBatch == nil {
		dl.currentBatch = &LoadBatch[K, V]{
			keys: dl.keysPool.Get().([]K)[:0],
			done: make(chan struct{}),
		}

		// start go routine for batch timed out
		go func(batch *LoadBatch[K, V]) {
			time.Sleep(dl.batchWait)
			dl.mu.Lock()

			if batch.isDispatched {
				dl.mu.Unlock()
				return
			}

			dl.currentBatch = nil // reset current batch to force create new batch
			dl.mu.Unlock()

			if len(batch.keys) == 0 {
				return
			}

			// Process batch in separate goroutine
			go dl.executeBatch(batch)
		}(dl.currentBatch)
	}

	batchPos = len(dl.currentBatch.keys)
	dl.currentBatch.keys = append(dl.currentBatch.keys, key)
	return
}

func (dl *DataLoader[K, V]) executeBatch(loadBatch *LoadBatch[K, V]) {
	if dl.arrayFetchFunc != nil { // Array Fetch
		dl.executeArrayFetch(loadBatch)
	} else { // Mapped Fetch
		dl.executeMappedFetch(loadBatch)
	}

	close(loadBatch.done) // Signal completion to all callers

	// Return keys to pools
	// In case K is struct type, we dont want pool to keep references to elements so GC can reclaim memory
	//nolint:staticcheck // SA6002: safe to pool slices here, we reuse the backing array
	dl.keysPool.Put(loadBatch.keys[:0])
}

func (dl *DataLoader[K, V]) executeArrayFetch(batch *LoadBatch[K, V]) {
	// Execute Array fetch
	values, errors := dl.safeArrayFetch(context.Background(), batch.keys)

	// Defensive: ensure values and errors are not nil and have correct length
	// If not, initialize them. Otherwise use as is
	if values == nil || len(values) != len(batch.keys) {
		values = make([]V, len(batch.keys))
	}
	if errors == nil || (len(errors) != 0 && len(errors) != len(batch.keys)) {
		errors = make([]error, len(batch.keys))
	}

	// If the fetch function returned the wrong number of responses/errors, return an error to all callers
	var fetchFnImplementationError error
	if len(values) != len(batch.keys) {
		fetchFnImplementationError = fmt.Errorf(
			"fetch function implementation error: %d values returned for %d keys", len(values), len(batch.keys),
		)
	}
	if len(errors) != 0 && len(errors) != len(batch.keys) { // errors can be nil or same length as keys
		fetchFnImplementationError = fmt.Errorf(
			"fetch function implementation error: %d errors returned for %d keys", len(errors), len(batch.keys),
		)
	}

	// Distribute results back to callers
	batch.responses = values
	batch.errors = errors

	// Handle Error cases
	if fetchFnImplementationError != nil {
		for i := range batch.keys {
			batch.errors[i] = fetchFnImplementationError
		}
	}
}

func (dl *DataLoader[K, V]) executeMappedFetch(batch *LoadBatch[K, V]) {
	// Bound check elimination
	keys := batch.keys
	keySize := len(keys)
	responses := make([]V, keySize)
	errs := make([]error, keySize)
	// END Bound check elimination

	// Execute Mapped fetch
	mappedResponse, fetchErr := dl.safeMappedFetch(context.Background(), keys)

	var mappedErr MappedError[K]
	// mappedFetchFunc can return either a single error or a MappedError
	// If single error, it applies to all keys
	// If MappedError, it contains per-key errors
	isMappedError := errors.As(fetchErr, &mappedErr)

	// Extract Responses
	for i := range keySize {
		key := keys[i]
		var foundResponse bool
		if mappedResponse != nil {
			responses[i], foundResponse = mappedResponse[key]
		}

		if fetchErr == nil {
			if !foundResponse {
				errs[i] = ErrNotFound // if fetchFn did not return value for this key, default to not found error
			}
			continue
		}

		// Handle errs from fetch
		if !isMappedError {
			// For single error, assign to all responses
			errs[i] = fetchErr
			continue
		}

		// For MappedError, check if this key has a specific error
		if keyErr, hasError := mappedErr[key]; hasError {
			errs[i] = keyErr
		} else if !foundResponse {
			errs[i] = ErrNotFound
		}
	}
	batch.responses = responses
	batch.errors = errs
}

func (dl *DataLoader[K, V]) safeArrayFetch(ctx context.Context, keys []K) (values []V, errs []error) {
	defer func() {
		if r := recover(); r != nil {
			panicErr := fmt.Errorf("panic during fetch: %v", r)
			// Set panic error for all keys
			for i := range keys {
				errs[i] = panicErr
			}
		}
	}()
	values, errs = dl.arrayFetchFunc(ctx, keys)
	return
}

func (dl *DataLoader[K, V]) safeMappedFetch(ctx context.Context, keys []K) (m map[K]V, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic during fetch: %v", r)
		}
	}()
	m, err = dl.mappedFetchFunc(ctx, keys)
	return
}
