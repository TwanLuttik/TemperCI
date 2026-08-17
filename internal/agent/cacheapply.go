package agent

import (
	"fmt"

	"github.com/TwanLuttik/TemperCI/internal/api"
	"github.com/TwanLuttik/TemperCI/internal/ghacache"
)

// ApplyCacheOps executes operator purge commands against the host cache store.
func ApplyCacheOps(st *ghacache.Store, ops []api.CacheOp) (int, error) {
	if st == nil {
		return 0, nil
	}
	n := 0
	for _, op := range ops {
		switch op.Action {
		case api.CacheOpPurgeAll:
			if _, _, err := st.DeleteAll(); err != nil {
				return n, fmt.Errorf("purge all: %w", err)
			}
			n++
		case api.CacheOpPurgeRepo:
			if _, _, err := st.DeleteRepo(op.Repo); err != nil {
				return n, fmt.Errorf("purge %s: %w", op.Repo, err)
			}
			n++
		default:
			return n, fmt.Errorf("unknown cache op %q", op.Action)
		}
	}
	return n, nil
}

// CacheUsageFromStore converts store inventory to the agent wire type.
func CacheUsageFromStore(st *ghacache.Store) *api.CacheUsage {
	if st == nil {
		return nil
	}
	u := st.Usage()
	out := &api.CacheUsage{Bytes: u.Bytes, MaxBytes: u.MaxBytes, Entries: u.Entries}
	for _, r := range u.Repos {
		out.Repos = append(out.Repos, api.CacheRepoUsage{
			Repo:       r.Repo,
			Bytes:      r.Bytes,
			Entries:    r.Entries,
			LastAccess: r.LastAccess,
		})
	}
	return out
}
