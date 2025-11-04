package benchmark

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	dattnguyen1991DL "github.com/dattnguyen1991/go-dataloader/pkg/dataloader"
	vikstrousDL "github.com/vikstrous/dataloadgen"
)

type PersonID int
type Person struct {
	ID   PersonID
	Name string
}

const batchSize = 100
const batchWait = 50 * time.Nanosecond

// fetch function
func mappedFetch(_ context.Context, keys []PersonID) (map[PersonID]Person, error) {
	result := make(map[PersonID]Person)
	for _, key := range keys {
		result[key] = Person{
			ID:   key,
			Name: fmt.Sprintf("P %d", key),
		}
	}
	return result, nil
}

func newVikstrousDL() *vikstrousDL.Loader[PersonID, Person] {
	return vikstrousDL.NewLoader(func(_ context.Context, keys []PersonID) ([]Person, []error) {
		users := make([]Person, len(keys))
		errors := make([]error, len(keys))

		for i, key := range keys {
			if key%100 == 1 {
				errors[i] = fmt.Errorf("user not found")
			} else {
				users[i] = Person{ID: key, Name: fmt.Sprintf("user %d", key)}
			}
		}
		return users, errors
	},
		vikstrousDL.WithBatchCapacity(batchSize),
		vikstrousDL.WithWait(batchWait),
		vikstrousDL.WithoutCache(),
	)
}

func newDatNguyenDL() *dattnguyen1991DL.DataLoader[PersonID, Person] {
	dl, _ := dattnguyen1991DL.NewDataLoader(
		dattnguyen1991DL.WithArrayFetchFunc(func(_ context.Context, keys []PersonID) ([]Person, []error) {
			users := make([]Person, len(keys))
			errors := make([]error, len(keys))

			for i, key := range keys {
				if key%100 == 1 {
					errors[i] = fmt.Errorf("user not found")
				} else {
					users[i] = Person{ID: key, Name: fmt.Sprintf("user %d", key)}
				}
			}
			return users, errors
		}),
		dattnguyen1991DL.WithBatchCapacity(batchSize),
		dattnguyen1991DL.WithBatchWait(batchWait),
	)
	return dl
}

func newVikstrousDLMapFetch() *vikstrousDL.Loader[PersonID, Person] {
	return vikstrousDL.NewMappedLoader(func(_ context.Context, keys []PersonID) (map[PersonID]Person, error) {
		users := make(map[PersonID]Person, len(keys))
		errors := make(map[PersonID]error, len(keys))

		for _, key := range keys {
			if key%100 == 1 {
				errors[key] = fmt.Errorf("user not found")
			} else {
				users[key] = Person{ID: key, Name: fmt.Sprintf("user %d", key)}
			}
		}
		return users, vikstrousDL.MappedFetchError[PersonID](errors)
	},
		vikstrousDL.WithBatchCapacity(batchSize),
		vikstrousDL.WithWait(batchWait),
		vikstrousDL.WithoutCache(),
	)
}

func newDatNguyenDLMapFetch() *dattnguyen1991DL.DataLoader[PersonID, Person] {
	dl, _ := dattnguyen1991DL.NewDataLoader(
		dattnguyen1991DL.WithMappedFetchFunc(func(_ context.Context, keys []PersonID) (map[PersonID]Person, error) {
			users := make(map[PersonID]Person, len(keys))
			errors := make(map[PersonID]error, len(keys))

			for _, key := range keys {
				if key%100 == 1 {
					errors[key] = fmt.Errorf("user not found")
				} else {
					users[key] = Person{ID: key, Name: fmt.Sprintf("user %d", key)}
				}
			}
			return users, vikstrousDL.MappedFetchError[PersonID](errors)
		}),
		dattnguyen1991DL.WithBatchCapacity(batchSize),
		dattnguyen1991DL.WithBatchWait(batchWait),
	)
	return dl
}

