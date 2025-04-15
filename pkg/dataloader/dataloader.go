package dataloader

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

type DataLoader[K comparable, V any] struct {
	// Channel for request coordination
	requestChan chan *LoadRequest[K, V]

	// Object pools for zero-allocation operation
	requestPool  *sync.Pool
	responsePool *sync.Pool

	// Configuration
	batchSize       int
	batchWait       time.Duration
	arrayFetchFunc  func(context.Context, []K) ([]V, []error)
	mappedFetchFunc func(context.Context, []K) (map[K]V, error)

	// Lifecycle
	loaderCtx    context.Context
	loaderCancel context.CancelFunc
}

// LoadRequest objects to eliminate allocations
type LoadRequest[K comparable, V any] struct {
	Key      K
	Response *LoadResponse[V]
	BatchPos int
}

type LoadResponse[V any] struct {
	Ready chan struct{}
	Value V
	Error error
}

func NewDataLoader[K comparable, V any](
	options ...Option[K, V],
) (*DataLoader[K, V], error) {
	ctx, cancel := context.WithCancel(context.Background())
	dl := &DataLoader[K, V]{
		requestChan: make(chan *LoadRequest[K, V], 10_000),
		batchSize:   100,
		batchWait:   10 * time.Millisecond,

		loaderCtx:    ctx,
		loaderCancel: cancel,
	}

	for _, opt := range options {
		opt(dl)
	}

	// Initialize object pools
	dl.requestPool = &sync.Pool{
		New: func() any {
			return &LoadRequest[K, V]{}
		},
	}

	dl.responsePool = &sync.Pool{
		New: func() any {
			return &LoadResponse[V]{
				Ready: make(chan struct{}, 1), // Buffered to avoid blocking
			}
		},
	}

	// Validation
	if dl.arrayFetchFunc == nil && dl.mappedFetchFunc == nil {
		return nil, errors.New("DataLoader must be configured with either an array or mapped fetch function")
	}

	// Start batch processor
	go dl.start()

	return dl, nil
}

// Load loads a single key, batching with other requests as needed
// blocking waits for the result or context cancellation then return the result
// wait time = batchWait (configured via WithBatchWait) + fetchFunc's execution time
func (dl *DataLoader[K, V]) Load(ctx context.Context, key K) (V, error) {
	req, res := dl.queueRequest(ctx, key)

	// Wait for result
	return dl.loadResult(ctx, req, res)
}

// LoadThunk loads a single key, batching with other requests as needed
// returns a thunk function that can be called later to get the result
func (dl *DataLoader[K, V]) LoadThunk(ctx context.Context, key K) func() (V, error) {
	req, res := dl.queueRequest(ctx, key)

	return func() (V, error) {
		return dl.loadResult(ctx, req, res)
	}
}

func (dl *DataLoader[K, V]) queueRequest(ctx context.Context, key K) (req *LoadRequest[K, V], res *LoadResponse[V]) {
	// Get pooled objects
	req = dl.getPooledRequest()
	res = dl.getPooledResponse()

	// Initialize request
	req.Key = key
	req.Response = res

	select {
	case dl.requestChan <- req:
		// Request queued successfully
	case <-ctx.Done():
		dl.returnToPool(req, res)
		res.Error = ctx.Err()

		// Signal completion to caller without blocking (channel size 1)
		select {
		case res.Ready <- struct{}{}:
		default:
			// Channel already signaled, ignore
		}

		// FIXME: Consider adding a max wait time to avoid overload that causes queuing stuck forever
		// case <-time.After(dl.maxWaitTime):  // Optional: Max wait time to enqueue
		// 	return zero, ErrSystemOverloaded
		// }
	}

	return
}

func (dl *DataLoader[K, V]) loadResult(ctx context.Context, req *LoadRequest[K, V], res *LoadResponse[V]) (V, error) {
	select {
	case <-res.Ready:
		value, err := res.Value, res.Error
		dl.returnToPool(req, res)
		return value, err
	case <-ctx.Done():
		dl.returnToPool(req, res)
		var zero V
		return zero, ctx.Err()
	}
}

func (dl *DataLoader[K, V]) getPooledRequest() *LoadRequest[K, V] {
	req := dl.requestPool.Get().(*LoadRequest[K, V])

	// Reset pooled value
	// Must reset ALL fields of LoadRequest to avoid stale data
	var zeroK K
	req.Key = zeroK
	req.Response = nil
	req.BatchPos = 0

	return req
}

func (dl *DataLoader[K, V]) getPooledResponse() *LoadResponse[V] {
	resp := dl.responsePool.Get().(*LoadResponse[V])

	// Reset pooled value
	// Must reset ALL fields of LoadResponse to avoid stale data
	var zeroV V
	resp.Value = zeroV
	resp.Error = nil

	// Drain channel if needed
	select {
	case <-resp.Ready:
	default:
	}

	return resp
}

