package dataloader

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Test data types
type UserID int
type User struct {
	ID   UserID
	Name string
}

// Mock fetch functions for testing
func successfulArrayFetch(_ context.Context, keys []UserID) ([]User, []error) {
	results := make([]User, len(keys))

	for i, key := range keys {
		results[i] = User{
			ID:   key,
			Name: fmt.Sprintf("User%d", key),
		}
	}
	return results, nil
}

func successfulMappedFetch(_ context.Context, keys []UserID) (map[UserID]User, error) {
	result := make(map[UserID]User)
	for _, key := range keys {
		result[key] = User{
			ID:   key,
			Name: fmt.Sprintf("User%d", key),
		}
	}
	return result, nil
}

func mappedErrorFetch(_ context.Context, keys []UserID) (map[UserID]User, error) {
	result := make(map[UserID]User)
	mappedErr := make(MappedError[UserID])

	for _, key := range keys {
		if key%3 == 0 {
			// Every third key gets an error
			mappedErr[key] = fmt.Errorf("permission denied for user %d", key)
		} else {
			result[key] = User{
				ID:   key,
				Name: fmt.Sprintf("User%d", key),
			}
		}
	}

	if len(mappedErr) > 0 {
		return result, mappedErr
	}
	return result, nil
}

func panicMappedFetch(_ context.Context, _ []UserID) (map[UserID]User, error) {
	panic("unexpected panic in fetch function")
}

// TestNewDataLoader_MappedFetch tests DataLoader creation with MappedFetch
func TestNewDataLoader_MappedFetch(t *testing.T) {
	tests := []struct {
		name        string
		fetchFnOpt  func(*DataLoader[UserID, User])
		options     []Option
		expectError bool
		errorMsg    string
	}{
		{
			name:        "successful creation with mapped fetch",
			fetchFnOpt:  WithMappedFetchFunc(successfulMappedFetch),
			options:     []Option{},
			expectError: false,
		},
		{
			name:        "successful creation with array fetch",
			fetchFnOpt:  WithArrayFetchFunc(successfulArrayFetch),
			options:     []Option{},
			expectError: false,
		},
		{
			name:       "successful creation with custom batch settings",
			fetchFnOpt: WithMappedFetchFunc(successfulMappedFetch),
			options: []Option{
				WithBatchCapacity(500),
				WithBatchWait(5 * time.Millisecond),
			},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dl, err := NewDataLoader(tt.fetchFnOpt, tt.options...)

			if tt.expectError {
				if err == nil {
					t.Errorf("expected error but got none")
					return
				}
				if err.Error() != tt.errorMsg {
					t.Errorf("expected error message '%s', got '%s'", tt.errorMsg, err.Error())
				}
				return
			}

			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			if dl == nil {
				t.Error("expected non-nil DataLoader")
				return
			}

			// Clean shutdown
		})
	}
}

// TestMappedFetch_Load tests basic Load operations
func TestMappedFetch_Load(t *testing.T) {
	dl, _ := NewDataLoader(
		WithMappedFetchFunc(successfulMappedFetch),
		WithBatchCapacity(100),
		WithBatchWait(10*time.Millisecond),
	)

	t.Run("single load", func(t *testing.T) {
		user, err := dl.Load(context.Background(), UserID(1))
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}

		expected := User{ID: UserID(1), Name: "User1"}
		if user != expected {
			t.Errorf("expected %+v, got %+v", expected, user)
		}
	})

	t.Run("multiple concurrent loads", func(t *testing.T) {
		const numLoads = 10
		var wg sync.WaitGroup
		results := make([]User, numLoads)
		errors := make([]error, numLoads)

		for i := range numLoads {
			wg.Add(1)
			// Multiple goroutines loading concurrently
			go func(idx int) {
				defer wg.Done()
				user, err := dl.Load(context.Background(), UserID(idx+1))
				results[idx] = user
				errors[idx] = err
			}(i)
		}

		wg.Wait()

		for i := range numLoads {
			if errors[i] != nil {
				t.Errorf("load %d failed: %v", i, errors[i])
			}

			expected := User{ID: UserID(i + 1), Name: fmt.Sprintf("User%d", i+1)}
			if results[i] != expected {
				t.Errorf("load %d: expected %+v, got %+v", i, expected, results[i])
			}
		}
	})
}

