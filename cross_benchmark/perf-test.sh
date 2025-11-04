#!/bin/bash
set -ex

# go test -bench=. -benchmem  -benchtime=5s -count=3 . > benchmark-results.txt 2>&1 && cat benchmark-results.txt
go test -bench=. -benchmem  -benchtime=5s . > benchmark-results.txt 2>&1 && cat benchmark-results.txt