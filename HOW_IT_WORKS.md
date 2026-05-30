# How gojek/work Operates

`gojek/work` is a high-performance Go library for background job processing, backed by Redis. It allows you to enqueue jobs from your application, and process them asynchronously in background workers. It supports scheduled jobs, retries, unique jobs, and periodic cron-like jobs.

This document details the inner workings of the library, specifically focusing on how job payloads are encoded and the exact Redis operations performed during different stages of a job's lifecycle.

## Payload Encoding

All jobs in `gojek/work` are represented by a `Job` struct. When a job is enqueued, it is encoded using **JSON**.

The core `Job` payload structure looks like this:
```json
{
  "name": "email_job",
  "id": "97c84119d13cb54119a38743",
  "t": 1695420000,
  "args": {
    "customer_id": 123,
    "email": "test@example.com"
  },
  "unique": false,
  "fails": 0
}
```
- `name`: The job type name.
- `id`: A unique 24-character hex identifier.
- `t`: Epoch timestamp when the job was enqueued or scheduled.
- `args`: Key-value pairs of job arguments.
- `unique`, `unique_key`: Used for unique jobs.
- `fails`, `err`, `failed_at`: Metadata updated during retries and failures.

## Enqueueing Jobs

When a job is enqueued, the library serializes the `Job` struct into JSON and pushes it to Redis.

### Normal Jobs (`Enqueue`)
When you enqueue a regular job using `enqueuer.Enqueue(jobName, args)`:
1. **Serialization**: The `Job` struct is encoded into a JSON byte array.
2. **`LPUSH`**: It performs an `LPUSH` onto the job's specific queue: `work:jobs:<jobName>`.
3. **Known Jobs Tracking (`SADD`)**: If this is the first time this job type has been enqueued recently, it performs a `SADD` to add the job name to `work:known_jobs`. This set is used by the UI and the reaper.

### Scheduled Jobs (`EnqueueIn` / `EnqueueAt`)
For jobs scheduled to run in the future:
1. **Serialization**: The job is encoded into JSON.
2. **`ZADD`**: Instead of a list, it uses a Redis Sorted Set (`ZADD`). The key is `work:scheduled`, and the score is the epoch time when the job should run. The value is the JSON payload.

### Unique Jobs (`EnqueueUnique`)
Unique jobs ensure only one job with specific arguments exists in the queue at a time.
1. **Unique Key Generation**: A key is generated hashing the job name and arguments, e.g., `work:unique:<jobName>:<argsHash>`.
2. **Lua Script Execution**: It uses a Lua script that does:
    - `SET` with `NX` and `EX` (expire after 24h) on the unique key.
    - If successful, it performs the `LPUSH` (or `ZADD` for scheduled unique jobs) to queue the job.
    - If the key already exists, it ignores the enqueue or updates the TTL, returning a 'duplicate' indicator.

### Bulk Enqueue (`BulkEnqueue`)
Bulk enqueue processes multiple jobs in a single pipeline. It loops through jobs doing `LPUSH` or `ZADD`, optionally `HSET` for unique jobs via a Lua script, and executes them together followed by a known jobs check.

## Dequeueing and Processing Jobs

Jobs are processed by `WorkerPool` instances, which spin up multiple concurrent `Worker` goroutines.

### Fetching a Job
Workers pull jobs from Redis using a Lua script to ensure atomic, safe dequeuing.
1. **Priority Sampling**: The worker selects a subset of job queues to pull from based on the priority assigned to each job type.
2. **Lua Script Execution**: The worker executes a Lua script (`redisLuaFetchJob`) passing the queues, in-progress queues, and lock info.
    - The script iterates over the sampled queues.
    - It checks if a queue is paused (`GET work:jobs:<jobName>:paused`).
    - It checks concurrency limits (`GET work:jobs:<jobName>:max_concurrency` and `GET work:jobs:<jobName>:lock`).
    - If valid, it runs `RPOPLPUSH` (Right Pop Left Push) to atomically pop the JSON job from the main list (`work:jobs:<jobName>`) and push it to a worker-pool specific in-progress list (`work:jobs:<jobName>:<poolID>:inprogress`).
    - It tracks the lock by incrementing `work:jobs:<jobName>:lock` and adding to the hash `work:jobs:<jobName>:lock_info`.

### Processing
The JSON is deserialized, and the job's assigned handler is invoked in Go.
- If it's a unique job, the worker executes a `DEL` on the unique key (`work:unique:...`) so new identical jobs can be enqueued.

### Post-Processing
After the job finishes (success or failure):
1. **Removal from In-Progress (`LREM`)**: The job is removed from the in-progress list (`LREM work:jobs:<jobName>:<poolID>:inprogress 1 <json_payload>`).
2. **Lock Release (`DECR`)**: The concurrency lock is decremented (`DECR` on lock key, `HINCRBY` -1 on lock info).

#### On Success
The job is simply removed from the in-progress queue.

#### On Failure
If the handler returns an error or panics:
- **Retries (`ZADD`)**: If fails are under `MaxFails`, the job's JSON payload is updated with the error and fail count. It is then added to the retry sorted set (`ZADD work:retry <backoff_timestamp> <json_payload>`).
- **Dead Queue (`ZADD`)**: If `MaxFails` is reached, it is added to the dead queue sorted set (`ZADD work:dead <now> <json_payload>`).

## Background Processes

### Requeuer
A background goroutine checks the `work:scheduled` and `work:retry` sorted sets.
- Every few seconds, it runs a Lua script (`redisLuaZremLpushCmd`).
- The script uses `ZRANGEBYSCORE` to find jobs whose timestamp is `<= now`.
- It removes them with `ZREM` and pushes them to their respective job lists using `LPUSH`, making them ready for workers to fetch.

### Reaper
Handles crashes. If a worker process dies ungracefully, jobs might be stuck in its in-progress list.
- The Heartbeater regularly writes a heartbeat key (`work:worker_pools:<poolID>`).
- The Reaper checks for expired heartbeats.
- If a dead pool is found, it uses a Lua script (`redisLuaReenqueueJob`) to `RPOPLPUSH` jobs from the dead pool's in-progress list back to the main job queue, and cleans up stale locks.

## Testing

The library is heavily tested using **unit tests and integration tests** interacting closely with Redis.
- There are no explicit end-to-end (E2E) UI or full application lifecycle tests marked as "E2E".
- Instead, the tests (e.g., `enqueue_test.go`, `worker_test.go`) start an actual Redis connection pool (or use mocks) and test the full behavior of the enqueueing, Lua scripts, worker pools, retries, and concurrent locking mechanisms.