// TestMappedFetch_LoadThunk tests basic LoadThunk operations
func TestMappedFetch_LoadThunk(t *testing.T) {
	dl, _ := NewDataLoader(
		WithMappedFetchFunc(successfulMappedFetch),
		WithBatchCapacity(100),
		WithBatchWait(10*time.Millisecond),
	)

	t.Run("single load", func(t *testing.T) {
		thunk := dl.LoadThunk(context.Background(), UserID(1))
		user, err := thunk()
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}

		expected := User{ID: UserID(1), Name: "User1"}
		if user != expected {
			t.Errorf("expected %+v, got %+v", expected, user)
		}
	})

	t.Run("multiple sequential load", func(t *testing.T) {
		numUserID := 100
		userIDs := make([]UserID, numUserID)
		for i := range numUserID {
			userIDs[i] = UserID(i + 1)
		}

		// Load thunks
		thunks := make([]func() (User, error), numUserID)
		for i, userID := range userIDs {
			// Call sequentially without blocking
			thunks[i] = dl.LoadThunk(context.Background(), userID)
		}

		// Execute thunks to get result
		for i, thunk := range thunks {
			// Blocking call to get result for each thunk
			user, err := thunk()
			if err != nil {
				t.Errorf("load %d failed: %v", i, err)
			}

			expected := User{ID: UserID(i + 1), Name: fmt.Sprintf("User%d", i+1)}
			if user != expected {
				t.Errorf("expected %+v, got %+v", expected, user)
			}
		}
	})

	t.Run("multiple concurrent loads", func(t *testing.T) {
		const numLoads = 10
		var wg sync.WaitGroup
		results := make([]User, numLoads)
		errors := make([]error, numLoads)

		for i := range numLoads {
			wg.Add(1)
			// Multiple goroutines loading concurrently
			go func(idx int) {
				defer wg.Done()
				thunk := dl.LoadThunk(context.Background(), UserID(idx+1))
				user, err := thunk()
				results[idx] = user
				errors[idx] = err
			}(i)
		}

		wg.Wait()

		for i := range numLoads {
			if errors[i] != nil {
				t.Errorf("load %d failed: %v", i, errors[i])
			}

			expected := User{ID: UserID(i + 1), Name: fmt.Sprintf("User%d", i+1)}
			if results[i] != expected {
				t.Errorf("load %d: expected %+v, got %+v", i, expected, results[i])
			}
		}
	})
}

// TestMappedFetch_SimulateRealisticFetch tests Realistic Fetch operations
func TestMappedFetch_SimulateRealisticFetch(t *testing.T) {
	cnt := atomic.Int64{}
	simulateRealisticFetch := func(_ context.Context, keys []UserID) (map[UserID]User, error) {
		result := make(map[UserID]User)
		time.Sleep(100 * time.Millisecond) // Simulate latency
		for _, key := range keys {
			result[key] = User{
				ID:   key,
				Name: fmt.Sprintf("User%d", key),
			}
		}
		cnt.Add(1)
		return result, nil
	}

	const numLoads = 100_000
	runTest := func(t *testing.T, testName string, dl *DataLoader[UserID, User], expectedFetchTimes int) {
		var wg sync.WaitGroup
		results := make([]User, numLoads)
		errors := make([]error, numLoads)

		startTime := time.Now()
		ctx := context.Background()
		for i := range numLoads {
			wg.Add(1)
			// 100k goroutines loading concurrently
			go func(idx int) {
				defer wg.Done()
				user, err := dl.Load(ctx, UserID(idx+1))
				results[idx] = user
				errors[idx] = err
			}(i)
		}

		wg.Wait()

		for i := range numLoads {
			if errors[i] != nil {
				t.Errorf("load %d failed: %v", i, errors[i])
			}

			expected := User{ID: UserID(i + 1), Name: fmt.Sprintf("User%d", i+1)}
			if results[i] != expected {
				t.Errorf("load %d: expected %+v, got %+v", i, expected, results[i])
			}
		}
		fetchTimes := cnt.Load()
		if int(fetchTimes) != expectedFetchTimes {
			t.Errorf("%s: expected fetch to be called %d times, got %d", testName, expectedFetchTimes, fetchTimes)
		}

		fmt.Printf("%s: 100k loads took: %v - fetch %v times\n", testName, time.Since(startTime), fetchTimes)
	}

	concurentBatchSize := 100
	dlConcurrent, _ := NewDataLoader(
		WithMappedFetchFunc(simulateRealisticFetch),
		WithBatchCapacity(concurentBatchSize),
		WithBatchWait(10*time.Millisecond),
	)

	singlebatchSize := 1
	dlSingle, _ := NewDataLoader(
		WithMappedFetchFunc(simulateRealisticFetch),
		WithBatchCapacity(singlebatchSize),
		WithBatchWait(10*time.Millisecond),
	)

	runTest(t, "Single load (no batching)", dlSingle, numLoads/singlebatchSize)
	cnt.Store(0)
	runTest(t, "Concurrent batching", dlConcurrent, numLoads/concurentBatchSize)
}

