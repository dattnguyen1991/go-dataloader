# Benchmarks

## 1) Proved the core value of batching

* **Dramatic fetch-call reduction.**

  * Sanity run: 
    - **100k single loads** → **100,000 fetches**; 
    - **100k batched** → **~1,000 fetches**.
  * High-latency test (50 ms): **500 requests**

    * No batching: **500 fetch calls**
    * Batch size 50: **10 fetch calls**
* **Bigger batches = more keys per call.**

  * Keys per call climbed to ~**100** with batch size 100, confirming the loader efficiently aggregates concurrent keys.

**Why it matters:** fewer backend calls reduces DB/API QPS, contention, and rate-limit pressure—often the bottleneck in real systems.

---

## 2) Demonstrated throughput gains under latency

* With 50 ms simulated latency, batching keeps **requests/sec** roughly the same while slashing backend work:

  * No batching: **~9,709 req/s**, **500 fetch calls**
  * Batched (50): **~9,758 req/s**, **10 fetch calls**

**Why it matters:** On latency-bound workloads, batching preserves client throughput while dramatically lowering upstream load.

---

## 3) Quantified scalability across batch sizes & concurrency

* With **100 concurrent** requests:

  * Batch 1 → **100 calls**
  * Batch 100 → **1 call**
* With **1,000 concurrent** requests:

  * Batch 50 → **20 calls**
  * Batch 100 → **10 calls**

**Why it matters:** The loader scales gracefully with concurrency; picking a batch size near your typical key fan-in yields near-optimal call consolidation.

---

## 4) Showed memory/alloc benefits at scale

* Under high concurrency, batching cut allocations and bytes/op substantially:

  * Example: moving from no batching to optimal batching reduced **B/op** by ~3–4× and **allocs/op** by **2–4×** in multiple benches.

**Why it matters:** Lower allocation rate = less GC churn and more predictable tail latencies in production.

---

## 5) Verified loader behavior in different usage patterns

* **Baseline (no loader):** every call hits the data source (**keys_per_call ≈ 1**).
* **Loader with realistic latency & waits:** small `BatchWait` windows (2–10 ms) effectively coalesce bursts of goroutines.
