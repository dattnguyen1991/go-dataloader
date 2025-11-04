package dataloader

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"
)

// Test data types
type PersonID int
type Person struct {
	ID   PersonID
	Name string
}

// Instrumented fetch functions to measure batching effectiveness
type FetchMetrics struct {
	CallCount int64
	KeyCount  int64
}

func (m *FetchMetrics) Reset() {
	atomic.StoreInt64(&m.CallCount, 0)
	atomic.StoreInt64(&m.KeyCount, 0)
}

func (m *FetchMetrics) IncrementCall(keyCount int) {
	atomic.AddInt64(&m.CallCount, 1)
	atomic.AddInt64(&m.KeyCount, int64(keyCount))
}

func (m *FetchMetrics) GetStats() (calls, keys int64) {
	return atomic.LoadInt64(&m.CallCount), atomic.LoadInt64(&m.KeyCount)
}

// Fast fetch function (no latency simulation)
func createFastMappedFetch(metrics *FetchMetrics) func(context.Context, []PersonID) (map[PersonID]Person, error) {
	return func(_ context.Context, keys []PersonID) (map[PersonID]Person, error) {
		metrics.IncrementCall(len(keys))
		result := make(map[PersonID]Person)
		for _, key := range keys {
			result[key] = Person{
				ID:   key,
				Name: fmt.Sprintf("Person%d", key),
			}
		}
		return result, nil
	}
}

// Realistic fetch function (simulates DB/API latency)
func createRealisticMappedFetch(
	metrics *FetchMetrics, latency time.Duration,
) func(context.Context, []PersonID) (map[PersonID]Person, error) {
	return func(_ context.Context, keys []PersonID) (map[PersonID]Person, error) {
		metrics.IncrementCall(len(keys))

		// Simulate realistic database/API latency
		time.Sleep(latency)

		result := make(map[PersonID]Person)
		for _, key := range keys {
			result[key] = Person{
				ID:   key,
				Name: fmt.Sprintf("Person%d", key),
			}
		}
		return result, nil
	}
}

// Baseline: Individual fetch calls (no dataloader)
func BenchmarkBaseline_IndividualCalls(b *testing.B) {
	metrics := &FetchMetrics{}
	fetchFunc := createRealisticMappedFetch(metrics, 10*time.Millisecond)
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Simulate individual calls to the data source
		_, err := fetchFunc(ctx, []PersonID{PersonID(i%1000 + 1)})
		if err != nil {
			b.Errorf("fetch failed: %v", err)
		}
	}

	calls, keys := metrics.GetStats()
	b.ReportMetric(float64(calls), "fetch_calls")
	b.ReportMetric(float64(keys), "total_keys")
	b.ReportMetric(float64(keys)/float64(calls), "keys_per_call")
}

// Benchmark: Fetch Call Reduction - demonstrates core dataloader value
func BenchmarkFetchCallReduction(b *testing.B) {
	const numRequests = 1000
	runTest := func(testName string, batchSize int) {
		b.Run(testName, func(b *testing.B) {
			metrics := &FetchMetrics{}
			fetchFunc := createFastMappedFetch(metrics)

			dl, _ := NewDataLoader(
				WithMappedFetchFunc(fetchFunc),
				WithBatchCapacity(batchSize), // Optimal batching
				WithBatchWait(10*time.Millisecond),
			)

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				metrics.Reset()

				// Fire concurrent requests
				results := make(chan error, numRequests)
				for j := 0; j < numRequests; j++ {
					go func(id int) {
						_, err := dl.Load(context.Background(), PersonID(id%100+1))
						results <- err
					}(j)
				}

				// Wait for all to complete
				for j := 0; j < numRequests; j++ {
					if err := <-results; err != nil {
						b.Errorf("load failed: %v", err)
					}
				}
			}

			calls, keys := metrics.GetStats()
			b.ReportMetric(float64(calls)/float64(b.N), "fetch_calls_per_iter")
			b.ReportMetric(float64(keys)/float64(calls), "keys_per_call")
		})
	}

	runTest("no_batching_size_1", 1)
	runTest("optimal_batching_size_100", 100)
}

