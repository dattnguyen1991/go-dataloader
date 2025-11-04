package dataloader

import (
	"errors"
	"fmt"
	"strings"
)

// ErrNotFound is returned when a requested key is not found in the data source.
// Useful to distinguish between a missing key and other types of errors.
var ErrNotFound = errors.New("dataloader: not found")

// MappedError represents a mapping of keys to their corresponding errors.
// It is used by Mapped fetch functions to return per-key errors in case client want
// to know which specific keys failed.
type MappedError[K comparable] map[K]error

func (e MappedError[K]) Error() string {
	var errs = make([]string, len(e))
	i := 0
	for k, v := range e {
		errs[i] = fmt.Sprintf("%v: %v", k, v)
		i++
	}
	return fmt.Sprint("mapped errors: [", strings.Join(errs, ", "), "]")
}