func xBenchmarkMemoryEfficiency_Sequential(b *testing.B) {

	dl, _ := dattnguyen1991DL.NewDataLoader(
		dattnguyen1991DL.WithMappedFetchFunc(mappedFetch),
		dattnguyen1991DL.WithBatchCapacity(batchSize),
		dattnguyen1991DL.WithBatchWait(batchWait),
	)
	defer dl.Stop()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		thunks := make([]func() (Person, error), 100)
		for j := range 100 {
			thunks[j] = dl.LoadThunk(context.Background(), PersonID(j))
		}

		for j := range 100 {
			_, _ = thunks[j]()
		}
	}
}

func xBenchmarkMemoryEfficiency_HighConcurrency(b *testing.B) {
	dl, _ := dattnguyen1991DL.NewDataLoader(
		dattnguyen1991DL.WithMappedFetchFunc(mappedFetch),
		dattnguyen1991DL.WithBatchCapacity(batchSize),
		dattnguyen1991DL.WithBatchWait(batchWait),
	)
	defer dl.Stop()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			thunks := make([]func() (Person, error), 100)
			for j := range 100 {
				thunks[j] = dl.LoadThunk(context.Background(), PersonID(j))
			}

			for j := range 100 {
				_, _ = thunks[j]()
			}
		}
	})

}

func xBenchmarkOps(b *testing.B) {
	b.Run("benchMapFn", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			personIds := make([]PersonID, 100)
			for j := range 100 {
				personIds[j] = PersonID(j)
			}

			_, _ = mappedFetch(context.Background(), personIds)
		}
		// BenchmarkOps/benchMapFn-12                123470              9447 ns/op           10596 B/op        110 allocs/op
	})

}

func BenchmarkSequential(b *testing.B) {
	b.Run("vikstrous", func(b *testing.B) {
		dl := newVikstrousDL()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			thunks := make([]func() (Person, error), 100)
			for j := range 100 {
				thunks[j] = dl.LoadThunk(context.Background(), PersonID(j))
			}

			for j := range 100 {
				_, _ = thunks[j]()
			}
		}
	})

	b.Run("dattnguyen1991", func(b *testing.B) {
		dl := newDatNguyenDL()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			thunks := make([]func() (Person, error), 100)
			for j := range 100 {
				thunks[j] = dl.LoadThunk(context.Background(), PersonID(j))
			}

			for j := range 100 {
				_, _ = thunks[j]()
			}
		}
	})
}

