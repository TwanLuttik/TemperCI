package agent

import (
	"fmt"
	"sort"

	"github.com/TwanLuttik/TemperCI/internal/api"
	"github.com/TwanLuttik/TemperCI/internal/ghacache"
	"github.com/TwanLuttik/TemperCI/internal/ocicache"
)

// ApplyCacheOps executes operator purge commands against Actions and OCI stores.
func ApplyCacheOps(st *ghacache.Store, oci *ocicache.Store, ops []api.CacheOp) (int, error) {
	if st == nil && oci == nil {
		return 0, nil
	}
	n := 0
	for _, op := range ops {
		switch op.Action {
		case api.CacheOpPurgeAll:
			if st != nil {
				if _, _, err := st.DeleteAll(); err != nil {
					return n, fmt.Errorf("purge all: %w", err)
				}
			}
			if oci != nil {
				if _, _, err := oci.DeleteAll(); err != nil {
					return n, fmt.Errorf("purge oci all: %w", err)
				}
			}
			n++
		case api.CacheOpPurgeRepo:
			if st != nil {
				if _, _, err := st.DeleteRepo(op.Repo); err != nil {
					return n, fmt.Errorf("purge %s: %w", op.Repo, err)
				}
			}
			if oci != nil {
				if _, _, err := oci.DeleteRepo(op.Repo); err != nil {
					return n, fmt.Errorf("purge oci %s: %w", op.Repo, err)
				}
			}
			n++
		default:
			return n, fmt.Errorf("unknown cache op %q", op.Action)
		}
	}
	return n, nil
}

// CacheUsageFromStore converts Actions cache inventory to the agent wire type.
func CacheUsageFromStore(st *ghacache.Store) *api.CacheUsage {
	return CacheUsageFromStores(st, nil)
}

// CacheUsageFromStores merges Actions + OCI inventory. OCI rows are prefixed oci:.
func CacheUsageFromStores(st *ghacache.Store, oci *ocicache.Store) *api.CacheUsage {
	if st == nil && oci == nil {
		return nil
	}
	out := &api.CacheUsage{}
	if st != nil {
		u := st.Usage()
		out.Bytes = u.Bytes
		out.MaxBytes = u.MaxBytes
		out.Entries = u.Entries
		for _, r := range u.Repos {
			out.Repos = append(out.Repos, api.CacheRepoUsage{
				Repo:       r.Repo,
				Bytes:      r.Bytes,
				Entries:    r.Entries,
				LastAccess: r.LastAccess,
			})
		}
	}
	if oci != nil {
		u := oci.Usage()
		out.Bytes += u.Bytes
		out.MaxBytes += u.MaxBytes
		out.Entries += u.Entries
		for _, r := range u.Repos {
			out.Repos = append(out.Repos, api.CacheRepoUsage{
				Repo:       "oci:" + r.Repo,
				Bytes:      r.Bytes,
				Entries:    r.Entries,
				LastAccess: r.LastAccess,
			})
		}
	}
	return out
}

func uniqueSorted(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}
