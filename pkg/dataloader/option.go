package dataloader

import (
	"context"
	"time"
)

type Options struct {
	batchSize int
	batchWait time.Duration
}

// Option allows for configuration of loader fields.
type Option func(*Options)

// WithBatchCapacity sets the batch capacity. Default is 100
func WithBatchCapacity(capacity int) Option {
	return func(opt *Options) {
		opt.batchSize = capacity
	}
}

// WithBatchWait sets the amount of time to wait before triggering a batch.
// Default duration is 10 milliseconds.
func WithBatchWait(duration time.Duration) Option {
	return func(opt *Options) {
		opt.batchWait = duration
	}
}

// WithArrayFetchFunc set Array fetch function.
func WithArrayFetchFunc[K comparable, V any](
	fn func(ctx context.Context, keys []K) ([]V, []error),
) func(*DataLoader[K, V]) {
	return func(l *DataLoader[K, V]) {
		l.arrayFetchFunc = fn
	}
}

// WithMappedFetchFunc set Mapped fetch function.
func WithMappedFetchFunc[K comparable, V any](
	fn func(ctx context.Context, keys []K) (map[K]V, error),
) func(*DataLoader[K, V]) {
	return func(l *DataLoader[K, V]) {
		l.mappedFetchFunc = fn
	}
}
