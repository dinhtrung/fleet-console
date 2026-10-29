package forge

import (
	"context"
	"sync"
)

// Parallelism bounds the per-item forge reads. A project's wall time is
// O(items/Parallelism), not O(items): the links/sub-issue/dependency reads are
// one CLI process each, so a serial loop cannot fit a big repository into a tick.
const Parallelism = 8

// parallelEach runs fn for every index with a small worker pool. It returns the
// index and error of the first failure (lowest index wins) and cancels the rest.
func parallelEach(ctx context.Context, total, workers int, fn func(context.Context, int) error) (int, error) {
	if total == 0 {
		return -1, nil
	}
	if workers <= 0 {
		workers = Parallelism
	}
	if workers > total {
		workers = total
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	jobs := make(chan int)
	var wg sync.WaitGroup
	var mu sync.Mutex
	failedIndex, firstErr := total, error(nil)

	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for index := range jobs {
				if runCtx.Err() != nil {
					return
				}
				if err := fn(runCtx, index); err != nil {
					mu.Lock()
					if index < failedIndex {
						failedIndex, firstErr = index, err
					}
					mu.Unlock()
					cancel()
					return
				}
			}
		}()
	}

send:
	for index := 0; index < total; index++ {
		select {
		case jobs <- index:
		case <-runCtx.Done():
			break send
		}
	}
	close(jobs)
	wg.Wait()

	if firstErr != nil {
		return failedIndex, firstErr
	}
	if err := ctx.Err(); err != nil {
		return -1, err
	}
	return -1, nil
}
