#!/bin/bash
set -ex

go test -bench=. -benchmem ./pkg/dataloader > benchmarks/benchmark-results.txt 2>&1 && cat benchmarks/benchmark-results.txt