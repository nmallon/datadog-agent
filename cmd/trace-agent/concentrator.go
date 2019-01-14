package main

import (
	"sort"
	"sync"
	"time"

	log "github.com/cihub/seelog"

	"github.com/DataDog/datadog-agent/pkg/trace/agent"
	"github.com/DataDog/datadog-agent/pkg/trace/watchdog"
)

// defaultBufferLen represents the default buffer length; the number of bucket size
// units used by the concentrator.
const defaultBufferLen = 2

// Concentrator produces time bucketed statistics from a stream of raw traces.
// https://en.wikipedia.org/wiki/Knelson_concentrator
// Gets an imperial shitton of traces, and outputs pre-computed data structures
// allowing to find the gold (stats) amongst the traces.
type Concentrator struct {
	// list of attributes to use for extra aggregation
	aggregators []string
	// bucket duration in nanoseconds
	bsize int64
	// Timestamp of the oldest time bucket for which we allow data.
	// Any ingested stats older than it get added to this bucket.
	oldestTs int64
	// bufferLen is the number of 10s stats bucket we keep in memory before flushing them.
	// It means that we can compute stats only for the last `bufferLen * bsize` and that we
	// wait such time before flushing the stats.
	// This only applies to past buckets. Stats buckets in the future are allowed with no restriction.
	bufferLen int

	OutStats chan []agent.StatsBucket

	exit   chan struct{}
	exitWG *sync.WaitGroup

	buckets map[int64]*agent.StatsRawBucket // buckets used to aggregate stats per timestamp
	mu      sync.Mutex
}

// NewConcentrator initializes a new concentrator ready to be started
func NewConcentrator(aggregators []string, bsize int64, out chan []agent.StatsBucket) *Concentrator {
	c := Concentrator{
		aggregators: aggregators,
		bsize:       bsize,
		buckets:     make(map[int64]*agent.StatsRawBucket),
		// At start, only allow stats for the current time bucket. Ensure we don't
		// override buckets which could have been sent before an Agent restart.
		oldestTs: alignTs(time.Now().UnixNano(), bsize),
		// TODO: Move to configuration.
		bufferLen: defaultBufferLen,

		OutStats: out,

		exit:   make(chan struct{}),
		exitWG: &sync.WaitGroup{},
	}
	sort.Strings(c.aggregators)
	return &c
}

// Start starts the concentrator.
func (c *Concentrator) Start() {
	go func() {
		defer watchdog.LogOnPanic()
		c.Run()
	}()
}

// Run runs the main loop of the concentrator goroutine. Traces are received
// through `Add`, this loop only deals with flushing.
func (c *Concentrator) Run() {
	c.exitWG.Add(1)
	defer c.exitWG.Done()

	// flush with the same period as stats buckets
	flushTicker := time.NewTicker(time.Duration(c.bsize) * time.Nanosecond)
	defer flushTicker.Stop()

	log.Debug("starting concentrator")

	for {
		select {
		case <-flushTicker.C:
			c.OutStats <- c.Flush()
		case <-c.exit:
			log.Info("exiting concentrator, computing remaining stats")
			c.OutStats <- c.Flush()
			return
		}
	}
}

// Stop stops the main Run loop.
func (c *Concentrator) Stop() {
	close(c.exit)
	c.exitWG.Wait()
}

// Add appends to the proper stats bucket this trace's statistics
func (c *Concentrator) Add(t agent.ProcessedTrace) {
	c.addNow(t, time.Now().UnixNano())
}

func (c *Concentrator) addNow(t agent.ProcessedTrace, now int64) {
	c.mu.Lock()

	for _, s := range t.WeightedTrace {
		// We do not compute stats for non top level spans since this is not surfaced in the UI
		if !s.TopLevel {
			continue
		}
		end := s.Start + s.Duration
		btime := end - end%c.bsize

		// // If too far in the past, count in the oldest-allowed time bucket instead.
		if btime < c.oldestTs {
			btime = c.oldestTs
		}

		b, ok := c.buckets[btime]
		if !ok {
			b = agent.NewStatsRawBucket(btime, c.bsize)
			c.buckets[btime] = b
		}

		sublayers, _ := t.Sublayers[s.Span]
		b.HandleSpan(s, t.Env, c.aggregators, sublayers)
	}

	c.mu.Unlock()
}

// Flush deletes and returns complete statistic buckets
func (c *Concentrator) Flush() []agent.StatsBucket {
	return c.flushNow(time.Now().UnixNano())
}

func (c *Concentrator) flushNow(now int64) []agent.StatsBucket {
	var sb []agent.StatsBucket

	c.mu.Lock()
	for ts, srb := range c.buckets {
		// Always keep `bufferLen` buckets (default is 2: current + previous one).
		// This is a trade-off: we accept slightly late traces (clock skew and stuff)
		// but we delay flushing by at most `bufferLen` buckets.
		if ts > now-int64(c.bufferLen)*c.bsize {
			continue
		}
		log.Debugf("flushing bucket %d", ts)
		sb = append(sb, srb.Export())
		delete(c.buckets, ts)
	}

	// After flushing, update the oldest timestamp allowed to prevent having stats for
	// an already-flushed bucket.
	newOldestTs := alignTs(now, c.bsize) - int64(c.bufferLen-1)*c.bsize
	if newOldestTs > c.oldestTs {
		log.Debugf("update oldestTs to %d", newOldestTs)
		c.oldestTs = newOldestTs
	}

	c.mu.Unlock()

	return sb
}

// alignTs returns the provided timestamp truncated to the bucket size.
// It gives us the start time of the time bucket in which such timestamp falls.
func alignTs(ts int64, bsize int64) int64 {
	return ts - ts%bsize
}