// TestMappedFetch_MissingKeys tests handling of missing keys (ErrNotFound)
func TestMappedFetch_MissingKeys(t *testing.T) {
	// only returns users for even numbered keys
	evenNumberSuccessMappedFetch := func(_ context.Context, keys []UserID) (map[UserID]User, error) {
		result := make(map[UserID]User)
		for _, key := range keys {
			if key%2 == 0 {
				result[key] = User{ID: key, Name: fmt.Sprintf("User%d", key)}
			}
		}
		return result, nil
	}
	dl, _ := NewDataLoader(WithMappedFetchFunc(evenNumberSuccessMappedFetch))

	t.Run("existing key", func(t *testing.T) {
		user, err := dl.Load(context.Background(), UserID(2)) // Even number, should exist
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}

		expected := User{ID: UserID(2), Name: "User2"}
		if user != expected {
			t.Errorf("expected %+v, got %+v", expected, user)
		}
	})

	t.Run("missing key", func(t *testing.T) {
		_, err := dl.Load(context.Background(), UserID(1)) // Odd number, should be missing
		if !errors.Is(err, ErrNotFound) {
			t.Errorf("expected ErrNotFound, got %v", err)
		}
	})
}

// TestMappedFetch_ErrorHandling tests various error scenarios
func TestMappedFetch_ErrorHandling(t *testing.T) {
	t.Run("global error", func(t *testing.T) {
		errorMappedFetch := func(_ context.Context, _ []UserID) (map[UserID]User, error) {
			return nil, errors.New("database connection failed")
		}
		dl, _ := NewDataLoader(WithMappedFetchFunc(errorMappedFetch))

		_, err := dl.Load(context.Background(), UserID(1))

		if err == nil {
			t.Error("expected error but got none")
		}

		if err.Error() != "database connection failed" {
			t.Errorf("expected 'database connection failed', got '%s'", err.Error())
		}
	})

	t.Run("mapped errors", func(t *testing.T) {
		dl, _ := NewDataLoader(WithMappedFetchFunc(mappedErrorFetch))

		// Test successful key (not divisible by 3)
		user, err := dl.Load(context.Background(), UserID(1))
		if err != nil {
			t.Errorf("unexpected error for successful key: %v", err)
		}
		expected := User{ID: UserID(1), Name: "User1"}
		if user != expected {
			t.Errorf("expected %+v, got %+v", expected, user)
		}

		// Test error key (divisible by 3)
		_, err = dl.Load(context.Background(), UserID(3))
		if err == nil {
			t.Error("expected error for key 3 but got none")
		}
		expectedErr := "permission denied for user 3"
		if err.Error() != expectedErr {
			t.Errorf("expected '%s', got '%s'", expectedErr, err.Error())
		}
	})

	t.Run("panic recovery", func(t *testing.T) {
		dl, _ := NewDataLoader(WithMappedFetchFunc(panicMappedFetch))

		_, err := dl.Load(context.Background(), UserID(1))

		if err == nil {
			t.Error("expected error from panic recovery but got none")
		}

		if !strings.Contains(err.Error(), "panic during fetch") {
			t.Errorf("expected panic error message, got '%s'", err.Error())
		}
	})
}

