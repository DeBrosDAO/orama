package inspector

import "sync"

// maxConcurrentCollectors bounds the SSH sessions open on one node at once. A
// node the inspector reads is often the one that is struggling; ten sessions
// at once would be load of the inspector's own making.
const maxConcurrentCollectors = 4

// collectorJob is one subsystem's collection: run fills the subsystem's field
// of the node's data and returns why it could not.
type collectorJob struct {
	subsystem string
	run       func() error
}

// runCollectors runs the jobs of one node, at most maxConcurrentCollectors at
// a time, and files each failure under its subsystem.
//
// The collectors are independent SSH sessions. Run one after another, a node
// whose every session costs seconds (a CPU-starved VPS takes 2-10s each, 49s
// in all) used up the whole --timeout before the last collectors started, and
// each of them was reported as not collected on a healthy node. Each job
// writes only its own field of nd; only the failure record is shared.
func runCollectors(nd *NodeData, jobs []collectorJob) {
	var (
		wg     sync.WaitGroup
		mu     sync.Mutex
		tokens = make(chan struct{}, maxConcurrentCollectors)
	)
	for _, job := range jobs {
		wg.Add(1)
		tokens <- struct{}{}
		go func(j collectorJob) {
			defer wg.Done()
			defer func() { <-tokens }()
			err := j.run()
			mu.Lock()
			defer mu.Unlock()
			nd.recordFailure(j.subsystem, err)
		}(job)
	}
	wg.Wait()
}
