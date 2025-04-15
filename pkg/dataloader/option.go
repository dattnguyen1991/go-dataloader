package dataloader

import (
	"context"
	"time"
)

// Option allows for configuration of loader fields.
type Option[K comparable, V any] func(*DataLoader[K, V])

// WithBatchCapacity sets the batch capacity. Default is 100
func WithBatchCapacity[K comparable, V any](capacity int) Option[K, V] {
	return func(l *DataLoader[K, V]) {
		l.batchSize = capacity
	}
}

// WithBatchWait sets the amount of time to wait before triggering a batch.
// Default duration is 10 milliseconds.
func WithBatchWait[K comparable, V any](duration time.Duration) Option[K, V] {
	return func(l *DataLoader[K, V]) {
		l.batchWait = duration
	}
}

// WithArrayFetchFunc set Array fetch function.
func WithArrayFetchFunc[K comparable, V any](
	fn func(ctx context.Context, keys []K) ([]V, []error),
) Option[K, V] {
	return func(l *DataLoader[K, V]) {
		l.arrayFetchFunc = fn
	}
}

// WithMappedFetchFunc set Mapped fetch function.
func WithMappedFetchFunc[K comparable, V any](
	fn func(ctx context.Context, keys []K) (map[K]V, error),
) Option[K, V] {
	return func(l *DataLoader[K, V]) {
		l.mappedFetchFunc = fn
	}
}