func (dl *DataLoader[K, V]) returnToPool(req *LoadRequest[K, V], resp *LoadResponse[V]) {
	dl.requestPool.Put(req)
	dl.responsePool.Put(resp)
}

// start the batch processing loop
func (dl *DataLoader[K, V]) start() {
	for {
		select {
		case <-dl.loaderCtx.Done():
			return // Clean exit from infinite loop
		default:
			batch := dl.collectBatch()
			if len(batch) == 0 {
				continue
			}

			// Process batch in separate goroutine to avoid blocking collection
			go dl.executeBatch(batch)
		}
	}
}

func (dl *DataLoader[K, V]) Stop() {
	dl.loaderCancel()
}

func (dl *DataLoader[K, V]) collectBatch() []*LoadRequest[K, V] {
	// Pre-allocate batch slice
	batch := make([]*LoadRequest[K, V], 0, dl.batchSize)

	timer := time.NewTimer(dl.batchWait)
	defer timer.Stop()

	for {
		// Gather requests until batch is full or window expires
		select {
		case req := <-dl.requestChan:
			req.BatchPos = len(batch)
			batch = append(batch, req)

			if len(batch) >= dl.batchSize {
				return batch // Batch full
			}

		case <-timer.C:
			return batch // Window expired
		}
	}
}

func (dl *DataLoader[K, V]) executeBatch(batch []*LoadRequest[K, V]) {
	if len(batch) == 0 {
		return
	}

	keys := make([]K, len(batch))

	for i, req := range batch {
		keys[i] = req.Key
	}

	if dl.arrayFetchFunc != nil { // Array Fetch
		dl.executeArrayFetch(batch, keys)
	} else { // Mapped Fetch
		dl.executeMappedFetch(batch, keys)
	}
}

func (dl *DataLoader[K, V]) executeArrayFetch(
	batch []*LoadRequest[K, V],
	keys []K,
) {
	batchCtx := context.Background()
	// Execute Array fetch
	values, errors := dl.safeArrayFetch(batchCtx, keys)

	// If the fetch function returned the wrong number of responses, return an error to all callers
	var valuesKeysLengthDiffErr error
	var errorsKeysLengthDiffErr error
	if len(values) != len(batch) {
		valuesKeysLengthDiffErr = fmt.Errorf(
			"fetch function implementation error: %d values returned for %d keys",
			len(values),
			len(keys),
		)
	}
	if len(errors) != 0 && len(errors) != len(batch) { // errors can be nil or same length as keys
		errorsKeysLengthDiffErr = fmt.Errorf(
			"fetch function implementation error: %d errors returned for %d keys",
			len(errors),
			len(keys),
		)
	}

	// Distribute results back to callers
	for i, req := range batch {
		resp := req.Response

		if valuesKeysLengthDiffErr != nil {
			resp.Error = valuesKeysLengthDiffErr
		} else if errorsKeysLengthDiffErr != nil {
			resp.Error = errorsKeysLengthDiffErr
		} else {
			// Normal case
			resp.Value = values[i]
			resp.Error = errors[i]
		}

		// Signal completion to caller without blocking (channel size 1)
		select {
		case resp.Ready <- struct{}{}:
		default:
			// Channel already signaled, ignore
		}
	}
}

func (dl *DataLoader[K, V]) executeMappedFetch(
	batch []*LoadRequest[K, V],
	keys []K,
) {
	batchCtx := context.Background()

	// Execute Mapped fetch
	mappedResponse, fetchErr := dl.safeMappedFetch(batchCtx, keys)

	var mappedErr MappedError[K]
	// mappedFetchFunc can return either a single error or a MappedError
	// If single error, it applies to all keys
	// If MappedError, it contains per-key errors
	isMappedError := errors.As(fetchErr, &mappedErr)

	// Extract Responses
	for _, req := range batch {
		resp := req.Response
		var foundResponse bool
		if mappedResponse != nil {
			resp.Value, foundResponse = mappedResponse[req.Key]
		}

		if fetchErr == nil {
			if !foundResponse {
				resp.Error = ErrNotFound
			}
			continue
		}

		// Handle errors from fetch
		if !isMappedError {
			// For single error, assign to all responses
			resp.Error = fetchErr
			continue
		}

		// For MappedError, check if this key has a specific error
		if keyErr, hasError := mappedErr[req.Key]; hasError {
			resp.Error = keyErr
		} else if !foundResponse {
			resp.Error = ErrNotFound
		}
	}

	// Distribute results back to callers
	for _, req := range batch {
		// Signal completion to caller without blocking (channel size 1)
		select {
		case req.Response.Ready <- struct{}{}:
		default:
			// Channel already signaled, ignore
		}
	}
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