// TestMappedFetch_ContextCancellation tests context cancellation scenarios
func TestMappedFetch_ContextCancellation(t *testing.T) {
	slowMappedFetch := func(ctx context.Context, keys []UserID) (map[UserID]User, error) {
		time.Sleep(100 * time.Millisecond)
		return successfulMappedFetch(ctx, keys)
	}
	dl, _ := NewDataLoader(WithMappedFetchFunc(slowMappedFetch))

	t.Run("context timeout during load", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
		defer cancel()

		_, err := dl.Load(ctx, UserID(1))
		if err == nil {
			t.Error("expected context cancellation error but got none")
		}

		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("expected context.DeadlineExceeded, got %v", err)
		}
	})

	t.Run("context cancellation during queue", func(t *testing.T) {
		// Fill up the request channel to force queueing
		ctx, cancel := context.WithCancel(context.Background())
		cancel() // Cancel immediately

		_, err := dl.Load(ctx, UserID(1))
		if err == nil {
			t.Error("expected context cancellation error but got none")
		}

		if !errors.Is(err, context.Canceled) {
			t.Errorf("expected context.Canceled, got %v", err)
		}
	})
}

// TestMappedFetch_Batching tests batching behavior
func TestMappedFetch_Batching(t *testing.T) {
	var fetchCallCount int64
	var fetchedKeysCount int64

	batchingFetch := func(_ context.Context, keys []UserID) (map[UserID]User, error) {
		atomic.AddInt64(&fetchCallCount, 1)
		atomic.AddInt64(&fetchedKeysCount, int64(len(keys)))

		result := make(map[UserID]User)
		for _, key := range keys {
			result[key] = User{ID: key, Name: fmt.Sprintf("User%d", key)}
		}
		return result, nil
	}

	dl, _ := NewDataLoader(
		WithMappedFetchFunc(batchingFetch),
		WithBatchCapacity(5),
		WithBatchWait(20*time.Millisecond),
	)

	const numRequests = 10
	// Fire off requests rapidly to trigger batching
	var wg sync.WaitGroup
	for i := 0; i < numRequests; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			_, err := dl.Load(context.Background(), UserID(id+1))
			if err != nil {
				t.Errorf("load failed: %v", err)
			}
		}(i)
	}

	wg.Wait()

	// Allow some time for batching to complete
	time.Sleep(50 * time.Millisecond)

	calls := atomic.LoadInt64(&fetchCallCount)
	totalKeys := atomic.LoadInt64(&fetchedKeysCount)

	if calls == 0 {
		t.Error("expected at least one fetch call")
	}

	if totalKeys != numRequests {
		t.Errorf("expected %d total keys fetched, got %d", numRequests, totalKeys)
	}

	// With batch size 5 and 10 requests, we should have <= 2 batches
	if calls > 2 {
		t.Errorf("expected at most 2 fetch calls for batching, got %d", calls)
	}
}

// TestMappedFetch_ConfigurationOptions tests various configuration options
func TestMappedFetch_ConfigurationOptions(t *testing.T) {
	t.Run("custom batch capacity", func(t *testing.T) {
		dl, _ := NewDataLoader(
			WithMappedFetchFunc(successfulMappedFetch),
			WithBatchCapacity(3),
		)

		if dl.batchSize != 3 {
			t.Errorf("expected batch size 3, got %d", dl.batchSize)
		}
	})

	t.Run("custom batch wait", func(t *testing.T) {
		dl, _ := NewDataLoader(
			WithMappedFetchFunc(successfulMappedFetch),
			WithBatchWait(50*time.Millisecond),
		)

		if dl.batchWait != 50*time.Millisecond {
			t.Errorf("expected batch wait 50ms, got %v", dl.batchWait)
		}
	})
}
