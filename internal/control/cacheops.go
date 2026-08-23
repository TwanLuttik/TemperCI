package control

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/TwanLuttik/TemperCI/internal/api"
)

// cacheQueue holds operator purge commands until the target agent heartbeats.
type cacheQueue struct {
	mu   sync.Mutex
	seq  atomic.Int64
	byID map[string][]api.CacheOp
}

func newCacheQueue() *cacheQueue {
	return &cacheQueue{byID: make(map[string][]api.CacheOp)}
}

func (q *cacheQueue) enqueue(agentIDs []string, action, repo string) int {
	if q == nil || len(agentIDs) == 0 {
		return 0
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	n := 0
	for _, id := range agentIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		op := api.CacheOp{
			ID:     strconv.FormatInt(q.seq.Add(1), 10),
			Action: action,
			Repo:   repo,
		}
		q.byID[id] = append(q.byID[id], op)
		n++
	}
	return n
}

func (q *cacheQueue) take(agentID string) []api.CacheOp {
	if q == nil || agentID == "" {
		return nil
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	ops := q.byID[agentID]
	if len(ops) == 0 {
		return nil
	}
	delete(q.byID, agentID)
	return ops
}

func cacheAction(repo string) string {
	if strings.TrimSpace(repo) == "" {
		return api.CacheOpPurgeAll
	}
	return api.CacheOpPurgeRepo
}

func cloneCacheRepos(in []api.CacheRepoUsage) []api.CacheRepoUsage {
	if in == nil {
		return nil
	}
	out := make([]api.CacheRepoUsage, len(in))
	for i, r := range in {
		out[i] = r
		if r.Keys != nil {
			out[i].Keys = append([]api.CacheEntryUsage(nil), r.Keys...)
		}
	}
	return out
}

func validateCacheRepo(repo string) error {
	repo = strings.TrimSpace(repo)
	if repo == "" {
		return nil
	}
	parts := strings.Split(repo, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return fmt.Errorf("repo must be org/name")
	}
	return nil
}