func BenchmarkConcurrently(b *testing.B) {
	b.Run("baseline", func(b *testing.B) {
		b.Run("vikstrous", func(b *testing.B) {
			dl := newVikstrousDL()
			b.ResetTimer()
			var goroutineID int32

			b.RunParallel(func(pb *testing.PB) {
				gID := int(atomic.AddInt32(&goroutineID, 1))
				for pb.Next() {
					thunks := make([]func() (Person, error), 100)
					for j := range 100 {
						thunks[j] = dl.LoadThunk(context.Background(), PersonID(gID*1000+j)) // avoid caching
					}

					for j := range 100 {
						_, _ = thunks[j]()
					}
				}
			})
		})

		b.Run("dattnguyen1991", func(b *testing.B) {
			dl := newDatNguyenDL()
			defer dl.Stop()
			b.ResetTimer()
			var goroutineID int32

			b.RunParallel(func(pb *testing.PB) {
				gID := int(atomic.AddInt32(&goroutineID, 1))
				for pb.Next() {
					thunks := make([]func() (Person, error), 100)
					for j := range 100 {
						thunks[j] = dl.LoadThunk(context.Background(), PersonID(gID*1000+j)) // avoid caching
					}

					for j := range 100 {
						_, _ = thunks[j]()
					}
				}
			})
		})

		b.Run("vikstrous_mapFetch", func(b *testing.B) {
			dl := newVikstrousDLMapFetch()
			b.ResetTimer()
			var goroutineID int32

			b.RunParallel(func(pb *testing.PB) {
				gID := int(atomic.AddInt32(&goroutineID, 1))
				for pb.Next() {
					thunks := make([]func() (Person, error), 100)
					for j := range 100 {
						thunks[j] = dl.LoadThunk(context.Background(), PersonID(gID*1000+j)) // avoid caching
					}

					for j := range 100 {
						_, _ = thunks[j]()
					}
				}
			})
		})

		b.Run("dattnguyen1991_mapFetch", func(b *testing.B) {
			dl := newDatNguyenDLMapFetch()
			defer dl.Stop()
			b.ResetTimer()
			var goroutineID int32

			b.RunParallel(func(pb *testing.PB) {
				gID := int(atomic.AddInt32(&goroutineID, 1))
				for pb.Next() {
					thunks := make([]func() (Person, error), 100)
					for j := range 100 {
						thunks[j] = dl.LoadThunk(context.Background(), PersonID(gID*1000+j)) // avoid caching
					}

					for j := range 100 {
						_, _ = thunks[j]()
					}
				}
			})
		})
	})

	b.Run("parallelism 200", func(b *testing.B) {
		parrallelism := 200
		b.Run("vikstrous", func(b *testing.B) {
			dl := newVikstrousDL()
			b.SetParallelism(parrallelism)
			b.ResetTimer()
			var goroutineID int32

			b.RunParallel(func(pb *testing.PB) {
				gID := int(atomic.AddInt32(&goroutineID, 1))
				for pb.Next() {
					thunks := make([]func() (Person, error), 100)
					for j := range 100 {
						thunks[j] = dl.LoadThunk(context.Background(), PersonID(gID*1000+j)) // avoid caching
					}

					for j := range 100 {
						_, _ = thunks[j]()
					}
				}
			})
		})

		b.Run("dattnguyen1991", func(b *testing.B) {
			dl := newDatNguyenDL()
			// defer dl.Stop()
			b.SetParallelism(parrallelism)
			b.ResetTimer()
			var goroutineID int32

			b.RunParallel(func(pb *testing.PB) {
				gID := int(atomic.AddInt32(&goroutineID, 1))
				for pb.Next() {
					thunks := make([]func() (Person, error), 100)
					for j := range 100 {
						thunks[j] = dl.LoadThunk(context.Background(), PersonID(gID*1000+j)) // avoid caching
					}

					for j := range 100 {
						_, _ = thunks[j]()
					}
				}
			})
		})

		b.Run("vikstrous_mapFetch", func(b *testing.B) {
			dl := newVikstrousDLMapFetch()
			b.SetParallelism(parrallelism)
			b.ResetTimer()
			var goroutineID int32

			b.RunParallel(func(pb *testing.PB) {
				gID := int(atomic.AddInt32(&goroutineID, 1))
				for pb.Next() {
					thunks := make([]func() (Person, error), 100)
					for j := range 100 {
						thunks[j] = dl.LoadThunk(context.Background(), PersonID(gID*1000+j)) // avoid caching
					}

					for j := range 100 {
						_, _ = thunks[j]()
					}
				}
			})
		})

		b.Run("dattnguyen1991_mapFetch", func(b *testing.B) {
			dl := newDatNguyenDLMapFetch()
			// defer dl.Stop()
			b.SetParallelism(parrallelism)
			b.ResetTimer()
			var goroutineID int32

			b.RunParallel(func(pb *testing.PB) {
				gID := int(atomic.AddInt32(&goroutineID, 1))
				for pb.Next() {
					thunks := make([]func() (Person, error), 100)
					for j := range 100 {
						thunks[j] = dl.LoadThunk(context.Background(), PersonID(gID*1000+j)) // avoid caching
					}

					for j := range 100 {
						_, _ = thunks[j]()
					}
				}
			})
		})
	})
}

// RPS := 1000000000/3705 = 270k