// Benchmark: Latency vs Throughput - shows real-world performance gains
func BenchmarkLatencyThroughput(b *testing.B) {
	const numRequests = 500

	runTest := func(testName string, batchSize int) {
		b.Run(testName, func(b *testing.B) {
			metrics := &FetchMetrics{}
			fetchFunc := createRealisticMappedFetch(metrics, 50*time.Millisecond)

			dl, _ := NewDataLoader(
				WithMappedFetchFunc(fetchFunc),
				WithBatchCapacity(batchSize),
				WithBatchWait(10*time.Millisecond),
			)

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				metrics.Reset()
				start := time.Now()

				// Fire concurrent requests
				results := make(chan error, numRequests)
				for j := 0; j < numRequests; j++ {
					go func(id int) {
						_, err := dl.Load(context.Background(), PersonID(id%50+1))
						results <- err
					}(j)
				}

				// Wait for all to complete
				for range numRequests {
					if err := <-results; err != nil {
						b.Errorf("load failed: %v", err)
					}
				}

				duration := time.Since(start)
				calls, _ := metrics.GetStats()

				b.ReportMetric(float64(numRequests)/duration.Seconds(), "requests_per_sec")
				b.ReportMetric(float64(calls), "fetch_calls")
			}
		})
	}
	runTest("high_latency_no_batching", 1)
	runTest("high_latency_optimal_batching", 50)
}

// Benchmark: Scalability - different batch sizes and concurrency patterns
func BenchmarkScalability(b *testing.B) {
	testCases := []struct {
		name        string
		batchSize   int
		concurrency int
		uniqueKeys  int
	}{
		{"batch_1_concurrent_100", 1, 100, 50},
		{"batch_10_concurrent_100", 10, 100, 50},
		{"batch_50_concurrent_100", 50, 100, 50},
		{"batch_100_concurrent_100", 100, 100, 50},
		{"batch_50_concurrent_1000", 50, 1000, 100},
		{"batch_100_concurrent_1000", 100, 1000, 100},
	}

	for _, tc := range testCases {
		b.Run(tc.name, func(b *testing.B) {
			metrics := &FetchMetrics{}
			fetchFunc := createRealisticMappedFetch(metrics, 20*time.Millisecond)

			dl, _ := NewDataLoader(
				WithMappedFetchFunc(fetchFunc),
				WithBatchCapacity(tc.batchSize),
				WithBatchWait(5*time.Millisecond),
			)

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				metrics.Reset()
				start := time.Now()

				// Fire concurrent requests
				results := make(chan error, tc.concurrency)
				for j := 0; j < tc.concurrency; j++ {
					go func(id int) {
						_, err := dl.Load(context.Background(), PersonID(id%tc.uniqueKeys+1))
						results <- err
					}(j)
				}

				// Wait for all to complete
				for j := 0; j < tc.concurrency; j++ {
					if err := <-results; err != nil {
						b.Errorf("load failed: %v", err)
					}
				}

				duration := time.Since(start)
				calls, keys := metrics.GetStats()

				b.ReportMetric(float64(tc.concurrency)/duration.Seconds(), "requests_per_sec")
				b.ReportMetric(float64(calls), "fetch_calls")
				b.ReportMetric(float64(keys)/float64(calls), "keys_per_call")
				b.ReportMetric(float64(tc.concurrency)/float64(calls), "batching_efficiency")
			}
		})
	}
}

// Benchmark: Memory Efficiency - object pooling benefits
func BenchmarkMemoryEfficiency_HighConcurrency(b *testing.B) {
	metrics := &FetchMetrics{}
	fetchFunc := createFastMappedFetch(metrics)

	dl, _ := NewDataLoader(
		WithMappedFetchFunc(fetchFunc),
		WithBatchCapacity(100),
		WithBatchWait(2*time.Millisecond),
	)

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			_, err := dl.Load(context.Background(), PersonID(i%1000+1))
			if err != nil {
				b.Errorf("load failed: %v", err)
			}
			i++
		}
	})

	calls, keys := metrics.GetStats()
	b.ReportMetric(float64(keys)/float64(calls), "keys_per_call")
}

func BenchmarkMappedFetch_ConcurrentLoad_Legacy(b *testing.B) {
	metrics := &FetchMetrics{}
	fetchFunc := createFastMappedFetch(metrics)

	dl, _ := NewDataLoader(WithMappedFetchFunc(fetchFunc))

	ctx := context.Background()
	b.ResetTimer()

	b.SetParallelism(50)
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			_, err := dl.Load(ctx, PersonID(i%1000+1))
			if err != nil {
				b.Errorf("load failed: %v", err)
			}
			i++
		}
	})

	calls, keys := metrics.GetStats()
	b.ReportMetric(float64(calls), "fetch_calls")
	b.ReportMetric(float64(keys)/float64(calls), "keys_per_call")
}
