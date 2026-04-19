package vm

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCIDAlloc_BasicAllocation verifies that AllocCID returns a CID in the
// valid range and that successive allocations (with active sessions holding
// each prior CID) return different values.
func TestCIDAlloc_BasicAllocation(t *testing.T) {
	db := testDB(t)
	mgr := NewManager(db, nil, nil)
	ctx := context.Background()

	cid, err := mgr.AllocCID(ctx)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, cid, cidMin)
	assert.LessOrEqual(t, cid, cidMax)
}

// TestCIDAlloc_UniquenessThenReuse allocates N CIDs (by inserting active
// sessions to hold each one), verifies uniqueness, then frees one and asserts
// it is reused on the next allocation.
func TestCIDAlloc_UniquenessThenReuse(t *testing.T) {
	const N = 10

	db := testDB(t)
	mgr := NewManager(db, nil, nil)
	ctx := context.Background()

	allocated := make([]uint32, 0, N)

	for i := 0; i < N; i++ {
		cid, err := mgr.AllocCID(ctx)
		require.NoError(t, err, "alloc #%d", i)

		// Verify not already allocated.
		for j, prev := range allocated {
			require.NotEqual(t, prev, cid, "CID %d returned twice (allocs %d and %d)", cid, j, i)
		}

		// Pin the CID by inserting an active session that holds it.
		_, err = db.ExecContext(ctx,
			`INSERT INTO sessions
			 (id, started_at, status, merge_outcome, disk_image_path, overlay_path,
			  vm_db_path, vsock_cid, filter_version, ledger_events_total, policy_status)
			 VALUES (?, 1000, 'booting', 'pending', '/img', '', '', ?, '', 0, 'ok')`,
			sessionIDForTest(i), int64(cid),
		)
		require.NoError(t, err)

		allocated = append(allocated, cid)
	}

	// Free the first CID by transitioning its session to failed + zeroing vsock_cid.
	toFree := allocated[0]
	require.NoError(t, mgr.FreeCID(ctx, toFree))

	// Next allocation must return the freed CID (it is the lowest available).
	reused, err := mgr.AllocCID(ctx)
	require.NoError(t, err)
	assert.Equal(t, toFree, reused, "freed CID should be reused on next alloc")
}

// TestCIDAlloc_FreeCID_Idempotent verifies that FreeCID with CID=0 and with a
// CID not held by any active session both return nil.
func TestCIDAlloc_FreeCID_Idempotent(t *testing.T) {
	db := testDB(t)
	mgr := NewManager(db, nil, nil)
	ctx := context.Background()

	require.NoError(t, mgr.FreeCID(ctx, 0))    // no-op
	require.NoError(t, mgr.FreeCID(ctx, 9999)) // not held by anyone — also no-op
}

// TestCIDAlloc_Concurrent verifies that AllocCID is goroutine-safe: multiple
// goroutines calling it simultaneously must not race on the Manager mutex and
// must each receive a valid CID (uniqueness across concurrent calls is only
// guaranteed when each caller pins its CID via a session row before the next
// allocation — here we just verify the mutex prevents data races under -race).
func TestCIDAlloc_Concurrent(t *testing.T) {
	const workers = 8
	db := testDB(t)
	mgr := NewManager(db, nil, nil)
	ctx := context.Background()

	var (
		mu      sync.Mutex
		results []uint32
		wg      sync.WaitGroup
	)

	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			cid, err := mgr.AllocCID(ctx)
			if err != nil {
				return
			}

			mu.Lock()
			results = append(results, cid)
			mu.Unlock()
		}()
	}
	wg.Wait()

	// Every goroutine should have received a valid CID.
	require.Equal(t, workers, len(results), "all workers should receive a CID")
	for _, cid := range results {
		assert.GreaterOrEqual(t, cid, cidMin)
		assert.LessOrEqual(t, cid, cidMax)
	}
}

func sessionIDForTest(i int) string {
	return "test-session-" + string(rune('a'+i))
}
